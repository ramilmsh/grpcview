package workspace

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/grpcreflect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	grpcviewstorev1 "codeberg.org/ramilmsh/grpcview/grpcview/store/v1"
	grpcviewv1 "codeberg.org/ramilmsh/grpcview/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/bazelbuild"
	"codeberg.org/ramilmsh/grpcview/service/store"
	"codeberg.org/ramilmsh/grpcview/service/wsroot"
)

type resolvedSource struct {
	id       string
	server   *grpcviewv1.Server
	files    *descriptorpb.FileDescriptorSet
	services []string
	err      error
}

func sourceID(src *grpcviewv1.DescriptorSource) string { return store.SourceID(src) }

func resolveReflection(ctx context.Context, id string, server *grpcviewv1.Server) (*resolvedSource, error) {
	conn, err := dial(server)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable,
			fmt.Errorf("couldn't connect to %s: %w", server.GetAddress(), err))
	}
	defer func() { _ = conn.Close() }()

	client := grpcreflect.NewClientAuto(ctx, conn)
	names, err := client.ListServices()
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("failed to list services: %w", err))
	}

	fileDescs := make([]*desc.FileDescriptor, 0, len(names))
	for _, name := range names {
		fileDesc, err := client.FileContainingSymbol(name)
		if err != nil {
			return nil, connect.NewError(connect.CodeUnavailable,
				fmt.Errorf("failed to get file for service [%s]: %w", name, err))
		}
		fileDescs = append(fileDescs, fileDesc)
	}
	return &resolvedSource{
		id:       id,
		server:   server,
		files:    desc.ToFileDescriptorSet(fileDescs...),
		services: names,
	}, nil
}

func resolveUpload(id string, fds *descriptorpb.FileDescriptorSet) (*resolvedSource, error) {
	files, err := desc.CreateFileDescriptorsFromSet(fds)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("link descriptor set: %w", err))
	}
	var names []string
	for _, fdp := range fds.GetFile() {
		file := files[fdp.GetName()]
		if file == nil {
			continue
		}
		for _, sd := range file.GetServices() {
			names = append(names, sd.GetFullyQualifiedName())
		}
	}
	return &resolvedSource{id: id, files: fds, services: names}, nil
}

func parseUpload(raw []byte) (*descriptorpb.FileDescriptorSet, error) {
	fds := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(raw, fds); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("parse descriptor set: %w", err))
	}
	return fds, nil
}

// The ONLY place a bazelbuild.Builder is constructed, which is what makes the trust check unskippable:
// a build can only be started through a Builder, so no future caller can add a second door that
// forgets to ask. Putting the gate in bazelbuild itself is worse — that package is the mechanism and
// knows nothing about workspaces, manifests or roots.
func (w Workspace) bazelBuilder() (bazelbuild.Builder, error) {
	root := w.store.Root()

	trusted, err := wsroot.IsTrusted(root)
	if err != nil {
		return bazelbuild.Builder{}, connect.NewError(connect.CodeInternal,
			fmt.Errorf("failed to read whether this workspace is trusted: %w", err))
	}
	if !trusted {
		return bazelbuild.Builder{}, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"the workspace %s is not trusted, and resolving a bazel source runs `bazel build` — arbitrary code from this repo's BUILD files: trust the workspace first if it is yours", root))
	}

	cfg, err := w.store.WorkspaceBazel()
	if err != nil {
		return bazelbuild.Builder{}, toConnectError(err)
	}

	bazelRoot := cfg.GetRoot()
	switch {
	case bazelRoot == "":
		bazelRoot = bazelbuild.FindRoot(root)
		if bazelRoot == "" {
			return bazelbuild.Builder{}, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"no bazel workspace found at or above %s (no MODULE.bazel, WORKSPACE or WORKSPACE.bazel): set bazel.root in %s",
				root, store.WorkspaceFileName))
		}
	case !filepath.IsAbs(bazelRoot):
		bazelRoot = filepath.Join(root, bazelRoot)
	}
	bazelRoot = filepath.Clean(bazelRoot)
	// bazel.root is authored config naming a build cwd, not a file whose bytes are read, so it is NOT
	// confined to the workspace root: a grpcview workspace opened at a subdirectory of a monorepo has its
	// bazel root ABOVE it, which is the only reason the field exists. It is still bounded, because the
	// trust decision is about ONE root — without this, trusting this workspace would authorize a build
	// whose cwd is inside a DIFFERENT, untrusted repo, whose BUILD files then execute.
	if err := relatedToRoot(root, bazelRoot); err != nil {
		return bazelbuild.Builder{}, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"bazel.root in %s resolves to %s, which is neither inside the trusted workspace %s nor an ancestor of it: trust covers one workspace root, and building with a cwd in an unrelated repo would run that repo's BUILD files instead",
			store.WorkspaceFileName, bazelRoot, root))
	}

	return bazelbuild.Builder{
		Root:    bazelRoot,
		Timeout: time.Duration(cfg.GetTimeoutSeconds()) * time.Second,
	}, nil
}

