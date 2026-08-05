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
	// DiscardUnknown drops a `buf build` image's extension fields, which the committed protojson
	// form cannot hold — without it a reload of the same source would yield different bytes.
	fds := &descriptorpb.FileDescriptorSet{}
	if err := (proto.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw, fds); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("parse descriptor set: %w", err))
	}
	return fds, nil
}

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

func (w Workspace) resolveConfigured(
	ctx context.Context,
	coll *store.Collection,
	sources []*grpcviewv1.DescriptorSource,
	fresh map[string]*resolvedSource,
) ([]*resolvedSource, error) {
	cached, err := coll.DescriptorBlobs(ctx)
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
	uploads map[string]*descriptorpb.FileDescriptorSet,
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
		Uploads:  uploads,
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
