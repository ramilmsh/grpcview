package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/jhump/protoreflect/desc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/protofiles"
)

const renderName = "service/workspace/testdata/render.proto"

// testdata/render.binpb: protoc's set for render.proto, with SourceCodeInfo (built by bazel).
func renderFixture(t *testing.T) (*descriptorpb.FileDescriptorSet, []byte) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "render.binpb"))
	if err != nil {
		t.Fatal(err)
	}
	fds := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(raw, fds); err != nil {
		t.Fatal(err)
	}
	return fds, raw
}

func renderedProtos(t *testing.T, fds *descriptorpb.FileDescriptorSet) []*grpcviewv1.ProtoDefinitionFile {
	t.Helper()
	linked, err := desc.CreateFileDescriptorsFromSet(fds)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]*desc.FileDescriptor{}
	claimedBy := map[string]string{}
	for _, f := range fds.GetFile() {
		files[f.GetName()] = linked[f.GetName()]
		claimedBy[f.GetName()] = "upload:x"
	}
	out, err := protoFiles(files, claimedBy, nil)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// compiled compiles one in-memory file and returns its descriptor without SourceCodeInfo.
func compiled(t *testing.T, name, text string) *descriptorpb.FileDescriptorProto {
	t.Helper()
	res, err := protofiles.Compile(context.Background(), protofiles.ResolverFunc(func(n string) ([]byte, error) {
		if n != name {
			return nil, os.ErrNotExist
		}
		return []byte(text), nil
	}), name)
	if err != nil {
		t.Fatalf("compile %s: %v\n%s", name, err, text)
	}
	for _, f := range res.Set.GetFile() {
		if f.GetName() == name {
			f.SourceCodeInfo = nil
			return f
		}
	}
	t.Fatalf("%s not in its own compile", name)
	return nil
}

func TestRenderProto(t *testing.T) {
	fds, _ := renderFixture(t)
	if commentedFiles(t, mustMarshal(t, fds)) == 0 {
		t.Fatal("fixture has no SourceCodeInfo")
	}
	protos := renderedProtos(t, fds)
	if len(protos) != 1 || protos[0].GetPath() != renderName {
		t.Fatalf("protos = %v, want only %s (WKTs omitted)", protos, renderName)
	}
	got := protos[0]
	if !got.GetRendered() || got.GetSource() != "upload:x" {
		t.Errorf("rendered, source = %v, %q", got.GetRendered(), got.GetSource())
	}
	for _, want := range []string{
		"repeated Book books = 7;",
		"google.protobuf.Timestamp updated = 3; // last write",
		"GENRE_FICTION = 2;",
		"int32 slot = 4;",
		"// Stable id.",
		"// One book.",
		"// Fetches a shelf.",
		"rpc GetShelf ( Shelf ) returns ( Shelf );",
	} {
		if !strings.Contains(got.GetContent(), want) {
			t.Errorf("rendered lacks %q:\n%s", want, got.GetContent())
		}
	}
	// Source order, not sorted: books (7) before updated (3).
	if c := got.GetContent(); strings.Index(c, "books = 7") > strings.Index(c, "updated = 3") {
		t.Error("fields reordered")
	}
	for range 4 {
		if again := renderedProtos(t, fds)[0].GetContent(); again != got.GetContent() {
			t.Fatal("render is not deterministic")
		}
	}

	// Golden: the rendered text compiles to the same schema as the original.
	original, err := os.ReadFile(filepath.Join("testdata", "render.proto"))
	if err != nil {
		t.Fatal(err)
	}
	if want, have := compiled(t, renderName, string(original)), compiled(t, renderName, got.GetContent()); !proto.Equal(want, have) {
		t.Errorf("round trip differs:\nwant %v\nhave %v", want, have)
	}
}

