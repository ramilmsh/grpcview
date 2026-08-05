package workspace

import (
	"testing"

	"github.com/jhump/protoreflect/desc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

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

func withComments(fds *descriptorpb.FileDescriptorSet) *descriptorpb.FileDescriptorSet {
	out := proto.CloneOf(fds)
	for _, f := range out.GetFile() {
		f.SourceCodeInfo = &descriptorpb.SourceCodeInfo{
			// path [2] is FileDescriptorProto.package; a well-formed span is required or the linker rejects the file.
			Location: []*descriptorpb.SourceCodeInfo_Location{{
				Path:            []int32{2},
				Span:            []int32{0, 0, 10},
				LeadingComments: proto.String(" a doc comment\n"),
			}},
		}
	}
	return out
}

func stripComments(fds *descriptorpb.FileDescriptorSet) *descriptorpb.FileDescriptorSet {
	out := proto.CloneOf(fds)
	for _, f := range out.GetFile() {
		f.SourceCodeInfo = nil
	}
	return out
}

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

	view, err := mergeSources([]*resolvedSource{upload, reflection})
	if err != nil {
		t.Fatalf("mergeSources(upload, reflection): %v", err)
	}
	services, merged, summaries := view.services, view.descriptorSet, view.summaries
	if got := commentedFiles(t, merged); got != len(rich.GetFile()) {
		t.Errorf("upload first: want all %d files commented, got %d", len(rich.GetFile()), got)
	}
	if got := summaries["upload:image.binpb"].GetWonServiceNames(); len(got) != 1 {
		t.Errorf("upload first: want the upload credited with Health, got %v", got)
	}
	if got := summaries["reflection:localhost:50051"].GetWonServiceNames(); len(got) != 0 {
		t.Errorf("upload first: want the reflection source shadowed, got %v", got)
	}

	health := hasServiceNamed(services, "grpc.health.v1.Health")
	if health == nil {
		t.Fatalf("Health missing from merged services: %v", services)
	}
	if got := health.GetSource().GetAddress(); got != "localhost:50051" {
		t.Errorf("want dial target localhost:50051 even with the upload winning, got %q", got)
	}

	view, err = mergeSources([]*resolvedSource{reflection, upload})
	if err != nil {
		t.Fatalf("mergeSources(reflection, upload): %v", err)
	}
	merged, summaries = view.descriptorSet, view.summaries
	if got := commentedFiles(t, merged); got != 0 {
		t.Errorf("reflection first: want no commented files, got %d", got)
	}
	if got := summaries["reflection:localhost:50051"].GetWonServiceNames(); len(got) != 1 {
		t.Errorf("reflection first: want the reflection source credited with Health, got %v", got)
	}
}

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

	view, err := mergeSources([]*resolvedSource{health, reflectionOnly})
	if err != nil {
		t.Fatalf("mergeSources: %v", err)
	}
	services, summaries := view.services, view.summaries
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

func TestMergeSourcesUnresolvedSourceIsNotFatal(t *testing.T) {
	dead := &resolvedSource{id: "reflection:localhost:1", err: errAlwaysDown}
	live := &resolvedSource{
		id:       "upload:health.binpb",
		files:    setFor(t, "grpc/health/v1/health.proto"),
		services: []string{"grpc.health.v1.Health"},
	}

	view, err := mergeSources([]*resolvedSource{dead, live})
	if err != nil {
		t.Fatalf("mergeSources: %v", err)
	}
	services, merged, summaries := view.services, view.descriptorSet, view.summaries
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
