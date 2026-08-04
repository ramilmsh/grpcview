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

// scriptingMaxPages is the wazero linear-memory ceiling in 64 KiB pages (256 MiB).
const scriptingMaxPages = 4096

// Workspace is the WorkspaceService handler: a thin adapter over store.Store that also owns the
// shared scripting Engine and the linked-definitions memo.
type Workspace struct {
	store  *store.Store
	engine *scripting.Engine
	defs   *definitionsCache
}

// New returns a handler persisting collections under os.UserConfigDir()/.grpcview/<name>, with
// the scripting engine compiled once up front.
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
		defs:   newDefinitionsCache(),
	}, nil
}

func (w Workspace) Close(ctx context.Context) error {
	if w.engine != nil {
		return w.engine.Close(ctx)
	}
	return nil
}

func toConnectError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrItemNotFound), errors.Is(err, store.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, store.ErrNotAFolder), errors.Is(err, store.ErrNotARequest),
		errors.Is(err, store.ErrAlreadyExists), errors.Is(err, store.ErrMoveIntoDescendant):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	default:
		return err
	}
}

func (w Workspace) Get(ctx context.Context, request *connect.Request[grpcviewv1.GetRequest]) (*connect.Response[grpcviewv1.GetResponse], error) {
	coll, err := w.store.Open(ctx, request.Msg.GetCollection())
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

	return connect.NewResponse(&grpcviewv1.GetResponse{Collection: ws}), nil
}

// AddDescriptorSource adds (or, when the id already exists, refreshes in place) a descriptor
// source at LOWEST priority.
func (w Workspace) AddDescriptorSource(ctx context.Context, request *connect.Request[grpcviewv1.AddDescriptorSourceRequest]) (*connect.Response[grpcviewv1.AddDescriptorSourceResponse], error) {
	coll, ws, err := w.openWithSources(ctx, request.Msg.GetCollection())
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
	return connect.NewResponse(&grpcviewv1.AddDescriptorSourceResponse{Collection: reloaded}), nil
}

// RefreshDescriptorSource re-resolves exactly one source and re-derives the merged view.
func (w Workspace) RefreshDescriptorSource(ctx context.Context, request *connect.Request[grpcviewv1.RefreshDescriptorSourceRequest]) (*connect.Response[grpcviewv1.RefreshDescriptorSourceResponse], error) {
	coll, ws, err := w.openWithSources(ctx, request.Msg.GetCollection())
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
	return connect.NewResponse(&grpcviewv1.RefreshDescriptorSourceResponse{Collection: reloaded}), nil
}

// ReorderDescriptorSources sets the source priority order and re-derives the merged view from
// the cached resolves — no network.
func (w Workspace) ReorderDescriptorSources(ctx context.Context, request *connect.Request[grpcviewv1.ReorderDescriptorSourcesRequest]) (*connect.Response[grpcviewv1.ReorderDescriptorSourcesResponse], error) {
	coll, ws, err := w.openWithSources(ctx, request.Msg.GetCollection())
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
	return connect.NewResponse(&grpcviewv1.ReorderDescriptorSourcesResponse{Collection: reloaded}), nil
}

func (w Workspace) openWithSources(ctx context.Context, name string) (*store.Collection, *grpcviewv1.Collection, error) {
	coll, err := w.store.Open(ctx, name)
	if err != nil {
		return nil, nil, err
	}
	if err := coll.EnsureCreated(ctx); err != nil {
		return nil, nil, fmt.Errorf("failed to create workspace: %w", err)
	}
	ws, err := coll.Load(ctx)
	if err != nil {
		return nil, nil, toConnectError(err)
	}
	return coll, ws, nil
}

// RemoveDescriptorSource drops one source and re-derives the merged view from the cached
// resolves of those that remain — no network.
func (w Workspace) RemoveDescriptorSource(ctx context.Context, request *connect.Request[grpcviewv1.RemoveDescriptorSourceRequest]) (*connect.Response[grpcviewv1.RemoveDescriptorSourceResponse], error) {
	coll, ws, err := w.openWithSources(ctx, request.Msg.GetCollection())
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
	return connect.NewResponse(&grpcviewv1.RemoveDescriptorSourceResponse{Collection: reloaded}), nil
}

