package workspace

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	echov1 "codeberg.org/ramilmsh/grpcview/grpcview/echo/v1"
	grpcviewstorev1 "codeberg.org/ramilmsh/grpcview/grpcview/store/v1"
	grpcviewv1 "codeberg.org/ramilmsh/grpcview/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/echo"
	"codeberg.org/ramilmsh/grpcview/service/store"
)

func startEchoServerWithoutReflection(t *testing.T) int {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	echov1.RegisterEchoServiceServer(srv, echo.NewServer())
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().(*net.TCPAddr).Port
}

func addEchoUpload(t *testing.T, w Workspace, ctx context.Context) {
	t.Helper()
	req := descriptorSetAddReq(fileDescriptorSet(t, "grpcview/echo/v1/echo.proto"))
	if _, err := w.AddDescriptorSource(ctx, connect.NewRequest(req)); err != nil {
		t.Fatalf("AddDescriptorSource (echo upload): %v", err)
	}
}

func TestInvokeTargetWithoutReflection(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	addEchoUpload(t, w, ctx)

	port := startEchoServerWithoutReflection(t)
	resp, err := w.Invoke(ctx, connect.NewRequest(&grpcviewv1.InvokeRequest{
		Spec: &grpcviewv1.InvokeSpec{
			Collection: testWorkspace,
			Service:    echoService,
			Method:     "Unary",
			Target:     &grpcviewv1.Server{Address: fmt.Sprintf("127.0.0.1:%d", port)},
		},
		Body: tsBody(`{"message":"staging"}`),
	}))
	if err != nil {
		t.Fatalf("Invoke against a reflection-less target: %v", err)
	}
	if code := resp.Msg.GetResponse().GetStatus().GetCode(); code != int32(codeOK) {
		t.Fatalf("status = %d (%s)", code, resp.Msg.GetResponse().GetStatus().GetMessage())
	}
	if got := decodeEchoMessage(t, resp.Msg.GetResponse().GetResponse()); got != "echo: staging" {
		t.Errorf("message = %q, want %q", got, "echo: staging")
	}
}

func TestStreamInvokeTargetWithoutReflection(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	addEchoUpload(t, w, ctx)

	port := startEchoServerWithoutReflection(t)
	frames, err := collectStream(ctx, w, echoStreamReq(port, "ServerStream", `{"message":"hi","count":3}`))
	if err != nil {
		t.Fatalf("streamInvoke against a reflection-less target: %v", err)
	}
	msgs, result := splitFrames(t, frames)
	if len(msgs) != 3 {
		t.Fatalf("want 3 message frames, got %d", len(msgs))
	}
	if code := result.GetStatus().GetCode(); code != int32(codeOK) {
		t.Fatalf("terminal status = %d (%q)", code, result.GetStatus().GetMessage())
	}
}

func TestInvokeWithoutDefinitionsIsRefused(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	port := startEchoServer(t)
	_, err := w.Invoke(ctx, connect.NewRequest(&grpcviewv1.InvokeRequest{
		Spec: &grpcviewv1.InvokeSpec{
			Collection: testWorkspace,
			Service:    echoService,
			Method:     "Unary",
			Target:     &grpcviewv1.Server{Address: fmt.Sprintf("127.0.0.1:%d", port)},
		},
		Body: tsBody(`{"message":"hi"}`),
	}))
	if err == nil {
		t.Fatal("want an error invoking a workspace with no descriptor sources, got nil")
	}
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition (%v)", got, err)
	}
}

func TestDefinitionsMemoizesLinking(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	addEchoUpload(t, w, ctx)

	first, err := w.definitions(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("definitions (first): %v", err)
	}
	second, err := w.definitions(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("definitions (second): %v", err)
	}
	if first.services[echoService] == nil {
		t.Fatalf("%s missing from the linked definitions", echoService)
	}
	if first.services[echoService] != second.services[echoService] {
		t.Error("a second definitions() re-linked an unchanged descriptor set; the memo did not hit")
	}

	grown := descriptorSetAddReq(fileDescriptorSet(t, "grpcview/v1/workspace.proto"))
	grown.FileName = "workspace.binpb"
	if _, err := w.AddDescriptorSource(ctx, connect.NewRequest(grown)); err != nil {
		t.Fatalf("AddDescriptorSource (second upload): %v", err)
	}
	third, err := w.definitions(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("definitions (after growth): %v", err)
	}
	if third.services[echoService] == nil {
		t.Fatalf("%s went missing after a second source was added", echoService)
	}
	if third.services[echoService] == first.services[echoService] {
		t.Error("definitions() reused a stale link after a write; the writer did not invalidate")
	}
}

func TestGetCarriesTheDerivedMergedView(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	addEchoUpload(t, w, ctx)

	got, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{Collection: testWorkspace}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	ws := got.Msg.GetCollection()
	if !hasService(ws.GetServices(), "EchoService") {
		t.Errorf("Get carried no EchoService: %v", ws.GetServices())
	}
	if len(ws.GetDescriptorSet()) == 0 {
		t.Error("Get carried no descriptor_set")
	}
	if len(ws.GetSources()) != 1 {
		t.Fatalf("sources = %d, want 1", len(ws.GetSources()))
	}
	resolved := ws.GetSources()[0].GetResolved()
	if resolved.GetFileCount() == 0 || len(resolved.GetWonServiceNames()) == 0 {
		t.Errorf("the source carries no derived summary: %v", resolved)
	}
}