func (w Workspace) resolveBazel(ctx context.Context, id, label string) (*resolvedSource, error) {
	canon, err := bazelbuild.CanonicalLabel(label)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	builder, err := w.bazelBuilder()
	if err != nil {
		return nil, err
	}
	sets, err := builder.DescriptorSets(ctx, canon)
	if err != nil {
		return nil, bazelResolveError(ctx, err)
	}
	return resolveUpload(id, dedupeFiles(sets))
}

// First spelling of each proto file name wins. Not an optimization: a merging rule emits its inputs'
// per-target sets, so one file name legitimately appears in more than one of them, and
// desc.CreateFileDescriptorsFromSet REJECTS a duplicate. Deduping by name alone is safe because every
// copy came out of one build of one target.
func dedupeFiles(sets []*descriptorpb.FileDescriptorSet) *descriptorpb.FileDescriptorSet {
	out := &descriptorpb.FileDescriptorSet{}
	seen := make(map[string]bool)
	for _, set := range sets {
		for _, f := range set.GetFile() {
			if seen[f.GetName()] {
				continue
			}
			seen[f.GetName()] = true
			out.File = append(out.File, f)
		}
	}
	return out
}

// The ONE seam where bazelbuild's plain errors become connect codes — that package returns plain errors
// so it stays usable outside an RPC. Messages pass through untouched: each already names its own fix.
func bazelResolveError(ctx context.Context, err error) error {
	switch {
	case ctx.Err() != nil:
		return connect.NewError(connect.CodeCanceled, err)
	case errors.Is(err, context.DeadlineExceeded):
		return connect.NewError(connect.CodeDeadlineExceeded, err)
	default:
		return connect.NewError(connect.CodeFailedPrecondition, err)
	}
}

// Re-acquires one source from its POINTER; an upload with no path is the only kind with nothing to
// re-acquire from. Every path into it is a source-MUTATING RPC: nothing on the READ path stats a recipe
// or rebuilds a label, so an untrusted or unbuildable bazel source still LOADS, with the reason in its
// Resolved.error and no acquisition triggered by opening a collection.
func (w Workspace) resolveOne(ctx context.Context, src *grpcviewv1.DescriptorSource) (*resolvedSource, error) {
	id := src.GetId()
	switch s := src.GetSource().(type) {
	case *grpcviewv1.DescriptorSource_Reflection:
		return resolveReflection(ctx, id, s.Reflection)
	case *grpcviewv1.DescriptorSource_Bazel:
		return w.resolveBazel(ctx, id, s.Bazel.GetLabel())
	case *grpcviewv1.DescriptorSource_Upload:
		path := s.Upload.GetPath()
		if path == "" {
			return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"source %q was uploaded with no path — a browser has a file picker and never learns one — so there is nothing to re-read: refresh it by handing the file over again", id))
		}
		real, _, err := resolveWorkspaceFile(w.store.Root(), path)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("cannot refresh source %q: %w", id, err))
		}
		raw, err := os.ReadFile(real)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("cannot refresh source %q: %w", id, err))
		}
		fds, err := parseUpload(raw)
		if err != nil {
			return nil, err
		}
		return resolveUpload(id, fds)
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, errMissingDefinition(id))
	}
}