func mustMarshal(t *testing.T, m proto.Message) []byte {
	t.Helper()
	raw, err := proto.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func protoByPath(protos []*grpcviewv1.ProtoDefinitionFile) map[string]*grpcviewv1.ProtoDefinitionFile {
	out := map[string]*grpcviewv1.ProtoDefinitionFile{}
	for _, p := range protos {
		out[p.GetPath()] = p
	}
	return out
}

func TestGetDefinitionsProtosUpload(t *testing.T) {
	w := newTestWorkspace(t)
	t.Cleanup(func() { _ = w.gen.Close(context.Background()) })
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	_, raw := renderFixture(t)
	uploadTo(t, w, ctx, testWorkspace, raw)

	protos := getDefinitions(t, w, ctx).GetProtos()
	if len(protos) != 1 {
		t.Fatalf("protos = %v", protos)
	}
	p := protos[0]
	if p.GetPath() != renderName || !p.GetRendered() || p.GetSource() != "upload:"+testUploadName ||
		!strings.Contains(p.GetContent(), "repeated Book books = 7;") || !strings.Contains(p.GetContent(), "// Stable id.") {
		t.Errorf("proto = %v", p)
	}
}

func TestGetDefinitionsProtosReflection(t *testing.T) {
	w := newTestWorkspace(t)
	t.Cleanup(func() { _ = w.gen.Close(context.Background()) })
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	echoTarget(t, w, ctx, startEchoServer)

	p := protoByPath(getDefinitions(t, w, ctx).GetProtos())["grpcview/echo/v1/echo.proto"]
	if p == nil || !p.GetRendered() || !strings.HasPrefix(p.GetSource(), "reflection:") ||
		!strings.Contains(p.GetContent(), "service EchoService") || !strings.Contains(p.GetContent(), " = 1;") {
		t.Errorf("echo.proto = %v", p)
	}
}

func TestGetDefinitionsProtosOriginals(t *testing.T) {
	w := newTestWorkspace(t)
	t.Cleanup(func() { _ = w.gen.Close(context.Background()) })
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	root := w.store.Root()
	writeFile(t, filepath.Join(root, "proto/acme/v1/common.proto"), []byte(protosCommon))
	writeFile(t, filepath.Join(root, "proto/acme/v1/svc.proto"), []byte(protosSvc))
	coll, err := addProtos(t, w, ctx, &grpcviewv1.Protos{Path: "proto/acme", ImportPaths: []string{"proto"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	id := coll.GetSources()[0].GetId()

	before := getDefinitions(t, w, ctx)
	got := protoByPath(before.GetProtos())
	if len(got) != 2 { // google/protobuf/empty.proto omitted
		t.Fatalf("protos = %v", before.GetProtos())
	}
	for name, want := range map[string]string{"acme/v1/common.proto": protosCommon, "acme/v1/svc.proto": protosSvc} {
		if p := got[name]; p.GetContent() != want || p.GetRendered() || p.GetSource() != id {
			t.Errorf("%s = %v, want verbatim original", name, p)
		}
	}

	// A trailing newline changes the original but no span: same set, new digest.
	setBefore := w.defs.lookup(testWorkspace).descriptorSet
	writeFile(t, filepath.Join(root, "proto/acme/v1/svc.proto"), []byte(protosSvc+"\n"))
	if _, err := w.RefreshDescriptorSource(ctx, connect.NewRequest(&grpcviewv1.RefreshDescriptorSourceRequest{
		Collection: testWorkspace, Id: id,
	})); err != nil {
		t.Fatal(err)
	}
	after := getDefinitions(t, w, ctx)
	if !proto.Equal(unmarshalSet(t, setBefore), unmarshalSet(t, w.defs.lookup(testWorkspace).descriptorSet)) {
		t.Fatal("descriptor set changed; the test no longer isolates the original")
	}
	if after.GetDigest() == before.GetDigest() {
		t.Error("digest unchanged after an original changed")
	}
	if c := protoByPath(after.GetProtos())["acme/v1/svc.proto"].GetContent(); c != protosSvc+"\n" {
		t.Errorf("svc.proto = %q", c)
	}
}

func unmarshalSet(t *testing.T, raw []byte) *descriptorpb.FileDescriptorSet {
	t.Helper()
	fds := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(raw, fds); err != nil {
		t.Fatal(err)
	}
	return fds
}
