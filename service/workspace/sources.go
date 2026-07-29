package workspace

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"connectrpc.com/connect"

	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/grpcreflect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	grpcviewstorev1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/store/v1"
	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/store"
)

// This file owns descriptor sources: their identity, how each one is resolved,
// and how the resolves merge into the workspace's single flat view.
//
// The model is deliberately two-layered. Each source resolves INDEPENDENTLY to
// its own descriptors (cached per source), and the workspace's services +
// descriptor_set are then DERIVED by merging those resolves in the source list's
// priority order. Every mutation — add, refresh, remove, reorder — re-derives the
// whole merged view from the caches, so the outcome depends only on the source
// list and never on the order the mutations happened to arrive in.
//
// That layering is what makes multiple sources describing the SAME protos work.
// The merged view is rebuilt, not patched, so nothing can be half-overwritten;
// only the source being added or refreshed touches the network, so a dead
// reflection target can't block a removal; and the priority order is explicit, so
// "which source's definitions win" is something the user sets rather than a
// side effect of write order. It matters concretely because the two source kinds
// are not equivalent: gRPC reflection strips source_code_info, so a buf-built
// upload of the same files carries doc comments the live server cannot.

// resolvedSource is one source's independent resolve: the descriptors it
// provides, the services it actually serves, and where to dial them.
type resolvedSource struct {
	id string
	// server is the dial target this source's services are reached at, nil for an
	// upload (which has no address). It becomes Service.source for services no
	// higher-priority reflection source serves.
	server *grpcviewv1.Server
	// files is this source's own descriptor set, self-contained (transitive deps
	// included). For an upload it is the uploaded bytes verbatim, so a buf-built
	// image keeps its source_code_info.
	files *descriptorpb.FileDescriptorSet
	// services are the fully-qualified names this source SERVES. It is narrower
	// than "every service defined in files": a reflection server's ListServices is
	// authoritative, and a dependency file pulled in for its messages may define
	// services the server does not expose.
	services []string
	// err is why resolving failed, if it did. A failed resolve contributes nothing
	// but is not fatal — see resolveConfigured.
	err error
}

// sourceID derives a source's stable identity from its config. The format lives
// in the store package (store.SourceID) because the manifest, the per-source cache
// paths and the migration of manifests older than ids all key off it, and two
// spellings of an identity would be two identities.
func sourceID(src *grpcviewv1.DescriptorSource) string { return store.SourceID(src) }

// resolveReflection dials a reflection target, asks it which services it serves,
// and collects the files defining them together with their transitive
// dependencies. One network round-trip per call; the connection is closed before
// returning.
//
// Failures are typed Unavailable: every one of them means the target is down, not
// serving reflection, or lost mid-resolve — a condition the user fixes by fixing
// the server and refreshing, which is what an unreachable-upstream code tells a
// client. Left untyped they surface as CodeUnknown/500, indistinguishable from a
// bug in grpcview itself.
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

// resolveUpload links an uploaded FileDescriptorSet and lists every service it
// defines. The set must be self-contained — carrying the transitive dependencies
// of its files, as `protoc --include_imports` and `buf build` produce — or linking
// fails. The bytes are kept verbatim as the source's descriptors so nothing is
// normalized away, doc comments least of all. Link failures surface as
// InvalidArgument because the bytes are caller-supplied.
func resolveUpload(id string, fds *descriptorpb.FileDescriptorSet) (*resolvedSource, error) {
	files, err := desc.CreateFileDescriptorsFromSet(fds)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("link descriptor set: %w", err))
	}
	// Walked in the set's own file order so the service list is deterministic.
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

// parseUpload decodes raw uploaded descriptor-set bytes into the ONE canonical form
// the rest of the pipeline sees. Parse failures surface as InvalidArgument because
// the bytes are caller-supplied.
//
// DiscardUnknown drops the extension fields a `buf build` image carries beyond a
// plain FileDescriptorSet (`buf.alpha.image.v1`, ~14% of such a file). Nothing here
// reads them, and dropping them at this one boundary is what makes an upload's
// descriptors identical whether they were just uploaded or re-read from the
// manifest — the committed protojson form can't hold unknown fields, so without
// this the merged descriptor_set would differ before and after a reload of the
// very same source. source_code_info is a normal field and is unaffected.
func parseUpload(raw []byte) (*descriptorpb.FileDescriptorSet, error) {
	fds := &descriptorpb.FileDescriptorSet{}
	if err := (proto.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, fds); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("parse descriptor set: %w", err))
	}
	return fds, nil
}

