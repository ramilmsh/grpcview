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

	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/grpcreflect"
	"google.golang.org/protobuf/proto"
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

func (w Workspace) AddDescriptorSource(ctx context.Context, request *connect.Request[grpcviewv1.AddDescriptorSourceRequest]) (*connect.Response[grpcviewv1.AddDescriptorSourceResponse], error) {
	coll, err := w.store.Open(ctx, request.Msg.GetWorkspaceName())
	if err != nil {
		return nil, err
	}
	ws, err := coll.Load(ctx)
	if err != nil {
		return nil, toConnectError(err)
	}

	// Resolve just the newly added source and merge its services into the flat
	// list; existing sources are not re-resolved on add (remove re-resolves the
	// whole list — see RemoveDescriptorSource). Both branches share the merge +
	// persist + reload tail in addResolvedSource.
	switch source := request.Msg.GetSource().(type) {
	case *grpcviewv1.AddDescriptorSourceRequest_DescriptorSet:
		resolved, fileDescs, err := resolveDescriptorSetServices(source.DescriptorSet)
		if err != nil {
			return nil, err
		}
		src := &grpcviewv1.DescriptorSource{
			Source: &grpcviewv1.DescriptorSource_DescriptorSet{DescriptorSet: source.DescriptorSet},
		}
		return w.addResolvedSource(ctx, coll, ws, src, resolved, fileDescs)
	case *grpcviewv1.AddDescriptorSourceRequest_Reflection:
		resolved, fileDescs, err := resolveReflectionServices(ctx, source.Reflection)
		if err != nil {
			return nil, err
		}
		src := &grpcviewv1.DescriptorSource{
			Source: &grpcviewv1.DescriptorSource_Reflection{Reflection: source.Reflection},
		}
		return w.addResolvedSource(ctx, coll, ws, src, resolved, fileDescs)
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unknown source type: <%T> %+v", source, source))
	}
}

// addResolvedSource records src and merges its freshly resolved services into
// the workspace (later services win on a package/name collision, matching
// resolveServicesFromSources), persists the source config (committed) alongside
// the resolved schema cache (gitignored), then reloads so the response reflects
// on-disk state. Shared by both AddDescriptorSource branches.
func (w Workspace) addResolvedSource(ctx context.Context, coll *store.Collection, ws *grpcviewv1.Workspace, src *grpcviewv1.DescriptorSource, resolved []*grpcviewv1.Service, fileDescs []*desc.FileDescriptor) (*connect.Response[grpcviewv1.AddDescriptorSourceResponse], error) {
	for _, svc := range resolved {
		ws.Services = mergeService(ws.Services, svc)
	}
	ws.Sources = appendSourceUnique(ws.Sources, src)
	// Fold the new source's descriptors into the cached descriptor_set (loaded on
	// ws), mirroring the incremental services merge above.
	descriptorSet, err := mergeDescriptorSet(ws.GetDescriptorSet(), fileDescs)
	if err != nil {
		return nil, fmt.Errorf("failed to merge descriptor set: %w", err)
	}
	if err := coll.PutDescriptorState(ctx, ws.Sources, ws.Services, descriptorSet); err != nil {
		return nil, fmt.Errorf("failed to persist descriptor state: %w", err)
	}

	reloaded, err := coll.Load(ctx)
	if err != nil {
		return nil, toConnectError(err)
	}
	return connect.NewResponse(&grpcviewv1.AddDescriptorSourceResponse{Workspace: reloaded}), nil
}

// RemoveDescriptorSource drops the source at the given index (its position in
// Workspace.sources, matching the displayed order) and re-resolves the flat
// services list from the sources that remain. The merged list can't be
// un-merged per source until Phase 2 gives sources real identity, so the
// remaining reflection sources are re-reflected here — one network round-trip
// each, performed server-side and surfaced as a clean Connect error on failure.
func (w Workspace) RemoveDescriptorSource(ctx context.Context, request *connect.Request[grpcviewv1.RemoveDescriptorSourceRequest]) (*connect.Response[grpcviewv1.RemoveDescriptorSourceResponse], error) {
	coll, err := w.store.Open(ctx, request.Msg.GetWorkspaceName())
	if err != nil {
		return nil, err
	}
	ws, err := coll.Load(ctx)
	if err != nil {
		return nil, toConnectError(err)
	}

	index := int(request.Msg.GetIndex())
	if index < 0 || index >= len(ws.Sources) {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("source index %d out of range [0,%d)", index, len(ws.Sources)))
	}

	remaining := slices.Delete(slices.Clone(ws.Sources), index, index+1)
	services, fileDescs, err := resolveServicesFromSources(ctx, remaining)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable,
			fmt.Errorf("failed to re-resolve services after removing source: %w", err))
	}
	// The remaining sources were fully re-resolved, so the descriptor_set is rebuilt
	// from scratch (no cache merge) — the removed source's files drop out.
	descriptorSet, err := mergeDescriptorSet(nil, fileDescs)
	if err != nil {
		return nil, fmt.Errorf("failed to merge descriptor set: %w", err)
	}

	if err := coll.PutDescriptorState(ctx, remaining, services, descriptorSet); err != nil {
		return nil, fmt.Errorf("failed to persist descriptor state: %w", err)
	}

	reloaded, err := coll.Load(ctx)
	if err != nil {
		return nil, toConnectError(err)
	}
	return connect.NewResponse(&grpcviewv1.RemoveDescriptorSourceResponse{Workspace: reloaded}), nil
}

