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
	// Trust rides along on the listing because this is the call a client makes first, before it
	// knows anything else about the workspace — and it is read only for the banner: nothing in the
	// listing, or in any read, is gated on it.
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

// SetWorkspaceTrust trusts or un-trusts the workspace ROOT, which is what gates resolving a source
// kind that EXECUTES (a bazel label builds). It is stored in user state, never in the repo: a
// `trusted: true` a repo could commit about itself would say nothing.
//
// Revoking un-resolves nothing. The descriptors every source already produced stay exactly where
// they are and every collection keeps loading, describing and invoking from them; only the next
// build is refused. Dropping them instead would make revoke a destructive act nobody would risk.
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
	// The resulting state is re-read rather than echoed back, so the response is what the next
	// build will actually see.
	trusted, err := wsroot.IsTrusted(root)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal,
			fmt.Errorf("failed to read whether this workspace is trusted: %w", err))
	}
	return connect.NewResponse(&grpcviewv1.SetWorkspaceTrustResponse{Trusted: trusted}), nil
}

// ListBazelTargets lists the labels a bazel source could be added from, so the add form can offer
// them instead of asking the user to recall one. It is a READ that executes: `bazel query` loads
// BUILD files and can fetch external repos, so it goes through bazelBuilder like every build does
// and inherits its trust gate — an untrusted workspace gets FailedPrecondition, and the client
// keeps its free-text field.
//
// A query that came back partial is a listing plus a warning, never an error: one unloadable
// package in a monorepo must not blank a picker whose whole job is convenience.
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

	// The add call is the only place an upload's bytes ever arrive, so they are resolved here and
	// handed to the store as this source's fresh resolve — the same path a dialed reflection
	// target takes. commit_descriptors then decides only where the store puts them.
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
		// The path is only the RECIPE for next time — the bytes the caller sent are this resolve,
		// and re-reading the file here could disagree with them. So a path that does not confine
		// to the workspace costs the RECIPE and nothing else: the upload still lands, it is simply
		// not refreshable. Failing the whole add instead would break the ordinary workflow — a
		// `buf build` image in ~/Downloads, or a bazel-bin/ path that is a symlink out of the repo
		// — over bytes that are already here and already valid. The confinement that matters is on
		// the READ side (resolveOne), which is the only place a recorded path is ever traversed.
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
				// Root-relative, so the recipe survives a colleague's checkout at another path.
				// Identity is still the file name alone, which is what makes re-adding the same
				// upload from a moved file edit this recipe instead of spawning a second source.
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
		// Canonicalize BEFORE the id is derived: "//pkg" and "//pkg:pkg" are one target, so the
		// stored label — and therefore the id — has to be the canonical spelling, or re-adding the
		// other spelling would duplicate the source instead of refreshing it.
		label, err := bazelbuild.CanonicalLabel(source.Bazel.GetLabel())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		src = &grpcviewv1.DescriptorSource{
			Source:            &grpcviewv1.DescriptorSource_Bazel{Bazel: &grpcviewv1.Bazel{Label: label}},
			CommitDescriptors: request.Msg.GetCommitDescriptors(),
		}
		src.Id = sourceID(src)
		// An add that cannot build FAILS, exactly as an undialable reflection target does: the user
		// is asking for this source right now, so a row that silently resolves to nothing would be
		// a worse answer than the build's error.
		if fresh, err = w.resolveBazel(ctx, src.GetId(), label); err != nil {
			return nil, err
		}
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unknown source type: <%T> %+v", source, source))
	}

	// commit_descriptors is STICKY across a re-add: an add whose id already exists is the documented
	// refresh gesture (and the browser's only way to refresh an upload or a bazel label), so
	// re-adding with the box unticked must not silently un-commit a committed source and delete the
	// sidecar the repo carries. A re-add can therefore turn committing ON but never off;
	// SetDescriptorSourceCommit (`grpcview sources commit --off`) is the one and only way off.
	if i := slices.IndexFunc(ws.GetSources(), func(s *grpcviewv1.DescriptorSource) bool {
		return s.GetId() == src.GetId()
	}); i != -1 && ws.GetSources()[i].GetCommitDescriptors() {
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
	if err := w.putDescriptorState(ctx, coll, sources, nil); err != nil {
		return nil, err
	}
	reloaded, err := w.loadCollection(ctx, coll)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&grpcviewv1.ReorderDescriptorSourcesResponse{Collection: reloaded}), nil
}

// SetDescriptorSourceCommit turns one source's commit_descriptors flag on or off, which moves what
// it last resolved to between a committed protojson sidecar and the local blob store and changes
// nothing else.
//
// It exists rather than "re-add with the flag set" because toggling must never dial or build: on
// writes the sidecar from the bytes the store already holds, off drops the sidecar and keeps the
// blob. That is why it is implemented as a flag flip plus putDescriptorState with NO fresh
// resolves — by construction there is nothing in that path that can acquire.
//
// Turning it on for a source that has never resolved is refused instead of resolved: acquisition
// triggered by a config change is exactly what the two-systems split forbids, and the message
// names the refresh that fixes it.
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