// resolveOne resolves a single source from scratch, hitting the network for
// reflection and re-parsing the committed descriptors for an upload. It is the
// explicit-request path (add / refresh), so a failure is returned as an error for
// the caller to surface.
func (w Workspace) resolveOne(ctx context.Context, coll *store.Collection, src *grpcviewv1.DescriptorSource) (*resolvedSource, error) {
	id := src.GetId()
	switch s := src.GetSource().(type) {
	case *grpcviewv1.DescriptorSource_Reflection:
		return resolveReflection(ctx, id, s.Reflection)
	case *grpcviewv1.DescriptorSource_Upload:
		fds, err := coll.UploadDescriptors(ctx, id)
		if err != nil {
			return nil, err
		}
		if fds == nil {
			return nil, connect.NewError(connect.CodeNotFound,
				fmt.Errorf("upload source %q has no stored descriptors", id))
		}
		return resolveUpload(id, fds)
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("source %q has no kind", id))
	}
}

// resolveConfigured produces a resolve for every configured source, in priority
// order, reusing fresh resolves the caller just performed and otherwise the
// per-source cache. Only a source with neither is resolved from scratch here.
//
// A source that cannot be resolved is recorded with its error and contributes
// nothing, rather than failing the whole operation: removing or reordering
// sources must keep working while some unrelated reflection target is down.
func (w Workspace) resolveConfigured(
	ctx context.Context,
	coll *store.Collection,
	sources []*grpcviewv1.DescriptorSource,
	fresh map[string]*resolvedSource,
) ([]*resolvedSource, error) {
	cached, err := coll.SourceResolves(ctx)
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
		rs, err := w.resolveOne(ctx, coll, src)
		if err != nil {
			slog.Default().Warn("descriptor source unresolved", "source", id, "error", err)
			out = append(out, &resolvedSource{id: id, server: src.GetReflection(), err: err})
			continue
		}
		out = append(out, rs)
	}
	return out, nil
}

// mergeSources derives the workspace's flat view from the per-source resolves,
// which arrive in PRIORITY order.
//
// Walking front to back, the first source to define a proto file (by file name)
// wins that file, and the first to serve a service (by full name) wins its place
// in the services list. Later sources only fill the gaps. So placing a buf-built
// upload ahead of a reflection source keeps that upload's richer descriptors —
// doc comments included — for every file they share, while the reflection source
// still contributes whatever the upload doesn't cover.
//
// A service's DIAL TARGET is resolved separately, and deliberately so: it is the
// first reflection source that serves the service, whichever source won its
// descriptors. An upload has no address, so without that split putting one first
// for its comments would strand every request it claimed with no target.
func mergeSources(resolved []*resolvedSource) ([]*grpcviewv1.Service, []byte, map[string]*grpcviewv1.Resolved, error) {
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

	// Claim files by name, highest-priority source first.
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
		return nil, nil, summaries, nil
	}

	// Link the claimed files as one set. Mixing files from sources built at
	// different times can produce a set that doesn't link (a winning file
	// referencing a symbol only a losing source's version of another file defines),
	// so this is checked rather than assumed: a clear error naming the sources beats
	// a silently broken workspace.
	linked, err := desc.CreateFileDescriptorsFromSet(&descriptorpb.FileDescriptorSet{File: claimed})
	if err != nil {
		return nil, nil, nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"descriptor sources %s cannot be merged — they disagree about the protos they share: %w",
			strings.Join(sourceIDs(resolved), ", "), err))
	}

	// Which service is defined where, after the file merge.
	serviceDescs := make(map[string]*desc.ServiceDescriptor)
	for _, fd := range linked {
		for _, sd := range fd.GetServices() {
			serviceDescs[sd.GetFullyQualifiedName()] = sd
		}
	}

	// Dial attribution: the first reflection source serving each service.
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
				// The source serves a service whose defining file it lost to a
				// higher-priority source that doesn't define it. Nothing to resolve it
				// against, so skip rather than emit a service with no descriptors.
				continue
			}
			seen[name] = true
			services = append(services, convertService(sd, dialFor[name]))
			// Credit the source whose descriptors the workspace actually uses, which is
			// whoever won the defining file — not necessarily rs.
			winner := claimedBy[sd.GetFile().GetName()]
			summaries[winner].WonServiceNames = append(summaries[winner].WonServiceNames, name)
		}
	}

	// One merged, deduped, topo-sorted set for the client's protoc-gen-es.
	all := make([]*desc.FileDescriptor, 0, len(claimed))
	for _, f := range claimed {
		if fd := linked[f.GetName()]; fd != nil {
			all = append(all, fd)
		}
	}
	descriptorSet, err := proto.Marshal(desc.ToFileDescriptorSet(all...))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("marshal merged descriptor set: %w", err)
	}
	return services, descriptorSet, summaries, nil
}

