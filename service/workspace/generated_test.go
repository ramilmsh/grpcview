package workspace

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/grpcview/v1"
)

func concatSets(t *testing.T, sets ...[]byte) []byte {
	t.Helper()
	var all descriptorpb.FileDescriptorSet
	for _, raw := range sets {
		var set descriptorpb.FileDescriptorSet
		if err := proto.Unmarshal(raw, &set); err != nil {
			t.Fatalf("unmarshal set: %v", err)
		}
		all.File = append(all.File, set.GetFile()...)
	}
	out, err := proto.Marshal(&all)
	if err != nil {
		t.Fatalf("marshal set: %v", err)
	}
	return out
}

func getDefinitions(t *testing.T, w Workspace, ctx context.Context) *grpcviewv1.GetDefinitionsResponse {
	t.Helper()
	resp, err := w.GetDefinitions(ctx, connect.NewRequest(&grpcviewv1.GetDefinitionsRequest{Collection: testWorkspace}))
	if err != nil {
		t.Fatalf("GetDefinitions: %v", err)
	}
	return resp.Msg
}

func TestGetDefinitions(t *testing.T) {
	w := newTestWorkspace(t)
	t.Cleanup(func() { _ = w.gen.Close(context.Background()) })
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	if _, err := w.GetDefinitions(ctx, connect.NewRequest(&grpcviewv1.GetDefinitionsRequest{Collection: testWorkspace})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("no sources: err = %v, want FailedPrecondition", err)
	}

	echoSet := fileDescriptorSet(t, "grpcview/echo/v1/echo.proto")
	addEchoUpload(t, w, ctx)

	// Concurrent first callers share one generation.
	var wg sync.WaitGroup
	first := make([]*grpcviewv1.GetDefinitionsResponse, 4)
	for i := range first {
		wg.Add(1)
		go func() {
			defer wg.Done()
			first[i] = getDefinitions(t, w, ctx)
		}()
	}
	wg.Wait()
	if n := w.gen.runs.Load(); n != 1 {
		t.Fatalf("generations after concurrent first calls = %d, want 1", n)
	}
	for _, r := range first[1:] {
		if r.GetDigest() != first[0].GetDigest() {
			t.Fatalf("digest differs across concurrent calls")
		}
	}

	again := getDefinitions(t, w, ctx)
	if again.GetDigest() != first[0].GetDigest() || w.gen.runs.Load() != 1 {
		t.Fatalf("second call: digest stable = %v, runs = %d; want memo hit", again.GetDigest() == first[0].GetDigest(), w.gen.runs.Load())
	}
	if len(again.GetDigest()) != 64 {
		t.Errorf("digest = %q, want hex sha256", again.GetDigest())
	}

	got, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{Collection: testWorkspace}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if d := got.Msg.GetCollection().GetDefinitionsDigest(); d != first[0].GetDigest() {
		t.Errorf("Collection.definitions_digest = %q, want GetDefinitions' %q", d, first[0].GetDigest())
	}
	echoSource := got.Msg.GetCollection().GetSources()[0].GetId()

	// A second, lower-priority source redefining echo.proto and adding workspace.proto.
	add := descriptorSetAddReq(concatSets(t, echoSet, fileDescriptorSet(t, "grpcview/v1/workspace.proto")))
	add.FileName = "second.binpb"
	resp, err := w.AddDescriptorSource(ctx, connect.NewRequest(add))
	if err != nil {
		t.Fatalf("AddDescriptorSource (second): %v", err)
	}
	secondSource := resp.Msg.GetCollection().GetSources()[1].GetId()

	defs := getDefinitions(t, w, ctx)
	if defs.GetDigest() == first[0].GetDigest() {
		t.Fatalf("digest unchanged after the merged set changed")
	}
	want := map[string]string{
		"grpcview/echo/v1/echo_pb.ts": echoSource,
		"grpcview/v1/workspace_pb.ts": secondSource,
	}
	gen := generatedFiles(defs)
	if len(gen) != len(want) {
		t.Fatalf("files = %d, want %d", len(gen), len(want))
	}
	for _, f := range gen {
		src, ok := want[f.GetPath()]
		if !ok {
			t.Errorf("unexpected file %q", f.GetPath())
			continue
		}
		if f.GetSource() != src {
			t.Errorf("%s: source = %q, want %q", f.GetPath(), f.GetSource(), src)
		}
	}
	if echo := gen[0]; !strings.Contains(echo.GetContent(), `export const EchoService: GenService`) {
		t.Errorf("%s missing EchoService:\n%s", echo.GetPath(), echo.GetContent())
	}
	// The stubs follow the generated files.
	stubs := defs.GetFiles()[len(gen):]
	if len(stubs) != len(protobufStubs) {
		t.Fatalf("stubs = %d, want %d", len(stubs), len(protobufStubs))
	}
	for _, f := range stubs {
		if f.GetSource() != stubSource || !strings.HasPrefix(f.GetPath(), "node_modules/@bufbuild/protobuf/") {
			t.Errorf("stub %q: source = %q", f.GetPath(), f.GetSource())
		}
	}
	// echo.proto is unchanged, so only workspace.proto is new.
	if r, f := w.gen.runs.Load(), w.gen.files.Load(); r != 2 || f != 2 {
		t.Errorf("runs, files = %d, %d; want 2, 2", r, f)
	}
}

