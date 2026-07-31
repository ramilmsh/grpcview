package workspace

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"

	"connectrpc.com/connect"

	"google.golang.org/protobuf/types/descriptorpb"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/scripting"
	"codeberg.org/ramilmsh/grpcview/service/store"
)

// scriptingMaxPages is the outer wazero linear-memory ceiling for the shared
// scripting engine, in 64 KiB pages: 4096 pages = 256 MiB. That sits above the
// most generous profile's inner heap cap (Scenario, 128 MiB) so a legitimate run
// can use its full inner budget with headroom, while still being a hard backstop
// enforced by wazero underneath QuickJS's own accounting.
const scriptingMaxPages = 4096

// Workspace is the WorkspaceService handler. It is a thin adapter over
// store.Store: each mutation delegates to the store and then reloads the whole
// Workspace so responses keep the exact shape the client already expects. It also
// owns the shared scripting Engine (compiled once, instances reused/pooled) that
// backs the RunScript RPC.
type Workspace struct {
	store  *store.Store
	engine *scripting.Engine
}

// New returns a handler persisting collections under the same base the previous
// blob storage used: os.UserConfigDir()/.grpcview/<name>. It also compiles the
// scripting engine once here (the ~660 KiB module compile is the expensive step;
// instances are cheap) so RunScript can serve from a warm engine.
func New(ctx context.Context) (Workspace, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return Workspace{}, fmt.Errorf("failed to get user config dir: %w", err)
	}
	engine, err := scripting.NewEngine(ctx, scriptingMaxPages)
	if err != nil {
		return Workspace{}, fmt.Errorf("failed to initialize scripting engine: %w", err)
	}
	return Workspace{
		store:  store.New(filepath.Join(configDir, ".grpcview"), slog.Default()),
		engine: engine,
	}, nil
}

func (w Workspace) Close(ctx context.Context) error {
	if w.engine != nil {
		return w.engine.Close(ctx)
	}
	return nil
}

// toConnectError maps the store's transport-agnostic sentinel errors onto the
// Connect error codes the client relies on; other errors pass through.
func toConnectError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrItemNotFound), errors.Is(err, store.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, store.ErrNotAFolder), errors.Is(err, store.ErrNotARequest),
		errors.Is(err, store.ErrAlreadyExists):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	default:
		return err
	}
}

func (w Workspace) Get(ctx context.Context, request *connect.Request[grpcviewv1.GetRequest]) (*connect.Response[grpcviewv1.GetResponse], error) {
	coll, err := w.store.Open(ctx, request.Msg.GetWorkspaceName())
	if err != nil {
		return nil, err
	}

	ws, err := coll.Load(ctx)
	if errors.Is(err, store.ErrNotFound) {
		if err := coll.EnsureCreated(ctx); err != nil {
			return nil, fmt.Errorf("failed to create workspace: %w", err)
		}
		ws, err = coll.Load(ctx)
	}
	if err != nil {
		return nil, toConnectError(err)
	}

	return connect.NewResponse(&grpcviewv1.GetResponse{Workspace: ws}), nil
}