func sourceIDs(resolved []*resolvedSource) []string {
	ids := make([]string, 0, len(resolved))
	for _, rs := range resolved {
		ids = append(ids, rs.id)
	}
	return ids
}

// putDescriptorState re-derives the whole merged view for the given priority-
// ordered source list and persists it together with the list. Every source
// mutation ends here, so they all share one definition of what the workspace's
// services and descriptor_set mean.
func (w Workspace) putDescriptorState(
	ctx context.Context,
	coll *store.Collection,
	sources []*grpcviewv1.DescriptorSource,
	fresh map[string]*resolvedSource,
	uploads map[string]*descriptorpb.FileDescriptorSet,
) error {
	resolved, err := w.resolveConfigured(ctx, coll, sources, fresh)
	if err != nil {
		return err
	}
	services, descriptorSet, summaries, err := mergeSources(resolved)
	if err != nil {
		return err
	}

	// Cache each source's own resolve so the next mutation re-derives without
	// touching the network. A failed resolve writes nothing, leaving the previous
	// entry (if any) in place to be superseded by a successful refresh.
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
	// The summaries ride on the persisted source list, so the sources view can show
	// what each one contributed without re-resolving.
	stored := make([]*grpcviewv1.DescriptorSource, 0, len(sources))
	for _, src := range sources {
		clone := proto.CloneOf(src)
		clone.Resolved = summaries[src.GetId()]
		stored = append(stored, clone)
	}

	return coll.PutDescriptorState(ctx, store.DescriptorState{
		Sources:       stored,
		Uploads:       uploads,
		Resolves:      resolves,
		Services:      services,
		DescriptorSet: descriptorSet,
	})
}

// upsertSource places src in the list: replacing the entry with the same id
// (a refresh in place, keeping its priority) or appending it at LOWEST priority.
// Appending last means adding a source never changes where an existing service
// resolves from — a new source fills gaps until the user promotes it.
func upsertSource(sources []*grpcviewv1.DescriptorSource, src *grpcviewv1.DescriptorSource) []*grpcviewv1.DescriptorSource {
	i := slices.IndexFunc(sources, func(s *grpcviewv1.DescriptorSource) bool { return s.GetId() == src.GetId() })
	if i == -1 {
		return append(sources, src)
	}
	sources[i] = src
	return sources
}

// reorderSources returns sources permuted to match ids. ids must name every
// configured source exactly once; anything else is rejected rather than applied
// partially, so a client working from a stale list can't silently drop a source.
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
		delete(byID, id) // a repeat would now be unknown, so duplicates are rejected too
		out = append(out, s)
	}
	return out, nil
}

// convertService builds a wire Service (package/name + method input/output message
// identities) from a resolved service descriptor. This is the schema-conversion
// step shared across source kinds — reflection and upload both funnel through
// here. dial is the service's invoke target (nil when no reflection source serves
// it); it becomes Service.source, the request's default target (resolveTarget).
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

		// client_streaming/server_streaming carry the method's real kind so the
		// tree/tabs render the right tag and InvokeStreaming maps onto the right
		// call shape. The editor's typing comes from the client-side protoc-gen-es
		// over Workspace.descriptor_set keyed by each message's package/name/file —
		// no per-message JSON schema is sent.
		service.Methods[j] = &grpcviewv1.Method{
			Name: methodDesc.GetName(),
			Input: &grpcviewv1.Message{
				Package: inputDesc.GetFile().AsFileDescriptorProto().GetPackage(),
				Name:    inputDesc.GetName(),
				// File is the proto path defining this message — the protoc-gen-es
				// fileToGenerate selector into Workspace.descriptor_set. It rides the
				// wire-typed services cache for free.
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
	return service
}