func errMissingDefinition(id string) error {
	return fmt.Errorf(
		"%s does not define the source %q this collection references: define it there, or remove the reference from this collection",
		store.WorkspaceFileName, id)
}

func unresolvedReason(src *grpcviewv1.DescriptorSource) error {
	if src.GetSource() == nil {
		return errMissingDefinition(src.GetId())
	}
	return errUnresolvedSource
}

func (w Workspace) resolveConfigured(
	ctx context.Context,
	coll *store.Collection,
	sources []*grpcviewv1.DescriptorSource,
	fresh map[string]*resolvedSource,
) ([]*resolvedSource, error) {
	cached, err := coll.DescriptorResolves(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]*resolvedSource, 0, len(sources))
	for _, src := range sources {
		id := src.GetId()
		if rs, ok := fresh[id]; ok {
			out = append(out, rs)
			continue
		}
		if c, ok := cached[id]; ok {
			out = append(out, &resolvedSource{
				id:       id,
				server:   src.GetReflection(),
				files:    c.GetDescriptorSet(),
				services: c.GetServiceNames(),
			})
			continue
		}
		rs, err := w.resolveOne(ctx, src)
		if err != nil {
			slog.Default().Warn("descriptor source unresolved", "source", id, "error", err)
			out = append(out, &resolvedSource{id: id, server: src.GetReflection(), err: err})
			continue
		}
		out = append(out, rs)
	}
	return out, nil
}

type mergedView struct {
	services      []*grpcviewv1.Service
	descriptorSet []byte
	summaries     map[string]*grpcviewv1.Resolved
	serviceDescs  map[string]*desc.ServiceDescriptor
}

func summarize(resolved []*resolvedSource) map[string]*grpcviewv1.Resolved {
	summaries := make(map[string]*grpcviewv1.Resolved, len(resolved))
	for _, rs := range resolved {
		summary := &grpcviewv1.Resolved{
			FileCount:    int32(len(rs.files.GetFile())),
			ServiceNames: rs.services,
		}
		if rs.err != nil {
			summary.Error = rs.err.Error()
		}
		summaries[rs.id] = summary
	}
	return summaries
}

func mergeSources(resolved []*resolvedSource) (*mergedView, error) {
	summaries := summarize(resolved)

	claimedBy := make(map[string]string)
	var claimed []*descriptorpb.FileDescriptorProto
	for _, rs := range resolved {
		for _, f := range rs.files.GetFile() {
			if _, taken := claimedBy[f.GetName()]; taken {
				continue
			}
			claimedBy[f.GetName()] = rs.id
			claimed = append(claimed, f)
		}
	}
	if len(claimed) == 0 {
		return &mergedView{summaries: summaries}, nil
	}

	linked, err := desc.CreateFileDescriptorsFromSet(&descriptorpb.FileDescriptorSet{File: claimed})
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"descriptor sources %s cannot be merged — they disagree about the protos they share: %w",
			strings.Join(sourceIDs(resolved), ", "), err))
	}

	serviceDescs := make(map[string]*desc.ServiceDescriptor)
	for _, fd := range linked {
		for _, sd := range fd.GetServices() {
			serviceDescs[sd.GetFullyQualifiedName()] = sd
		}
	}

	dialFor := make(map[string]*grpcviewv1.Server)
	for _, rs := range resolved {
		if rs.server == nil {
			continue
		}
		for _, name := range rs.services {
			if _, ok := dialFor[name]; !ok {
				dialFor[name] = rs.server
			}
		}
	}

	var services []*grpcviewv1.Service
	seen := make(map[string]bool, len(serviceDescs))
	for _, rs := range resolved {
		for _, name := range rs.services {
			if seen[name] {
				continue
			}
			sd := serviceDescs[name]
			if sd == nil {
				continue
			}
			seen[name] = true
			services = append(services, convertService(sd, dialFor[name]))
			winner := claimedBy[sd.GetFile().GetName()]
			summaries[winner].WonServiceNames = append(summaries[winner].WonServiceNames, name)
		}
	}

	all := make([]*desc.FileDescriptor, 0, len(claimed))
	for _, f := range claimed {
		if fd := linked[f.GetName()]; fd != nil {
			all = append(all, fd)
		}
	}
	descriptorSet, err := proto.Marshal(desc.ToFileDescriptorSet(all...))
	if err != nil {
		return nil, fmt.Errorf("marshal merged descriptor set: %w", err)
	}
	return &mergedView{
		services:      services,
		descriptorSet: descriptorSet,
		summaries:     summaries,
		serviceDescs:  serviceDescs,
	}, nil
}

