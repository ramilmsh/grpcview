package workspace

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"connectrpc.com/connect"

	"google.golang.org/protobuf/proto"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/bazelbuild"
	"codeberg.org/ramilmsh/grpcview/service/scripting"
	"codeberg.org/ramilmsh/grpcview/service/store"
	"codeberg.org/ramilmsh/grpcview/service/wsroot"
)

const scriptingMaxPages = 4096

type Workspace struct {
	store  *store.Store
	engine *scripting.Engine
	defs   *definitionsCache
}

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

func (w Workspace) ListCollections(ctx context.Context, request *connect.Request[grpcviewv1.ListCollectionsRequest]) (*connect.Response[grpcviewv1.ListCollectionsResponse], error) {
	infos, err := w.store.List(ctx)
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
	trusted, err := wsroot.IsTrusted(w.store.Root())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal,
			fmt.Errorf("failed to read whether this workspace is trusted: %w", err))
	}
	return connect.NewResponse(&grpcviewv1.ListCollectionsResponse{
		Root:        w.store.Root(),
		Collections: collections,
		Trusted:     trusted,
	}), nil
}

func (w Workspace) SetWorkspaceTrust(_ context.Context, request *connect.Request[grpcviewv1.SetWorkspaceTrustRequest]) (*connect.Response[grpcviewv1.SetWorkspaceTrustResponse], error) {
	root := w.store.Root()
	var err error
	if request.Msg.GetTrusted() {
		err = wsroot.Trust(root)
	} else {
		err = wsroot.Revoke(root)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to record workspace trust: %w", err))
	}
	trusted, err := wsroot.IsTrusted(root)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal,
			fmt.Errorf("failed to read whether this workspace is trusted: %w", err))
	}
	return connect.NewResponse(&grpcviewv1.SetWorkspaceTrustResponse{Trusted: trusted}), nil
}

func (w Workspace) ListBazelTargets(ctx context.Context, _ *connect.Request[grpcviewv1.ListBazelTargetsRequest]) (*connect.Response[grpcviewv1.ListBazelTargetsResponse], error) {
	builder, err := w.bazelBuilder()
	if err != nil {
		return nil, err
	}
	labels, warning, err := builder.QueryTargets(ctx)
	if err != nil {
		return nil, bazelResolveError(ctx, err)
	}
	return connect.NewResponse(&grpcviewv1.ListBazelTargetsResponse{Labels: labels, Warning: warning}), nil
}

func (w Workspace) CreateCollection(ctx context.Context, request *connect.Request[grpcviewv1.CreateCollectionRequest]) (*connect.Response[grpcviewv1.CreateCollectionResponse], error) {
	coll, err := w.store.Open(ctx, request.Msg.GetCollection())
	if err != nil {
		return nil, toConnectError(err)
	}
	if err := coll.Create(ctx, request.Msg.GetName()); err != nil {
		return nil, toConnectError(err)
	}
	ws, err := w.loadCollection(ctx, coll)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&grpcviewv1.CreateCollectionResponse{Collection: ws}), nil
}

func (w Workspace) UpdateCollection(ctx context.Context, request *connect.Request[grpcviewv1.UpdateCollectionRequest]) (*connect.Response[grpcviewv1.UpdateCollectionResponse], error) {
	coll, err := w.store.Open(ctx, request.Msg.GetCollection())
	if err != nil {
		return nil, toConnectError(err)
	}
	if request.Msg.Name != nil {
		if err := coll.SetName(ctx, request.Msg.GetName()); err != nil {
			return nil, toConnectError(err)
		}
	}

	if request.Msg.NewCollection != nil && request.Msg.GetNewCollection() != request.Msg.GetCollection() {
		// The memo is keyed by collection id, and the rename changes it: drop the old entry while it is still
		// addressable, or nothing will ever invalidate it again.
		w.defs.invalidate(coll.Key())
		moved, err := w.store.Rename(ctx, request.Msg.GetCollection(), request.Msg.GetNewCollection())
		if err != nil {
			return nil, toConnectError(err)
		}
		coll = moved
	}

	ws, err := w.loadCollection(ctx, coll)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&grpcviewv1.UpdateCollectionResponse{Collection: ws}), nil
}