func uploadTo(t *testing.T, w Workspace, ctx context.Context, collection string, set []byte) {
	t.Helper()
	req := descriptorSetAddReq(set)
	req.Collection = collection
	if _, err := w.AddDescriptorSource(ctx, connect.NewRequest(req)); err != nil {
		t.Fatalf("AddDescriptorSource %s: %v", collection, err)
	}
}

func definitionsOf(t *testing.T, w Workspace, ctx context.Context, collection string) map[string]string {
	t.Helper()
	resp, err := w.GetDefinitions(ctx, connect.NewRequest(&grpcviewv1.GetDefinitionsRequest{Collection: collection}))
	if err != nil {
		t.Fatalf("GetDefinitions %s: %v", collection, err)
	}
	out := map[string]string{}
	for _, f := range generatedFiles(resp.Msg) {
		out[f.GetPath()] = f.GetContent()
	}
	return out
}

// generatedFiles drops the builtin stubs, which every response carries.
func generatedFiles(resp *grpcviewv1.GetDefinitionsResponse) []*grpcviewv1.DefinitionFile {
	var out []*grpcviewv1.DefinitionFile
	for _, f := range resp.GetFiles() {
		if f.GetSource() != stubSource {
			out = append(out, f)
		}
	}
	return out
}

func wantRuns(t *testing.T, g *generator, runs, files int64) {
	t.Helper()
	if r, f := g.runs.Load(), g.files.Load(); r != runs || f != files {
		t.Fatalf("runs, files = %d, %d; want %d, %d", r, f, runs, files)
	}
}

func TestGetDefinitionsSharedAcrossCollections(t *testing.T) {
	w := newWorkspaceAt(t, t.TempDir())
	w.gen = newGenerator(t.TempDir())
	t.Cleanup(func() { _ = w.gen.Close(context.Background()) })
	ctx := context.Background()
	set := fileDescriptorSet(t, "grpcview/v1/service.proto")
	for _, id := range []string{"a", "b"} {
		createCollectionAt(t, w, ctx, id)
		uploadTo(t, w, ctx, id, set)
	}
	a := definitionsOf(t, w, ctx, "a")
	wantRuns(t, w.gen, 1, 2)
	if b := definitionsOf(t, w, ctx, "b"); !maps.Equal(a, b) {
		t.Fatal("collections sharing protos got different definitions")
	}
	wantRuns(t, w.gen, 1, 2)
}