// resolveServicesFromSources rebuilds the merged services list from a set of
// descriptor sources, reflecting each reflection source (one network round-trip
// each) and parsing each uploaded descriptor set. Later-listed sources win on
// package/name collisions, matching the incremental merge AddDescriptorSource
// performs.
func resolveServicesFromSources(ctx context.Context, sources []*grpcviewv1.DescriptorSource) ([]*grpcviewv1.Service, []*desc.FileDescriptor, error) {
	var services []*grpcviewv1.Service
	var fileDescs []*desc.FileDescriptor
	for _, src := range sources {
		var resolved []*grpcviewv1.Service
		var files []*desc.FileDescriptor
		var err error
		switch s := src.GetSource().(type) {
		case *grpcviewv1.DescriptorSource_Reflection:
			resolved, files, err = resolveReflectionServices(ctx, s.Reflection)
		case *grpcviewv1.DescriptorSource_DescriptorSet:
			resolved, files, err = resolveDescriptorSetServices(s.DescriptorSet)
		}
		if err != nil {
			return nil, nil, err
		}
		for _, svc := range resolved {
			services = mergeService(services, svc)
		}
		fileDescs = append(fileDescs, files...)
	}
	return services, fileDescs, nil
}

// resolveReflectionServices dials a reflection server, lists its services, and
// converts each into a wire Service (package/name + methods with input schemas).
// It also returns the resolved *desc.FileDescriptor for each service (carrying its
// transitive dependency graph incl. WKTs), which the caller merges into the
// workspace's cached descriptor_set (§T2). One network round-trip per call.
func resolveReflectionServices(ctx context.Context, server *grpcviewv1.Server) ([]*grpcviewv1.Service, []*desc.FileDescriptor, error) {
	conn, err := dial(server)
	if err != nil {
		return nil, nil, fmt.Errorf("couldn't connect to %s: %w", server, err)
	}

	client := grpcreflect.NewClientAuto(ctx, conn)
	names, err := client.ListServices()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list services: %w", err)
	}

	services := make([]*grpcviewv1.Service, 0, len(names))
	fileDescs := make([]*desc.FileDescriptor, 0, len(names))
	for _, serviceName := range names {
		fileDesc, err := client.FileContainingSymbol(serviceName)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get file for service [%s]: %w", serviceName, err)
		}
		serviceDesc := fileDesc.FindSymbol(serviceName).(*desc.ServiceDescriptor)
		// Attribute every service from this source to the server being reflected, so a
		// request against it defaults its invoke target to the source that provides it.
		service, err := convertService(serviceDesc, server)
		if err != nil {
			return nil, nil, err
		}
		services = append(services, service)
		fileDescs = append(fileDescs, fileDesc)
	}
	return services, fileDescs, nil
}

// resolveDescriptorSetServices parses an uploaded FileDescriptorSet (raw
// protobuf wire bytes) and converts every service it defines into a wire Service
// via the shared convertService path. The set must be self-contained — carrying
// the transitive dependencies of its files, as `protoc --include_imports` and
// the UI upload produce — or linking fails. Services are walked in the set's
// file order and merged so a later file wins on a package/name collision,
// mirroring resolveServicesFromSources' cross-source rule. Parse and link
// failures surface as InvalidArgument because the bytes are caller-supplied.
func resolveDescriptorSetServices(raw []byte) ([]*grpcviewv1.Service, []*desc.FileDescriptor, error) {
	fds := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(raw, fds); err != nil {
		return nil, nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("parse descriptor set: %w", err))
	}
	files, err := desc.CreateFileDescriptorsFromSet(fds)
	if err != nil {
		return nil, nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("link descriptor set: %w", err))
	}

	var services []*grpcviewv1.Service
	// Every linked file (incl. transitive deps) feeds the merged descriptor_set;
	// desc.ToFileDescriptorSet dedupes and topo-sorts, so file order is irrelevant.
	fileDescs := make([]*desc.FileDescriptor, 0, len(files))
	for _, fdp := range fds.GetFile() {
		file := files[fdp.GetName()]
		if file == nil {
			continue
		}
		fileDescs = append(fileDescs, file)
		for _, serviceDesc := range file.GetServices() {
			// A descriptor-set upload has no dial target, so its services carry no
			// source; a request against one falls back to the first reflection source.
			service, err := convertService(serviceDesc, nil)
			if err != nil {
				return nil, nil, err
			}
			services = mergeService(services, service)
		}
	}
	return services, fileDescs, nil
}