func TestDefinitionsMemoDoesNoIO(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	addEchoUpload(t, w, ctx)

	if _, err := w.definitions(ctx, testWorkspace); err != nil {
		t.Fatalf("definitions (warming the memo): %v", err)
	}

	coll, err := w.store.Open(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	stateRoot := filepath.Dir(filepath.Dir(coll.State()))
	if err := os.RemoveAll(filepath.Join(stateRoot, "blobs")); err != nil {
		t.Fatalf("remove blobs: %v", err)
	}
	if err := os.RemoveAll(coll.State()); err != nil {
		t.Fatalf("remove collection state: %v", err)
	}

	defs, err := w.definitions(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("definitions with the whole descriptor state deleted: %v", err)
	}
	if defs.services[echoService] == nil {
		t.Errorf("%s missing from the memoized definitions", echoService)
	}
	got, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{Collection: testWorkspace}))
	if err != nil {
		t.Fatalf("Get with the whole descriptor state deleted: %v", err)
	}
	if !hasService(got.Msg.GetCollection().GetServices(), "EchoService") {
		t.Error("Get lost its services once the descriptor state was gone, so it re-read them")
	}
}

func dependentSet() *descriptorpb.FileDescriptorSet {
	return &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{{
		Name:       proto.String("dup/a.proto"),
		Package:    proto.String("dup"),
		Syntax:     proto.String("proto3"),
		Dependency: []string{"dup/common.proto"},
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("A"),
			Field: []*descriptorpb.FieldDescriptorProto{{
				Name:     proto.String("shared"),
				Number:   proto.Int32(1),
				Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
				TypeName: proto.String(".dup.Shared"),
			}},
		}},
	}}}
}

func commonSet() *descriptorpb.FileDescriptorSet {
	return &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{{
		Name:        proto.String("dup/common.proto"),
		Package:     proto.String("dup"),
		Syntax:      proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{{Name: proto.String("Unrelated")}},
	}}}
}

func TestGetSurvivesAnUnmergeableSourceList(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	coll, err := w.store.Open(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := coll.CreateRequest(ctx, nil, "Echo", "dup.Service", "Call"); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	const (
		oneID = "upload:one.binpb"
		twoID = "upload:two.binpb"
	)
	one := dependentSet()
	two := commonSet()
	if err := coll.PutDescriptorState(ctx, store.DescriptorState{
		Sources: []*grpcviewv1.DescriptorSource{
			{Id: oneID, Source: &grpcviewv1.DescriptorSource_Upload{Upload: &grpcviewv1.Upload{FileName: "one.binpb"}}},
			{Id: twoID, Source: &grpcviewv1.DescriptorSource_Upload{Upload: &grpcviewv1.Upload{FileName: "two.binpb"}}},
		},
		Resolves: map[string]*grpcviewstorev1.ResolvedSource{
			oneID: {Id: oneID, DescriptorSet: one},
			twoID: {Id: twoID, DescriptorSet: two},
		},
	}); err != nil {
		t.Fatalf("PutDescriptorState: %v", err)
	}

	got, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{Collection: testWorkspace}))
	if err != nil {
		t.Fatalf("Get with an unmergeable source list must still load the collection: %v", err)
	}
	ws := got.Msg.GetCollection()
	items := ws.GetItem().GetFolder().GetItems()
	if len(items) != 1 || items[0].GetName() != "Echo" {
		t.Errorf("the tree went missing: %v", items)
	}
	if len(ws.GetSources()) != 2 {
		t.Errorf("sources = %d, want both rows listed", len(ws.GetSources()))
	}
	if len(ws.GetServices()) != 0 {
		t.Errorf("services = %v, want none from a merge that failed", ws.GetServices())
	}
	for _, src := range ws.GetSources() {
		if msg := src.GetResolved().GetError(); !strings.Contains(msg, "cannot be merged") {
			t.Errorf("source %s error = %q, want the merge failure surfaced", src.GetId(), msg)
		}
	}

	if _, err := w.definitions(ctx, testWorkspace); err == nil {
		t.Error("want an error from definitions() when the sources cannot be merged")
	} else if code := connect.CodeOf(err); code != connect.CodeFailedPrecondition {
		t.Errorf("definitions() code = %v, want FailedPrecondition (%v)", code, err)
	}
}

func TestMemoRefusesAStaleDerivation(t *testing.T) {
	cache := newDefinitionsCache()
	const key = "requests"

	reader := cache.epoch(key)
	cache.invalidate(key)
	stale := &definitions{}
	cache.store(key, reader, stale)
	if got := cache.lookup(key); got != nil {
		t.Error("the memo kept a derivation that predates an invalidation it raced")
	}

	fresh := &definitions{}
	cache.store(key, cache.epoch(key), fresh)
	if got := cache.lookup(key); got != fresh {
		t.Error("a derivation begun after the invalidation was refused")
	}
}

func TestGetNoticesAHandEditedSourceList(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	addEchoUpload(t, w, ctx)

	if _, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{Collection: testWorkspace})); err != nil {
		t.Fatalf("Get: %v", err)
	}

	coll, err := w.store.Open(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	manifest := filepath.Join(coll.Root(), store.CollectionFileName)
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	edited := strings.Replace(string(data), `"sources"`, `"sourcesWasHere"`, 1)
	if edited == string(data) {
		t.Fatalf("the manifest has no sources to remove:\n%s", data)
	}
	if err := os.WriteFile(manifest, []byte(edited), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	got, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{Collection: testWorkspace}))
	if err != nil {
		t.Fatalf("Get after the edit: %v", err)
	}
	ws := got.Msg.GetCollection()
	if len(ws.GetSources()) != 0 {
		t.Errorf("sources = %v, want none after the edit", ws.GetSources())
	}
	if len(ws.GetServices()) != 0 {
		t.Errorf("services = %v, want none: they came from a source the manifest no longer lists", ws.GetServices())
	}
	if len(ws.GetDescriptorSet()) != 0 {
		t.Error("descriptor_set survived the removal of every source that produced it")
	}
}
