package workspace

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"connectrpc.com/connect"

	"google.golang.org/protobuf/types/descriptorpb"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/scripting"
	"codeberg.org/ramilmsh/grpcview/service/store"
	"codeberg.org/ramilmsh/grpcview/service/wsroot"
)

// scriptingMaxPages is the wazero linear-memory ceiling in 64 KiB pages (256 MiB).
const scriptingMaxPages = 4096

// Workspace is the WorkspaceService handler: a thin adapter over store.Store that also owns the
// shared scripting Engine and the memo holding each collection's derived merged view.
type Workspace struct {
	store  *store.Store
	engine *scripting.Engine
	defs   *definitionsCache
}

// New returns a handler persisting collections under the workspace rooted at root, with local
// state (resolved-schema cache, run history) kept in root's OS-level state directory —
// wsroot.StateDir(root), never inside root itself — and the scripting engine compiled once up
// front. root is expected to already be resolved (see wsroot.Discover); this does no discovery
// of its own.
func New(ctx context.Context, root string) (Workspace, error) {
	stateRoot, err := wsroot.StateDir(root)
	if err != nil {
		return Workspace{}, fmt.Errorf("failed to resolve workspace state dir: %w", err)
	}
	engine, err := scripting.NewEngine(ctx, scriptingMaxPages)
	if err != nil {
		return Workspace{}, fmt.Errorf("failed to initialize scripting engine: %w", err)
	}
	return Workspace{
		store:  store.New(root, stateRoot, slog.Default()),
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
	case errors.Is(err, store.ErrInvalidCollectionID):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, store.ErrCollectionExists):
		return connect.NewError(connect.CodeAlreadyExists, err)
	case errors.Is(err, store.ErrWorkspaceTooLarge):
		// ResourceExhausted, not InvalidArgument: nothing about the workspace is malformed,
		// the scan is refusing to be unbounded — and the message already names the fix.
		return connect.NewError(connect.CodeResourceExhausted, err)
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
		return nil, toConnectError(err)
	}

	ws, err := w.loadCollection(ctx, coll)
	if err != nil {
		return nil, err
	}

	return connect.NewResponse(&grpcviewv1.GetResponse{Collection: ws}), nil
}

// ListCollections summarizes every collection in the workspace. A collection that cannot be
// summarized comes back as a row carrying its error, so one unparseable grpcview.json cannot
// hide the rest of the repo.
func (w Workspace) ListCollections(ctx context.Context, request *connect.Request[grpcviewv1.ListCollectionsRequest]) (*connect.Response[grpcviewv1.ListCollectionsResponse], error) {
	infos, err := w.store.List(ctx, request.Msg.GetRefresh())
	if err != nil {
		return nil, toConnectError(err)
	}
	collections := make([]*grpcviewv1.CollectionSummary, 0, len(infos))
	for _, info := range infos {
		collections = append(collections, &grpcviewv1.CollectionSummary{
			Id:          info.ID,
			Name:        info.Name,
			SourceCount: int32(info.SourceCount),
			Error:       info.Err,
		})
	}
	return connect.NewResponse(&grpcviewv1.ListCollectionsResponse{
		Root:        w.store.Root(),
		Collections: collections,
	}), nil
}

// CreateCollection is the one place a collection legitimately comes into existence: every
// other handler now requires one to already be there. An existing collection at this address
// is AlreadyExists, not silently reused.
func (w Workspace) CreateCollection(ctx context.Context, request *connect.Request[grpcviewv1.CreateCollectionRequest]) (*connect.Response[grpcviewv1.CreateCollectionResponse], error) {
	coll, err := w.store.Open(ctx, request.Msg.GetCollection())
	if err != nil {
		return nil, toConnectError(err)
	}
	if err := coll.Create(ctx, request.Msg.GetName()); err != nil {
		return nil, toConnectError(err)
	}
	// The cached listing is keyed by the workspace ROOT's mtime, and creating
	// services/payments/requests never touches the root — so the only writer that can add a
	// collection has to say so itself.
	w.store.InvalidateList()
	ws, err := w.loadCollection(ctx, coll)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&grpcviewv1.CreateCollectionResponse{Collection: ws}), nil
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
	reloaded, err := w.loadCollection(ctx, coll)
	if err != nil {
		return nil, err
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
	reloaded, err := w.loadCollection(ctx, coll)
	if err != nil {
		return nil, err
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
	reloaded, err := w.loadCollection(ctx, coll)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&grpcviewv1.ReorderDescriptorSourcesResponse{Collection: reloaded}), nil
}

// openWithSources deliberately loads WITHOUT the derived merged view (loadCollection): its
// callers are about to change the source list and re-derive anyway, so deriving the view they are
// replacing would be pure waste — and on a cold process it would be a merge of every blob.
func (w Workspace) openWithSources(ctx context.Context, name string) (*store.Collection, *grpcviewv1.Collection, error) {
	coll, err := w.store.Open(ctx, name)
	if err != nil {
		return nil, nil, toConnectError(err)
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
	reloaded, err := w.loadCollection(ctx, coll)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&grpcviewv1.RemoveDescriptorSourceResponse{Collection: reloaded}), nil
}

func (w Workspace) mutate(ctx context.Context, name string, fn func(*store.Collection) error) (*grpcviewv1.Collection, error) {
	coll, err := w.store.Open(ctx, name)
	if err != nil {
		return nil, toConnectError(err)
	}
	if err := fn(coll); err != nil {
		return nil, toConnectError(err)
	}
	return w.loadCollection(ctx, coll)
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