// AddDescriptorSource adds (or, when the id already exists, refreshes in place) a
// descriptor source. Only this one source is resolved — the merged view is then
// re-derived from every source's cached resolve, so adding a source can never
// half-overwrite what another already contributed. A new source lands at LOWEST
// priority, so adding one never moves an existing service to a different source.
func (w Workspace) AddDescriptorSource(ctx context.Context, request *connect.Request[grpcviewv1.AddDescriptorSourceRequest]) (*connect.Response[grpcviewv1.AddDescriptorSourceResponse], error) {
	coll, ws, err := w.openWithSources(ctx, request.Msg.GetWorkspaceName())
	if err != nil {
		return nil, err
	}

	var src *grpcviewv1.DescriptorSource
	uploads := map[string]*descriptorpb.FileDescriptorSet{}
	switch source := request.Msg.GetSource().(type) {
	case *grpcviewv1.AddDescriptorSourceRequest_DescriptorSet:
		fileName := request.Msg.GetFileName()
		if fileName == "" {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				errors.New("a descriptor-set upload needs a file_name — it is the source's identity"))
		}
		fds, err := parseUpload(source.DescriptorSet)
		if err != nil {
			return nil, err
		}
		src = &grpcviewv1.DescriptorSource{
			Source: &grpcviewv1.DescriptorSource_Upload{Upload: &grpcviewv1.Upload{FileName: fileName}},
		}
		src.Id = sourceID(src)
		uploads[src.GetId()] = fds
	case *grpcviewv1.AddDescriptorSourceRequest_Reflection:
		src = &grpcviewv1.DescriptorSource{
			Source: &grpcviewv1.DescriptorSource_Reflection{Reflection: source.Reflection},
		}
		src.Id = sourceID(src)
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unknown source type: <%T> %+v", source, source))
	}

	// Resolve the new source up front so an unreachable target (or bytes that don't
	// parse) is an error the user sees, rather than a listed source that silently
	// provides nothing.
	var fresh *resolvedSource
	if fds, ok := uploads[src.GetId()]; ok {
		fresh, err = resolveUpload(src.GetId(), fds)
	} else {
		fresh, err = resolveReflection(ctx, src.GetId(), src.GetReflection())
	}
	if err != nil {
		return nil, err
	}

	sources := upsertSource(slices.Clone(ws.GetSources()), src)
	if err := w.putDescriptorState(ctx, coll, sources, map[string]*resolvedSource{src.GetId(): fresh}, uploads); err != nil {
		return nil, err
	}
	reloaded, err := coll.Load(ctx)
	if err != nil {
		return nil, toConnectError(err)
	}
	return connect.NewResponse(&grpcviewv1.AddDescriptorSourceResponse{Workspace: reloaded}), nil
}

// RefreshDescriptorSource re-resolves exactly one source — re-dialing a reflection
// target to pick up a redeployed schema, or re-linking an upload — and re-derives
// the merged view. Sibling sources keep their cached resolves untouched, so
// refreshing one never reaches the network on behalf of another.
func (w Workspace) RefreshDescriptorSource(ctx context.Context, request *connect.Request[grpcviewv1.RefreshDescriptorSourceRequest]) (*connect.Response[grpcviewv1.RefreshDescriptorSourceResponse], error) {
	coll, ws, err := w.openWithSources(ctx, request.Msg.GetWorkspaceName())
	if err != nil {
		return nil, err
	}
	sources := ws.GetSources()
	index := slices.IndexFunc(sources, func(s *grpcviewv1.DescriptorSource) bool { return s.GetId() == request.Msg.GetId() })
	if index == -1 {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("unknown source id %q", request.Msg.GetId()))
	}

	fresh, err := w.resolveOne(ctx, coll, sources[index])
	if err != nil {
		return nil, err
	}
	if err := w.putDescriptorState(ctx, coll, sources, map[string]*resolvedSource{fresh.id: fresh}, nil); err != nil {
		return nil, err
	}
	reloaded, err := coll.Load(ctx)
	if err != nil {
		return nil, toConnectError(err)
	}
	return connect.NewResponse(&grpcviewv1.RefreshDescriptorSourceResponse{Workspace: reloaded}), nil
}

// ReorderDescriptorSources sets the source priority order and re-derives the
// merged view from the cached resolves — no network. This is how the user switches
// which source's definitions win when several describe the same protos.
func (w Workspace) ReorderDescriptorSources(ctx context.Context, request *connect.Request[grpcviewv1.ReorderDescriptorSourcesRequest]) (*connect.Response[grpcviewv1.ReorderDescriptorSourcesResponse], error) {
	coll, ws, err := w.openWithSources(ctx, request.Msg.GetWorkspaceName())
	if err != nil {
		return nil, err
	}
	sources, err := reorderSources(ws.GetSources(), request.Msg.GetIds())
	if err != nil {
		return nil, err
	}
	if err := w.putDescriptorState(ctx, coll, sources, nil, nil); err != nil {
		return nil, err
	}
	reloaded, err := coll.Load(ctx)
	if err != nil {
		return nil, toConnectError(err)
	}
	return connect.NewResponse(&grpcviewv1.ReorderDescriptorSourcesResponse{Workspace: reloaded}), nil
}