// withExtraMessage adds a message to name's descriptor in set.
func withExtraMessage(t *testing.T, raw []byte, name string) []byte {
	t.Helper()
	var set descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(raw, &set); err != nil {
		t.Fatal(err)
	}
	for _, f := range set.GetFile() {
		if f.GetName() == name {
			f.MessageType = append(f.MessageType, &descriptorpb.DescriptorProto{Name: proto.String("CacheProbe")})
		}
	}
	out, err := proto.Marshal(&set)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Editing workspace.proto regenerates it and its importer service.proto, never echo.proto.
func TestGetDefinitionsRegeneratesOnlyAffected(t *testing.T) {
	w := newTestWorkspace(t)
	t.Cleanup(func() { _ = w.gen.Close(context.Background()) })
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	services := fileDescriptorSet(t, "grpcview/v1/service.proto")
	echo := fileDescriptorSet(t, "grpcview/echo/v1/echo.proto")

	uploadTo(t, w, ctx, testWorkspace, concatSets(t, services, echo))
	before := definitionsOf(t, w, ctx, testWorkspace)
	wantRuns(t, w.gen, 1, 3)

	uploadTo(t, w, ctx, testWorkspace, concatSets(t, withExtraMessage(t, services, "grpcview/v1/workspace.proto"), echo))
	after := definitionsOf(t, w, ctx, testWorkspace)
	wantRuns(t, w.gen, 2, 5)
	if !strings.Contains(after["grpcview/v1/workspace_pb.ts"], "CacheProbe") {
		t.Error("workspace_pb.ts not regenerated")
	}
	if after["grpcview/echo/v1/echo_pb.ts"] != before["grpcview/echo/v1/echo_pb.ts"] {
		t.Error("echo_pb.ts changed")
	}
}

// A fresh Workspace (a new process) serves from the disk cache without starting a pool; a
// corrupt entry is regenerated alone.
func TestGetDefinitionsDiskCache(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	w := newTestWorkspace(t)
	w.gen = newGenerator(dir)
	t.Cleanup(func() { _ = w.gen.Close(context.Background()) })
	ensureWorkspace(t, w, ctx)
	uploadTo(t, w, ctx, testWorkspace, concatSets(t, fileDescriptorSet(t, "grpcview/v1/service.proto"), fileDescriptorSet(t, "grpcview/echo/v1/echo.proto")))
	want := definitionsOf(t, w, ctx, testWorkspace)
	wantRuns(t, w.gen, 1, 3)

	restart := func() Workspace {
		fresh := Workspace{store: w.store, defs: newDefinitionsCache(), gen: newGenerator(dir)}
		t.Cleanup(func() { _ = fresh.gen.Close(context.Background()) })
		return fresh
	}
	fresh := restart()
	if got := definitionsOf(t, fresh, ctx, testWorkspace); !maps.Equal(got, want) {
		t.Fatal("disk-cached definitions differ")
	}
	wantRuns(t, fresh.gen, 0, 0)
	if fresh.gen.pool != nil {
		t.Error("pool started on an all-hit request")
	}

	entries, err := filepath.Glob(filepath.Join(dir, "*", "*.ts"))
	if err != nil || len(entries) != 3 {
		t.Fatalf("disk entries = %v, %v; want 3", entries, err)
	}
	if err := os.WriteFile(entries[0], []byte("corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	fresh = restart()
	if got := definitionsOf(t, fresh, ctx, testWorkspace); !maps.Equal(got, want) {
		t.Fatal("definitions differ after a corrupt entry")
	}
	wantRuns(t, fresh.gen, 1, 1)
}

func TestDefinitionsDigestCoversWinners(t *testing.T) {
	set := []byte("set")
	a, err := definitionsDigest(set, map[string]string{"a.proto": "x", "b.proto": "y"}, nil)
	if err != nil {
		t.Fatalf("definitionsDigest: %v", err)
	}
	for range 8 { // map order must not matter
		if b, _ := definitionsDigest(set, map[string]string{"b.proto": "y", "a.proto": "x"}, nil); b != a {
			t.Fatalf("digest depends on map order")
		}
	}
	if b, _ := definitionsDigest(set, map[string]string{"a.proto": "y", "b.proto": "x"}, nil); b == a {
		t.Error("digest unchanged when the winning sources swap")
	}
	if b, _ := definitionsDigest([]byte("other"), map[string]string{"a.proto": "x", "b.proto": "y"}, nil); b == a {
		t.Error("digest unchanged when the set changes")
	}
	winners := map[string]string{"a.proto": "x", "b.proto": "y"}
	o1, _ := definitionsDigest(set, winners, map[string]string{"a.proto": "one"})
	o2, _ := definitionsDigest(set, winners, map[string]string{"a.proto": "two"})
	if o1 == a || o1 == o2 {
		t.Error("digest does not cover the originals")
	}
}