func (w Workspace) mutate(ctx context.Context, name string, fn func(*store.Collection) error) (*grpcviewv1.Collection, error) {
	coll, err := w.store.Open(ctx, name)
	if err != nil {
		return nil, err
	}
	if err := coll.EnsureCreated(ctx); err != nil {
		return nil, fmt.Errorf("failed to create workspace: %w", err)
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

func (w Workspace) CreateFolder(ctx context.Context, request *connect.Request[grpcviewv1.CreateFolderRequest]) (*connect.Response[grpcviewv1.CreateFolderResponse], error) {
	ws, err := w.mutate(ctx, request.Msg.GetCollection(), func(coll *store.Collection) error {
		return coll.CreateFolder(ctx, request.Msg.GetPath(), request.Msg.GetItemName())
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&grpcviewv1.CreateFolderResponse{Collection: ws}), nil
}

func (w Workspace) CreateRequest(ctx context.Context, request *connect.Request[grpcviewv1.CreateRequestRequest]) (*connect.Response[grpcviewv1.CreateRequestResponse], error) {
	ws, err := w.mutate(ctx, request.Msg.GetCollection(), func(coll *store.Collection) error {
		return coll.CreateRequest(ctx, request.Msg.GetPath(), request.Msg.GetItemName(), request.Msg.GetService(), request.Msg.GetMethod())
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&grpcviewv1.CreateRequestResponse{Collection: ws}), nil
}

// DeleteRequest removes any item — folder or request — by name.
func (w Workspace) DeleteRequest(ctx context.Context, request *connect.Request[grpcviewv1.DeleteRequestRequest]) (*connect.Response[grpcviewv1.DeleteRequestResponse], error) {
	ws, err := w.mutate(ctx, request.Msg.GetCollection(), func(coll *store.Collection) error {
		return coll.Delete(ctx, request.Msg.GetPath(), request.Msg.GetItemName())
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&grpcviewv1.DeleteRequestResponse{Collection: ws}), nil
}

func (w Workspace) UpdateRequest(ctx context.Context, request *connect.Request[grpcviewv1.UpdateRequestRequest]) (*connect.Response[grpcviewv1.UpdateRequestResponse], error) {
	patch := store.RequestPatch{
		Name:                request.Msg.Name,
		Service:             request.Msg.Service,
		Method:              request.Msg.Method,
		DraftBody:           request.Msg.DraftBody,
		DraftMetadataScript: request.Msg.DraftMetadataScript,
		Middleware:          request.Msg.GetMiddleware(),
		SetMiddleware:       request.Msg.GetUpdateMiddleware(),
		Target:              request.Msg.GetTarget(),
		SetTarget:           request.Msg.GetUpdateTarget(),
	}
	ws, err := w.mutate(ctx, request.Msg.GetCollection(), func(coll *store.Collection) error {
		return coll.UpdateRequest(ctx, request.Msg.GetPath(), request.Msg.GetItemName(), patch)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&grpcviewv1.UpdateRequestResponse{Collection: ws}), nil
}

func (w Workspace) UpdateFolder(ctx context.Context, request *connect.Request[grpcviewv1.UpdateFolderRequest]) (*connect.Response[grpcviewv1.UpdateFolderResponse], error) {
	patch := store.FolderPatch{
		Name:                request.Msg.Name,
		DraftMetadataScript: request.Msg.DraftMetadataScript,
	}
	ws, err := w.mutate(ctx, request.Msg.GetCollection(), func(coll *store.Collection) error {
		return coll.UpdateFolder(ctx, request.Msg.GetPath(), request.Msg.GetItemName(), patch)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&grpcviewv1.UpdateFolderResponse{Collection: ws}), nil
}

// MoveItem passes `before` as the raw *string: GetBefore() would collapse "unset" (append at
// the end) into "" (insert before an item literally named "").
func (w Workspace) MoveItem(ctx context.Context, request *connect.Request[grpcviewv1.MoveItemRequest]) (*connect.Response[grpcviewv1.MoveItemResponse], error) {
	ws, err := w.mutate(ctx, request.Msg.GetCollection(), func(coll *store.Collection) error {
		return coll.Move(ctx, request.Msg.GetPath(), request.Msg.GetItemName(), request.Msg.GetNewPath(), request.Msg.Before)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&grpcviewv1.MoveItemResponse{Collection: ws}), nil
}

func (w Workspace) CreateScript(ctx context.Context, request *connect.Request[grpcviewv1.CreateScriptRequest]) (*connect.Response[grpcviewv1.CreateScriptResponse], error) {
	ws, err := w.mutate(ctx, request.Msg.GetCollection(), func(coll *store.Collection) error {
		return coll.CreateScript(ctx, request.Msg.GetName(), request.Msg.GetKind())
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&grpcviewv1.CreateScriptResponse{Collection: ws}), nil
}

func (w Workspace) UpdateScript(ctx context.Context, request *connect.Request[grpcviewv1.UpdateScriptRequest]) (*connect.Response[grpcviewv1.UpdateScriptResponse], error) {
	patch := store.ScriptPatch{
		Name:   request.Msg.NewName,
		Source: request.Msg.Source,
	}
	ws, err := w.mutate(ctx, request.Msg.GetCollection(), func(coll *store.Collection) error {
		return coll.UpdateScript(ctx, request.Msg.GetName(), patch)
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&grpcviewv1.UpdateScriptResponse{Collection: ws}), nil
}

func (w Workspace) DeleteScript(ctx context.Context, request *connect.Request[grpcviewv1.DeleteScriptRequest]) (*connect.Response[grpcviewv1.DeleteScriptResponse], error) {
	ws, err := w.mutate(ctx, request.Msg.GetCollection(), func(coll *store.Collection) error {
		return coll.DeleteScript(ctx, request.Msg.GetName())
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&grpcviewv1.DeleteScriptResponse{Collection: ws}), nil
}