// openWithSources is the shared prologue of the four source RPCs: open the
// collection and read its current source list.
func (w Workspace) openWithSources(ctx context.Context, name string) (*store.Collection, *grpcviewv1.Workspace, error) {
	coll, err := w.store.Open(ctx, name)
	if err != nil {
		return nil, nil, err
	}
	ws, err := coll.Load(ctx)
	if err != nil {
		return nil, nil, toConnectError(err)
	}
	return coll, ws, nil
}

// RemoveDescriptorSource drops the source with the given id and re-derives the
// merged view from the cached resolves of those that remain — no network, so an
// unreachable sibling source can never block a removal.
func (w Workspace) RemoveDescriptorSource(ctx context.Context, request *connect.Request[grpcviewv1.RemoveDescriptorSourceRequest]) (*connect.Response[grpcviewv1.RemoveDescriptorSourceResponse], error) {
	coll, ws, err := w.openWithSources(ctx, request.Msg.GetWorkspaceName())
	if err != nil {
		return nil, err
	}
	sources := slices.Clone(ws.GetSources())
	index := slices.IndexFunc(sources, func(s *grpcviewv1.DescriptorSource) bool { return s.GetId() == request.Msg.GetId() })
	if index == -1 {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("unknown source id %q", request.Msg.GetId()))
	}

	if err := w.putDescriptorState(ctx, coll, slices.Delete(sources, index, index+1), nil, nil); err != nil {
		return nil, err
	}
	reloaded, err := coll.Load(ctx)
	if err != nil {
		return nil, toConnectError(err)
	}
	return connect.NewResponse(&grpcviewv1.RemoveDescriptorSourceResponse{Workspace: reloaded}), nil
}

// mutate applies a single store mutation to the named collection and returns the
// reloaded workspace, mapping store sentinels to Connect codes. The four
// tree-mutating RPCs share this open→mutate→reload→map policy and differ only in
// the response type, which each caller wraps.
func (w Workspace) mutate(ctx context.Context, name string, fn func(*store.Collection) error) (*grpcviewv1.Workspace, error) {
	coll, err := w.store.Open(ctx, name)
	if err != nil {
		return nil, err
	}
	if err := fn(coll); err != nil {
		return nil, toConnectError(err)
	}
	ws, err := coll.Load(ctx)
	if err != nil {
		return nil, toConnectError(err)
	}
	return ws, nil
}