func (w Workspace) AddDescriptorSource(ctx context.Context, request *connect.Request[grpcviewv1.AddDescriptorSourceRequest]) (*connect.Response[grpcviewv1.AddDescriptorSourceResponse], error) {
	coll, ws, err := w.openWithSources(ctx, request.Msg.GetCollection())
	if err != nil {
		return nil, err
	}

	var (
		src   *grpcviewv1.DescriptorSource
		fresh *resolvedSource
	)
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
		var recipe string
		if p := request.Msg.GetPath(); p != "" {
			_, rel, err := resolveWorkspaceFile(w.store.Root(), p)
			if err != nil {
				slog.Default().Warn("upload is not refreshable: its file is outside the workspace",
					"file_name", fileName, "path", p, "root", w.store.Root(), "error", err)
			} else {
				recipe = rel
			}
		}
		src = &grpcviewv1.DescriptorSource{
			Source: &grpcviewv1.DescriptorSource_Upload{
				Upload: &grpcviewv1.Upload{FileName: fileName, Path: recipe},
			},
			CommitDescriptors: request.Msg.GetCommitDescriptors(),
		}
		src.Id = sourceID(src)
		if fresh, err = resolveUpload(src.GetId(), fds); err != nil {
			return nil, err
		}
	case *grpcviewv1.AddDescriptorSourceRequest_Reflection:
		src = &grpcviewv1.DescriptorSource{
			Source:            &grpcviewv1.DescriptorSource_Reflection{Reflection: source.Reflection},
			CommitDescriptors: request.Msg.GetCommitDescriptors(),
		}
		src.Id = sourceID(src)
		if fresh, err = resolveReflection(ctx, src.GetId(), src.GetReflection()); err != nil {
			return nil, err
		}
	case *grpcviewv1.AddDescriptorSourceRequest_Bazel:
		label, err := bazelbuild.CanonicalLabel(source.Bazel.GetLabel())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		src = &grpcviewv1.DescriptorSource{
			Source:            &grpcviewv1.DescriptorSource_Bazel{Bazel: &grpcviewv1.Bazel{Label: label}},
			CommitDescriptors: request.Msg.GetCommitDescriptors(),
		}
		src.Id = sourceID(src)
		if fresh, err = w.resolveBazel(ctx, src.GetId(), label); err != nil {
			return nil, err
		}
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unknown source type: <%T> %+v", source, source))
	}

	if i := slices.IndexFunc(ws.GetSources(), func(s *grpcviewv1.DescriptorSource) bool {
		return s.GetId() == src.GetId()
	}); i != -1 && ws.GetSources()[i].GetCommitDescriptors() {
		// commit_descriptors is STICKY across a re-add: an add whose id already exists is the documented
		// refresh gesture, so re-adding with the box unticked must not silently un-commit a source and delete
		// the sidecar the repo carries. A re-add can turn committing ON but never off; SetDescriptorSourceCommit
		// is the one way off.
		src.CommitDescriptors = true
	}

	sources := upsertSource(slices.Clone(ws.GetSources()), src)
	if err := w.putDescriptorState(ctx, coll, sources, map[string]*resolvedSource{src.GetId(): fresh}); err != nil {
		return nil, err
	}
	reloaded, err := w.loadCollection(ctx, coll)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&grpcviewv1.AddDescriptorSourceResponse{Collection: reloaded}), nil
}

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

	fresh, err := w.resolveOne(ctx, sources[index])
	if err != nil {
		return nil, err
	}
	if err := w.putDescriptorState(ctx, coll, sources, map[string]*resolvedSource{fresh.id: fresh}); err != nil {
		return nil, err
	}
	reloaded, err := w.loadCollection(ctx, coll)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&grpcviewv1.RefreshDescriptorSourceResponse{Collection: reloaded}), nil
}

