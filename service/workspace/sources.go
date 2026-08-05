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

	grpcviewstorev1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/store/v1"
	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/bazelbuild"
	"codeberg.org/ramilmsh/grpcview/service/store"
	"codeberg.org/ramilmsh/grpcview/service/wsroot"
)

// Descriptor sources: each resolves independently to its own descriptors (cached per source),
// and the workspace's services + descriptor_set are re-derived by merging those resolves in the
// source list's priority order.

type resolvedSource struct {
	id       string
	server   *grpcviewv1.Server // dial target; nil for an upload, which has no address
	files    *descriptorpb.FileDescriptorSet
	services []string // names this source SERVES — narrower than what files defines
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

// resolveUpload resolves from bytes that ARE the answer: an upload's payload, or what a bazel build
// wrote (see resolveBazel). Neither has a ListServices to narrow it, so every service the set
// defines is served — the one place a reflection source is different.
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

// parseUpload is a plain parse: normalizing a `buf build` image's extension fields away belongs
// at the store's write boundary (normalizeDescriptorSet), where it applies to every kind rather
// than only to the bytes that happen to arrive through this door.
func parseUpload(raw []byte) (*descriptorpb.FileDescriptorSet, error) {
	fds := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(raw, fds); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("parse descriptor set: %w", err))
	}
	return fds, nil
}

// bazelBuilder is the ONLY place a bazelbuild.Builder is constructed, which is what makes the
// trust check unskippable: a build can only be started through a Builder, so every future caller
// that wants one comes through this door and cannot add a second one that forgets to ask. Putting
// the gate in bazelbuild itself was the alternative and is worse — that package is the mechanism and
// knows nothing about workspaces, manifests or roots.
func (w Workspace) bazelBuilder() (bazelbuild.Builder, error) {
	root := w.store.Root()

	trusted, err := wsroot.IsTrusted(root)
	if err != nil {
		return bazelbuild.Builder{}, connect.NewError(connect.CodeInternal,
			fmt.Errorf("failed to read whether this workspace is trusted: %w", err))
	}
	if !trusted {
		// FailedPrecondition, not PermissionDenied: nothing is refusing the user, the workspace is
		// simply in a state where building is not allowed yet, and the fix is one call away.
		return bazelbuild.Builder{}, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"the workspace %s is not trusted, and resolving a bazel source runs `bazel build` — arbitrary code from this repo's BUILD files: trust the workspace first if it is yours", root))
	}

	cfg, err := w.store.WorkspaceBazel()
	if err != nil {
		return bazelbuild.Builder{}, toConnectError(err)
	}

	// bazel.root is authored config naming a build cwd, not a file whose bytes are read back, so it
	// is NOT confined to the workspace root the way an upload's path is: a grpcview workspace opened
	// at a subdirectory of a monorepo has its bazel root ABOVE it, which is the common case and the
	// only reason the field exists.
	//
	// It is still bounded, because the trust decision is about ONE root: the value is repo state a
	// colleague commits, and without a bound trusting this workspace would authorize a build whose
	// cwd is inside a DIFFERENT, untrusted repo — whose BUILD files then execute. So the resolved
	// root has to be on the same line as the trusted one, an ancestor (the monorepo case) or a
	// descendant (a nested bazel tree inside the workspace); anything sideways is refused.
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
	if err := relatedToRoot(root, bazelRoot); err != nil {
		return bazelbuild.Builder{}, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"bazel.root in %s resolves to %s, which is neither inside the trusted workspace %s nor an ancestor of it: trust covers one workspace root, and building with a cwd in an unrelated repo would run that repo's BUILD files instead",
			store.WorkspaceFileName, bazelRoot, root))
	}

	// A zero timeout_seconds means "the default", which Builder already spells; passing 0 through
	// keeps that one definition rather than duplicating the number here.
	return bazelbuild.Builder{
		Root:    bazelRoot, // cleaned above, before the bound was checked against it
		Timeout: time.Duration(cfg.GetTimeoutSeconds()) * time.Second,
	}, nil
}

// resolveBazel builds label and resolves this source from what the build wrote.
//
// The label is canonicalized FIRST, before anything derives an id from it, so "//pkg" and
// "//pkg:pkg" cannot become two sources (see bazelbuild.CanonicalLabel).
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
	// A bazel source has no ListServices to narrow it, so what it SERVES is every service the built
	// set defines — exactly an upload's rule, which is why this shares an upload's resolve.
	return resolveUpload(id, dedupeFiles(sets))
}

// dedupeFiles concatenates the built sets, first spelling of each proto file name winning, in the
// order cquery printed them. It is not an optimization: a merging rule (a proto_descriptor_set over
// several proto_librarys) emits its inputs' per-target sets, so one file name legitimately appears
// in more than one of them, and desc.CreateFileDescriptorsFromSet REJECTS a duplicate file name.
// Deduping by name alone is safe because every copy came out of one build of one target — they are
// the same file by construction, not two sources disagreeing (that is mergeSources' problem).
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