// CreateFolder implements [grpcviewv1.WorkspaceServiceHandler].
func (w Workspace) CreateFolder(ctx context.Context, request *connect.Request[grpcviewv1.CreateFolderRequest]) (*connect.Response[grpcviewv1.CreateFolderResponse], error) {
	ws, err := w.mutate(ctx, request.Msg.GetWorkspaceName(), func(coll *store.Collection) error {
		return coll.CreateFolder(ctx, request.Msg.GetPath(), request.Msg.GetItemName())
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&grpcviewv1.CreateFolderResponse{Workspace: ws}), nil
}

// CreateRequest implements [grpcviewv1.WorkspaceServiceHandler].
func (w Workspace) CreateRequest(ctx context.Context, request *connect.Request[grpcviewv1.CreateRequestRequest]) (*connect.Response[grpcviewv1.CreateRequestResponse], error) {
	ws, err := w.mutate(ctx, request.Msg.GetWorkspaceName(), func(coll *store.Collection) error {
		return coll.CreateRequest(ctx, request.Msg.GetPath(), request.Msg.GetItemName(), request.Msg.GetService(), request.Msg.GetMethod())
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&grpcviewv1.CreateRequestResponse{Workspace: ws}), nil
}

// DeleteRequest implements [grpcviewv1.WorkspaceServiceHandler]. It removes any
// item (folder or request) by name, matching the previous behavior.
func (w Workspace) DeleteRequest(ctx context.Context, request *connect.Request[grpcviewv1.DeleteRequestRequest]) (*connect.Response[grpcviewv1.DeleteRequestResponse], error) {
	ws, err := w.mutate(ctx, request.Msg.GetWorkspaceName(), func(coll *store.Collection) error {
		return coll.Delete(ctx, request.Msg.GetPath(), request.Msg.GetItemName())
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&grpcviewv1.DeleteRequestResponse{Workspace: ws}), nil
}

// UpdateRequest implements [grpcviewv1.WorkspaceServiceHandler].
func (w Workspace) UpdateRequest(ctx context.Context, request *connect.Request[grpcviewv1.UpdateRequestRequest]) (*connect.Response[grpcviewv1.UpdateRequestResponse], error) {
	patch := store.RequestPatch{
		Name:                request.Msg.Name,
		Service:             request.Msg.Service,
		Method:              request.Msg.Method,
		DraftBody:           request.Msg.DraftBody,
		DraftMetadataScript: request.Msg.DraftMetadataScript, // optional *string, like DraftBody
		Middleware:          request.Msg.GetMiddleware(),
		SetMiddleware:       request.Msg.GetUpdateMiddleware(),
		Target:              request.Msg.GetTarget(),
		SetTarget:           request.Msg.GetUpdateTarget(),
	}
	ws, err := w.mutate(ctx, request.Msg.GetWorkspaceName(), func(coll *store.Collection) error {
		return coll.UpdateRequest(ctx, request.Msg.GetPath(), request.Msg.GetItemName(), patch)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&grpcviewv1.UpdateRequestResponse{Workspace: ws}), nil
}

// UpdateFolder implements [grpcviewv1.WorkspaceServiceHandler].
func (w Workspace) UpdateFolder(ctx context.Context, request *connect.Request[grpcviewv1.UpdateFolderRequest]) (*connect.Response[grpcviewv1.UpdateFolderResponse], error) {
	patch := store.FolderPatch{
		Name:                request.Msg.Name,
		DraftMetadataScript: request.Msg.DraftMetadataScript,
	}
	ws, err := w.mutate(ctx, request.Msg.GetWorkspaceName(), func(coll *store.Collection) error {
		return coll.UpdateFolder(ctx, request.Msg.GetPath(), request.Msg.GetItemName(), patch)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&grpcviewv1.UpdateFolderResponse{Workspace: ws}), nil
}

// CreateScript implements [grpcviewv1.WorkspaceServiceHandler].
func (w Workspace) CreateScript(ctx context.Context, request *connect.Request[grpcviewv1.CreateScriptRequest]) (*connect.Response[grpcviewv1.CreateScriptResponse], error) {
	ws, err := w.mutate(ctx, request.Msg.GetWorkspaceName(), func(coll *store.Collection) error {
		return coll.CreateScript(ctx, request.Msg.GetName(), request.Msg.GetKind())
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&grpcviewv1.CreateScriptResponse{Workspace: ws}), nil
}

// UpdateScript implements [grpcviewv1.WorkspaceServiceHandler]. It patches a script's
// source and/or renames it; NewName maps to the store's rename (slug stays stable).
func (w Workspace) UpdateScript(ctx context.Context, request *connect.Request[grpcviewv1.UpdateScriptRequest]) (*connect.Response[grpcviewv1.UpdateScriptResponse], error) {
	patch := store.ScriptPatch{
		Name:   request.Msg.NewName,
		Source: request.Msg.Source,
	}
	ws, err := w.mutate(ctx, request.Msg.GetWorkspaceName(), func(coll *store.Collection) error {
		return coll.UpdateScript(ctx, request.Msg.GetName(), patch)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&grpcviewv1.UpdateScriptResponse{Workspace: ws}), nil
}

// DeleteScript implements [grpcviewv1.WorkspaceServiceHandler].
func (w Workspace) DeleteScript(ctx context.Context, request *connect.Request[grpcviewv1.DeleteScriptRequest]) (*connect.Response[grpcviewv1.DeleteScriptResponse], error) {
	ws, err := w.mutate(ctx, request.Msg.GetWorkspaceName(), func(coll *store.Collection) error {
		return coll.DeleteScript(ctx, request.Msg.GetName())
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&grpcviewv1.DeleteScriptResponse{Workspace: ws}), nil
}