func sourceIDs(resolved []*resolvedSource) []string {
	ids := make([]string, 0, len(resolved))
	for _, rs := range resolved {
		ids = append(ids, rs.id)
	}
	return ids
}

func (w Workspace) putDescriptorState(
	ctx context.Context,
	coll *store.Collection,
	sources []*grpcviewv1.DescriptorSource,
	fresh map[string]*resolvedSource,
) error {
	resolved, err := w.resolveConfigured(ctx, coll, sources, fresh)
	if err != nil {
		return err
	}
	view, err := mergeSources(resolved)
	if err != nil {
		return err
	}

	resolves := make(map[string]*grpcviewstorev1.ResolvedSource, len(resolved))
	for _, rs := range resolved {
		if rs.err != nil {
			continue
		}
		resolves[rs.id] = &grpcviewstorev1.ResolvedSource{
			Id:            rs.id,
			DescriptorSet: rs.files,
			ServiceNames:  rs.services,
		}
	}
	stored := make([]*grpcviewv1.DescriptorSource, 0, len(sources))
	for _, src := range sources {
		clone := proto.CloneOf(src)
		clone.Resolved = view.summaries[src.GetId()]
		stored = append(stored, clone)
	}

	if err := coll.PutDescriptorState(ctx, store.DescriptorState{
		Sources:  stored,
		Resolves: resolves,
	}); err != nil {
		return err
	}
	w.defs.invalidate(coll.Key())
	return nil
}

func upsertSource(sources []*grpcviewv1.DescriptorSource, src *grpcviewv1.DescriptorSource) []*grpcviewv1.DescriptorSource {
	i := slices.IndexFunc(sources, func(s *grpcviewv1.DescriptorSource) bool { return s.GetId() == src.GetId() })
	if i == -1 {
		return append(sources, src)
	}
	sources[i] = src
	return sources
}

func reorderSources(sources []*grpcviewv1.DescriptorSource, ids []string) ([]*grpcviewv1.DescriptorSource, error) {
	if len(ids) != len(sources) {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
			"reorder needs all %d source ids, got %d", len(sources), len(ids)))
	}
	byID := make(map[string]*grpcviewv1.DescriptorSource, len(sources))
	for _, s := range sources {
		byID[s.GetId()] = s
	}
	out := make([]*grpcviewv1.DescriptorSource, 0, len(ids))
	for _, id := range ids {
		s, ok := byID[id]
		if !ok {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("unknown source id %q", id))
		}
		delete(byID, id)
		out = append(out, s)
	}
	return out, nil
}

func convertService(serviceDesc *desc.ServiceDescriptor, dial *grpcviewv1.Server) *grpcviewv1.Service {
	service := &grpcviewv1.Service{
		Package: serviceDesc.GetFile().AsFileDescriptorProto().GetPackage(),
		Name:    serviceDesc.GetName(),
		Methods: make([]*grpcviewv1.Method, len(serviceDesc.GetMethods())),
		Source:  dial,
	}

	for j, methodDesc := range serviceDesc.GetMethods() {
		inputDesc := methodDesc.GetInputType()
		outputDesc := methodDesc.GetOutputType()

		service.Methods[j] = &grpcviewv1.Method{
			Name: methodDesc.GetName(),
			Input: &grpcviewv1.Message{
				Package: inputDesc.GetFile().AsFileDescriptorProto().GetPackage(),
				Name:    inputDesc.GetName(),
				File:    inputDesc.GetFile().GetName(),
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
	return service
}