// mergeDescriptorSet combines freshly resolved file descriptors with the
// previously cached descriptor_set (existing, may be nil) and marshals one
// merged, deduped FileDescriptorSet with transitive deps incl. WKTs.
// desc.ToFileDescriptorSet walks GetDependencies transitively, dedupes by file
// name, and topo-sorts; fileDescs come first so a re-added/updated source wins.
// This mirrors the incremental services merge: Add folds new descriptors into the
// cache, while RemoveDescriptorSource re-resolves the full set and passes nil.
func mergeDescriptorSet(existing []byte, fileDescs []*desc.FileDescriptor) ([]byte, error) {
	all := fileDescs
	if len(existing) > 0 {
		prev := &descriptorpb.FileDescriptorSet{}
		if err := proto.Unmarshal(existing, prev); err != nil {
			return nil, fmt.Errorf("parse cached descriptor set: %w", err)
		}
		prevFiles, err := desc.CreateFileDescriptorsFromSet(prev)
		if err != nil {
			return nil, fmt.Errorf("link cached descriptor set: %w", err)
		}
		for _, fd := range prevFiles {
			all = append(all, fd)
		}
	}
	if len(all) == 0 {
		return nil, nil
	}
	return proto.Marshal(desc.ToFileDescriptorSet(all...))
}

// convertService builds a wire Service (package/name + methods with input JSON
// schemas) from a resolved service descriptor. This is the schema-conversion
// step shared across descriptor source types — reflection and descriptor-set
// upload both funnel through here. source is the reflection server the service
// was resolved from (nil for a descriptor-set upload, which has no dial target);
// it becomes Service.source, the request's default invoke target (resolveTarget).
func convertService(serviceDesc *desc.ServiceDescriptor, source *grpcviewv1.Server) (*grpcviewv1.Service, error) {
	service := &grpcviewv1.Service{
		Package: serviceDesc.GetFile().AsFileDescriptorProto().GetPackage(),
		Name:    serviceDesc.GetName(),
		Methods: make([]*grpcviewv1.Method, len(serviceDesc.GetMethods())),
		Source:  source,
	}

	for j, methodDesc := range serviceDesc.GetMethods() {
		inputDesc := methodDesc.GetInputType()
		outputDesc := methodDesc.GetOutputType()

		// client_streaming/server_streaming carry the method's real kind so the
		// tree/tabs render the right tag and InvokeStreaming maps onto the right
		// call shape. Both reflection and descriptor-set sources funnel through
		// here, so populating them once covers both branches. The editor's typing
		// comes from the client-side protoc-gen-es over Workspace.descriptor_set
		// (§T2), keyed by each message's package/name/file — no per-message JSON
		// schema is sent.
		service.Methods[j] = &grpcviewv1.Method{
			Name: methodDesc.GetName(),
			Input: &grpcviewv1.Message{
				Package: inputDesc.GetFile().AsFileDescriptorProto().GetPackage(),
				Name:    inputDesc.GetName(),
				// File is the proto path defining this message — the protoc-gen-es
				// fileToGenerate selector into Workspace.descriptor_set (§T2). It
				// rides the wire-typed services cache for free.
				File: inputDesc.GetFile().GetName(),
			},
			Output: &grpcviewv1.Message{
				Package: outputDesc.GetFile().AsFileDescriptorProto().GetPackage(),
				Name:    outputDesc.GetName(),
				File:    outputDesc.GetFile().GetName(),
			},
			ClientStreaming: methodDesc.IsClientStreaming(),
			ServerStreaming: methodDesc.IsServerStreaming(),
		}
	}
	return service, nil
}

// mergeService replaces the entry sharing svc's package/name identity or appends
// svc when none exists, returning the updated slice.
func mergeService(services []*grpcviewv1.Service, svc *grpcviewv1.Service) []*grpcviewv1.Service {
	index := slices.IndexFunc(services, func(s *grpcviewv1.Service) bool {
		return s.GetPackage() == svc.GetPackage() && s.GetName() == svc.GetName()
	})
	if index == -1 {
		return append(services, svc)
	}
	services[index] = svc
	return services
}

func appendSourceUnique(sources []*grpcviewv1.DescriptorSource, src *grpcviewv1.DescriptorSource) []*grpcviewv1.DescriptorSource {
	if slices.ContainsFunc(sources, func(s *grpcviewv1.DescriptorSource) bool { return proto.Equal(s, src) }) {
		return sources
	}
	return append(sources, src)
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
