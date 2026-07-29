package workspace

import (
	"testing"

	"github.com/jhump/protoreflect/desc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

// setFor builds a self-contained FileDescriptorSet for a registered proto file
// (the file plus its transitive dependencies), standing in for what a source
// resolves to.
func setFor(t *testing.T, path string) *descriptorpb.FileDescriptorSet {
	t.Helper()
	fd, err := protoregistry.GlobalFiles.FindFileByPath(path)
	if err != nil {
		t.Fatalf("find file descriptor %s: %v", path, err)
	}
	wrapped, err := desc.WrapFile(fd)
	if err != nil {
		t.Fatalf("wrap file descriptor %s: %v", path, err)
	}
	return desc.ToFileDescriptorSet(wrapped)
}

// withComments returns a copy of fds whose files all carry source_code_info,
// standing in for a `buf build` image. Its counterpart — the same files WITHOUT
// source_code_info — stands in for gRPC reflection output, which strips it. Which
// of the two wins the merge is exactly what decides whether the editor can show
// proto doc comments, so it is the observable the priority tests assert on.
func withComments(fds *descriptorpb.FileDescriptorSet) *descriptorpb.FileDescriptorSet {
	out := proto.CloneOf(fds)
	for _, f := range out.GetFile() {
		f.SourceCodeInfo = &descriptorpb.SourceCodeInfo{
			// path [2] is FileDescriptorProto.package; span is {line, col, endCol}.
			// Both must be well-formed or the linker rejects the file outright.
			Location: []*descriptorpb.SourceCodeInfo_Location{{
				Path:            []int32{2},
				Span:            []int32{0, 0, 10},
				LeadingComments: proto.String(" a doc comment\n"),
			}},
		}
	}
	return out
}

// stripComments returns a copy of fds with source_code_info removed from every file.
func stripComments(fds *descriptorpb.FileDescriptorSet) *descriptorpb.FileDescriptorSet {
	out := proto.CloneOf(fds)
	for _, f := range out.GetFile() {
		f.SourceCodeInfo = nil
	}
	return out
}

// commentedFiles counts the files in a marshaled FileDescriptorSet that carry
// source_code_info.
func commentedFiles(t *testing.T, raw []byte) int {
	t.Helper()
	fds := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(raw, fds); err != nil {
		t.Fatalf("unmarshal merged set: %v", err)
	}
	n := 0
	for _, f := range fds.GetFile() {
		if f.GetSourceCodeInfo() != nil {
			n++
		}
	}
	return n
}

func hasServiceNamed(services []*grpcviewv1.Service, fq string) *grpcviewv1.Service {
	for _, s := range services {
		if s.GetPackage()+"."+s.GetName() == fq {
			return s
		}
	}
	return nil
}

// TestMergeSourcesPriorityDecidesDescriptors is the regression test for the bug
// that made multiple sources unusable: two sources describing the SAME protos, one
// of them richer. The merge must be decided by the source list's priority order
// alone — so putting the descriptor-set upload first keeps its doc comments, and
// putting the reflection source first does not — and it must be a pure function of
// that order, never of which source happened to be added last.
func TestMergeSourcesPriorityDecidesDescriptors(t *testing.T) {
	base := setFor(t, "grpc/health/v1/health.proto")
	rich := withComments(base)
	plain := stripComments(base)
	server := &grpcviewv1.Server{Address: "localhost:50051"}

	upload := &resolvedSource{
		id:       "upload:image.binpb",
		files:    rich,
		services: []string{"grpc.health.v1.Health"},
	}
	reflection := &resolvedSource{
		id:       "reflection:localhost:50051",
		server:   server,
		files:    plain,
		services: []string{"grpc.health.v1.Health"},
	}

	// Upload first: its commented copy of every shared file wins.
	services, merged, summaries, err := mergeSources([]*resolvedSource{upload, reflection})
	if err != nil {
		t.Fatalf("mergeSources(upload, reflection): %v", err)
	}
	if got := commentedFiles(t, merged); got != len(rich.GetFile()) {
		t.Errorf("upload first: want all %d files commented, got %d", len(rich.GetFile()), got)
	}
	if got := summaries["upload:image.binpb"].GetWonServiceNames(); len(got) != 1 {
		t.Errorf("upload first: want the upload credited with Health, got %v", got)
	}
	if got := summaries["reflection:localhost:50051"].GetWonServiceNames(); len(got) != 0 {
		t.Errorf("upload first: want the reflection source shadowed, got %v", got)
	}

	// The dial target still comes from the reflection source, even though the upload
	// won the descriptors — an upload has no address, so without that split the
	// request would be stranded with no target.
	health := hasServiceNamed(services, "grpc.health.v1.Health")
	if health == nil {
		t.Fatalf("Health missing from merged services: %v", services)
	}
	if got := health.GetSource().GetAddress(); got != "localhost:50051" {
		t.Errorf("want dial target localhost:50051 even with the upload winning, got %q", got)
	}

	// Reflection first: the stripped copy wins and the comments are gone. Same
	// inputs, opposite order — the ONLY thing that changed.
	_, merged, summaries, err = mergeSources([]*resolvedSource{reflection, upload})
	if err != nil {
		t.Fatalf("mergeSources(reflection, upload): %v", err)
	}
	if got := commentedFiles(t, merged); got != 0 {
		t.Errorf("reflection first: want no commented files, got %d", got)
	}
	if got := summaries["reflection:localhost:50051"].GetWonServiceNames(); len(got) != 1 {
		t.Errorf("reflection first: want the reflection source credited with Health, got %v", got)
	}
}

// TestMergeSourcesFillsGaps asserts a lower-priority source still contributes what
// no higher-priority source covers, so ordering a source down never means losing it.
func TestMergeSourcesFillsGaps(t *testing.T) {
	health := &resolvedSource{
		id:       "upload:health.binpb",
		files:    setFor(t, "grpc/health/v1/health.proto"),
		services: []string{"grpc.health.v1.Health"},
	}
	reflectionOnly := &resolvedSource{
		id:       "reflection:localhost:50051",
		server:   &grpcviewv1.Server{Address: "localhost:50051"},
		files:    setFor(t, "grpc/reflection/v1/reflection.proto"),
		services: []string{"grpc.reflection.v1.ServerReflection"},
	}

	services, _, summaries, err := mergeSources([]*resolvedSource{health, reflectionOnly})
	if err != nil {
		t.Fatalf("mergeSources: %v", err)
	}
	if hasServiceNamed(services, "grpc.health.v1.Health") == nil {
		t.Errorf("Health (first source) missing: %v", services)
	}
	if hasServiceNamed(services, "grpc.reflection.v1.ServerReflection") == nil {
		t.Errorf("ServerReflection (second source) missing: %v", services)
	}
	for id, want := range map[string]int{"upload:health.binpb": 1, "reflection:localhost:50051": 1} {
		if got := len(summaries[id].GetWonServiceNames()); got != want {
			t.Errorf("%s: want %d won services, got %d", id, want, got)
		}
	}
}

// TestMergeSourcesUnresolvedSourceIsNotFatal asserts a source that failed to
// resolve contributes nothing, records why, and does not stop the others from
// merging — the property that keeps a removal or reorder working while some
// unrelated reflection target is down.
func TestMergeSourcesUnresolvedSourceIsNotFatal(t *testing.T) {
	dead := &resolvedSource{id: "reflection:localhost:1", err: errAlwaysDown}
	live := &resolvedSource{
		id:       "upload:health.binpb",
		files:    setFor(t, "grpc/health/v1/health.proto"),
		services: []string{"grpc.health.v1.Health"},
	}

	services, merged, summaries, err := mergeSources([]*resolvedSource{dead, live})
	if err != nil {
		t.Fatalf("mergeSources: %v", err)
	}
	if hasServiceNamed(services, "grpc.health.v1.Health") == nil {
		t.Errorf("the healthy source must still merge: %v", services)
	}
	if len(merged) == 0 {
		t.Error("want a merged descriptor set from the healthy source")
	}
	if got := summaries["reflection:localhost:1"].GetError(); got == "" {
		t.Error("want the failed source's error recorded in its summary")
	}
}

var errAlwaysDown = &staticError{"connection refused"}

type staticError struct{ msg string }

func (e *staticError) Error() string { return e.msg }

func TestSourceID(t *testing.T) {
	cases := []struct {
		name string
		src  *grpcviewv1.DescriptorSource
		want string
	}{{
		name: "reflection",
		src: &grpcviewv1.DescriptorSource{
			Source: &grpcviewv1.DescriptorSource_Reflection{Reflection: &grpcviewv1.Server{Address: "localhost:8080"}},
		},
		want: "reflection:localhost:8080",
	}, {
		name: "reflection with tls",
		src: &grpcviewv1.DescriptorSource{
			Source: &grpcviewv1.DescriptorSource_Reflection{
				Reflection: &grpcviewv1.Server{Address: "api.example.com:443", Tls: &grpcviewv1.Server_TLS{}},
			},
		},
		want: "reflection:api.example.com:443+tls",
	}, {
		name: "upload",
		src: &grpcviewv1.DescriptorSource{
			Source: &grpcviewv1.DescriptorSource_Upload{Upload: &grpcviewv1.Upload{FileName: "buf_image.binpb"}},
		},
		want: "upload:buf_image.binpb",
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sourceID(tc.src); got != tc.want {
				t.Errorf("sourceID = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestUpsertSourceRefreshesInPlace asserts re-adding a source with the same id
// replaces it AT ITS PRIORITY rather than appending a duplicate — so re-uploading a
// rebuilt image refreshes the source it came from instead of accumulating
// indistinguishable rows, and doesn't silently demote it either.
func TestUpsertSourceRefreshesInPlace(t *testing.T) {
	srcAt := func(id string) *grpcviewv1.DescriptorSource {
		return &grpcviewv1.DescriptorSource{Id: id}
	}
	sources := []*grpcviewv1.DescriptorSource{srcAt("upload:a.binpb"), srcAt("reflection:x:1")}

	sources = upsertSource(sources, srcAt("upload:a.binpb"))
	if len(sources) != 2 || sources[0].GetId() != "upload:a.binpb" {
		t.Fatalf("re-adding the same id must refresh in place, got %v", sourceIDsOf(sources))
	}

	sources = upsertSource(sources, srcAt("upload:b.binpb"))
	if len(sources) != 3 || sources[2].GetId() != "upload:b.binpb" {
		t.Fatalf("a new source must append at lowest priority, got %v", sourceIDsOf(sources))
	}
}

func TestReorderSources(t *testing.T) {
	sources := []*grpcviewv1.DescriptorSource{{Id: "a"}, {Id: "b"}, {Id: "c"}}

	got, err := reorderSources(sources, []string{"c", "a", "b"})
	if err != nil {
		t.Fatalf("reorderSources: %v", err)
	}
	if ids := sourceIDsOf(got); ids[0] != "c" || ids[1] != "a" || ids[2] != "b" {
		t.Errorf("reorderSources = %v, want [c a b]", ids)
	}

	// Anything that isn't a full permutation is rejected outright, so a client
	// working from a stale list can never silently drop a source.
	for _, bad := range [][]string{{"a", "b"}, {"a", "b", "d"}, {"a", "b", "b"}, nil} {
		if _, err := reorderSources(sources, bad); err == nil {
			t.Errorf("reorderSources(%v): want error, got nil", bad)
		}
	}
}

func sourceIDsOf(sources []*grpcviewv1.DescriptorSource) []string {
	ids := make([]string, 0, len(sources))
	for _, s := range sources {
		ids = append(ids, s.GetId())
	}
	return ids
}