func (w Workspace) ReorderDescriptorSources(ctx context.Context, request *connect.Request[grpcviewv1.ReorderDescriptorSourcesRequest]) (*connect.Response[grpcviewv1.ReorderDescriptorSourcesResponse], error) {
	coll, ws, err := w.openWithSources(ctx, request.Msg.GetCollection())
	if err != nil {
		return nil, err
	}
	sources, err := reorderSources(ws.GetSources(), request.Msg.GetIds())
	if err != nil {
		return nil, err
	}
	if err := w.putDescriptorState(ctx, coll, sources, nil); err != nil {
		return nil, err
	}
	reloaded, err := w.loadCollection(ctx, coll)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&grpcviewv1.ReorderDescriptorSourcesResponse{Collection: reloaded}), nil
}

func (w Workspace) SetDescriptorSourceCommit(ctx context.Context, request *connect.Request[grpcviewv1.SetDescriptorSourceCommitRequest]) (*connect.Response[grpcviewv1.SetDescriptorSourceCommitResponse], error) {
	coll, ws, err := w.openWithSources(ctx, request.Msg.GetCollection())
	if err != nil {
		return nil, err
	}
	id := request.Msg.GetId()
	sources := slices.Clone(ws.GetSources())
	index := slices.IndexFunc(sources, func(s *grpcviewv1.DescriptorSource) bool { return s.GetId() == id })
	if index == -1 {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("unknown source id %q", id))
	}

	if request.Msg.GetCommit() {
		resolves, err := coll.DescriptorResolves(ctx)
		if err != nil {
			return nil, toConnectError(err)
		}
		if _, ok := resolves[id]; !ok {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
				"source %q has never resolved, so there are no descriptors to commit: refresh it first", id))
		}
	}

	sources[index] = proto.CloneOf(sources[index])
	sources[index].CommitDescriptors = request.Msg.GetCommit()
	if err := w.putDescriptorState(ctx, coll, sources, nil); err != nil {
		return nil, err
	}
	reloaded, err := w.loadCollection(ctx, coll)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&grpcviewv1.SetDescriptorSourceCommitResponse{Collection: reloaded}), nil
}

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

	if err := w.putDescriptorState(ctx, coll, slices.Delete(sources, index, index+1), nil); err != nil {
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
	return connect.NewResponse(&grpcviewv1.CreateRequestResponse{
		Collection: ws,
		Warnings:   w.createRequestWarnings(ctx, request.Msg),
	}), nil
}

// Best-effort: an unresolvable method is not this RPC's business to reject — the request is
// already created, and a collection whose sources are cold must still be authorable.
func (w Workspace) createRequestWarnings(ctx context.Context, msg *grpcviewv1.CreateRequestRequest) []string {
	if msg.GetService() == "" || msg.GetMethod() == "" {
		return nil
	}
	methodDesc, _, err := w.describeMethod(ctx, msg.GetCollection(), msg.GetService(), msg.GetMethod())
	if err != nil {
		return nil
	}
	if reason := notInvocableReason(methodDesc); reason != "" {
		return []string{fmt.Sprintf("%s/%s is %s", msg.GetService(), msg.GetMethod(), reason)}
	}
	return nil
}

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

func (w Workspace) MoveItem(ctx context.Context, request *connect.Request[grpcviewv1.MoveItemRequest]) (*connect.Response[grpcviewv1.MoveItemResponse], error) {
	ws, err := w.mutate(ctx, request.Msg.GetCollection(), func(coll *store.Collection) error {
		// request.Msg.Before, not GetBefore(): that would collapse "unset" (append at the end) into ""
		// (insert before an item literally named "").
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