// bazelResolveError is the ONE seam where bazelbuild's plain errors become connect codes — that
// package returns plain errors on purpose so it stays usable outside an RPC. The message is passed
// through untouched: each one already names its own fix (--remote_download_toplevel,
// bazel.timeout_seconds, the stderr tail), and rewording it here would lose that.
//
// FailedPrecondition is the default because it describes every remaining case: the label is fine
// and the request is fine, the workspace is simply not in a state where it can produce descriptors —
// an unbuildable target, a target that is not a descriptor set, outputs left on a remote cache. A
// build that ran out of its own timeout lands here too, carrying the message that names
// bazel.timeout_seconds; only a deadline that arrives as a real context error is reported as one.
func bazelResolveError(ctx context.Context, err error) error {
	switch {
	case ctx.Err() != nil:
		// The caller went away, so this is not the workspace's fault at all.
		return connect.NewError(connect.CodeCanceled, err)
	case errors.Is(err, context.DeadlineExceeded):
		return connect.NewError(connect.CodeDeadlineExceeded, err)
	default:
		return connect.NewError(connect.CodeFailedPrecondition, err)
	}
}

// resolveOne re-acquires one source from its POINTER. Reflection dials, bazel builds, and an upload
// re-reads the file its add recorded — an upload with NO path is the only kind with nothing to
// re-acquire from, and saying so is the point: silently succeeding would report a refresh that
// re-fetched nothing.
//
// Every path into it is a source-MUTATING RPC: add and refresh call it directly, and the rest reach
// it through resolveConfigured when a listed source has no stored resolve yet. Nothing on the READ
// path stats a recipe or rebuilds a label — deriveDefinitions reports an unresolved source with
// unresolvedReason instead — so an untrusted or unbuildable bazel source still LOADS, with the reason
// in its Resolved.error and no acquisition triggered by somebody opening a collection.
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
		// The recorded path is wire input too (a colleague's grpcview.json wrote it), so it is
		// confined here exactly as it was on the add — and the READ side is where the confinement
		// has to be STRICT: an add may decline to record a recipe it cannot confine, but nothing
		// may ever re-read one that leaves the workspace. What comes back is the symlink-resolved
		// path, which is what closes the check-then-read window on it.
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

// errMissingDefinition is the one failure a REFERENCE has of its own. An entry in a collection's
// list carrying an id and no config means "resolve this against the workspace manifest", so a
// source that reaches any consumer with no kind at all is exactly that entry with nothing behind
// it — a definition renamed, deleted, or (for an upload) never legal at the workspace level. The
// message names the file to fix, because the row is in one manifest and the fix is in another.
func errMissingDefinition(id string) error {
	return fmt.Errorf(
		"%s does not define the source %q this collection references: define it there, or remove the reference from this collection",
		store.WorkspaceFileName, id)
}

// unresolvedReason is why a source in the list contributed nothing. Both cases contribute nothing
// and neither is fatal, but only one is fixed by a refresh — a reference with no definition would
// resolve forever without ever acquiring anything.
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

// mergedView is what merging the resolved sources in priority order produced: the whole derived
// half of a collection, which is held in memory and never written to disk.
type mergedView struct {
	services      []*grpcviewv1.Service
	descriptorSet []byte
	summaries     map[string]*grpcviewv1.Resolved
	// serviceDescs is descriptorSet in LINKED form, indexed by service full name. It is carried
	// out of the merge rather than re-linked by whoever consumes it: linking is the expensive
	// half of the whole operation, and the merge has already done it to decide which services
	// survived.
	serviceDescs map[string]*desc.ServiceDescriptor
}

// summarize is the per-source half of a merge, which is independent of whether the merge itself
// succeeds — a caller that has to report an unmergeable source list still needs these rows.
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

	// Dial attribution is deliberately separate from who won the descriptors: an upload has no
	// address, so a service it wins still dials the first reflection source serving it.
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
				// It lost its defining file to a higher-priority source that doesn't define
				// the service, so there is nothing to resolve it against.
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
	// A mutation still FAILS on an unmergeable source list, unlike a read (see
	// deriveDefinitions): the user is changing the sources right now, so refusing the change is
	// actionable where refusing to load a collection they did not touch would not be.
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
	// The writer invalidates, and that is the whole of the memo's coherency: nothing but these
	// four RPCs can change a collection's descriptors. The freshly computed view is deliberately
	// NOT grafted in — the next reader re-derives from the blobs that were just written, so
	// there is one derivation path rather than two that could disagree.
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
		delete(byID, id) // so a duplicate id is rejected as unknown
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
