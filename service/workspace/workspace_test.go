package workspace

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"testing"

	"connectrpc.com/connect"
	"github.com/jhump/protoreflect/desc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoregistry"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/scripting"
	"codeberg.org/ramilmsh/grpcview/service/store"
)

const testWorkspace = "default"

// tsBody wraps a JSON/JS object literal as a canonical TypeScript request-body module. Every
// request body is evaluated as a TS module on invoke now (the frontend migrates legacy JSON to
// this form before sending), so a test that wants to send a plain object writes tsBody(`{…}`)
// to drive the real path; the module's returned object is the literal.
func tsBody(obj string) string { return "export default () => (" + obj + ")" }

// startReflectionServer starts an in-process gRPC server on a loopback port with
// server reflection enabled. When withHealth is set it also registers the health
// service, so the server exposes a grpc.health.v1.Health service that a
// reflection-only peer does not — letting a remove test observe that the removed
// source's unique services actually disappear on re-resolution.
func startReflectionServer(t *testing.T, withHealth bool) int {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	if withHealth {
		grpc_health_v1.RegisterHealthServer(srv, health.NewServer())
	}
	reflection.Register(srv)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().(*net.TCPAddr).Port
}

func newTestWorkspace(t *testing.T) Workspace {
	t.Helper()
	return Workspace{store: store.New(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))}
}

// newTestWorkspaceWithEngine is newTestWorkspace plus a real scripting engine, needed by the
// tests that evaluate TS bodies/metadata (the engine compile is the expensive step, so a test
// builds one and reuses it across its subtests). The engine is torn down on cleanup.
func newTestWorkspaceWithEngine(t *testing.T) Workspace {
	t.Helper()
	eng, err := scripting.NewEngine(context.Background(), scriptingMaxPages)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close(context.Background()) })
	return Workspace{
		store:  store.New(t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil))),
		engine: eng,
	}
}

// createGenerator saves a GENERATOR script through the store, the way the other workspace
// tests set up requests (create then patch source).
func createGenerator(t *testing.T, w Workspace, ctx context.Context, name, source string) {
	t.Helper()
	coll, err := w.store.Open(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := coll.CreateScript(ctx, name, grpcviewv1.ScriptKind_SCRIPT_KIND_GENERATOR); err != nil {
		t.Fatalf("CreateScript %q: %v", name, err)
	}
	if err := coll.UpdateScript(ctx, name, store.ScriptPatch{Source: &source}); err != nil {
		t.Fatalf("UpdateScript %q: %v", name, err)
	}
}

func reflectionAddReq(port int) *grpcviewv1.AddDescriptorSourceRequest {
	return &grpcviewv1.AddDescriptorSourceRequest{
		WorkspaceName: testWorkspace,
		Source: &grpcviewv1.AddDescriptorSourceRequest_Reflection{
			Reflection: &grpcviewv1.Server{Address: fmt.Sprintf("127.0.0.1:%d", port)},
		},
	}
}

func removeReq(id string) *grpcviewv1.RemoveDescriptorSourceRequest {
	return &grpcviewv1.RemoveDescriptorSourceRequest{WorkspaceName: testWorkspace, Id: id}
}

// testUploadName is the descriptor-set upload's file name in tests, which is also
// its source identity ("upload:<file name>").
const testUploadName = "test.binpb"

func descriptorSetAddReq(set []byte) *grpcviewv1.AddDescriptorSourceRequest {
	return &grpcviewv1.AddDescriptorSourceRequest{
		WorkspaceName: testWorkspace,
		Source:        &grpcviewv1.AddDescriptorSourceRequest_DescriptorSet{DescriptorSet: set},
		FileName:      testUploadName,
	}
}

// fileDescriptorSet marshals a self-contained FileDescriptorSet (the named
// registered proto file plus its transitive dependencies) to wire bytes,
// standing in for what `protoc --include_imports` emits and the UI uploads.
func fileDescriptorSet(t *testing.T, path string) []byte {
	t.Helper()
	fd, err := protoregistry.GlobalFiles.FindFileByPath(path)
	if err != nil {
		t.Fatalf("find file descriptor %s: %v", path, err)
	}
	wrapped, err := desc.WrapFile(fd)
	if err != nil {
		t.Fatalf("wrap file descriptor %s: %v", path, err)
	}
	raw, err := proto.Marshal(desc.ToFileDescriptorSet(wrapped))
	if err != nil {
		t.Fatalf("marshal descriptor set: %v", err)
	}
	return raw
}

func ensureWorkspace(t *testing.T, w Workspace, ctx context.Context) {
	t.Helper()
	if _, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{WorkspaceName: testWorkspace})); err != nil {
		t.Fatalf("Get (create): %v", err)
	}
}

func hasService(services []*grpcviewv1.Service, name string) bool {
	for _, s := range services {
		if s.GetName() == name {
			return true
		}
	}
	return false
}

// TestRemoveDescriptorSourceReResolves adds two reflection sources and removes
// one, asserting the flat services list is re-resolved from the source that
// remains (the removed source's unique service disappears), the removal
// persists, and clearing the last source empties services.
func TestRemoveDescriptorSourceReResolves(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	portA := startReflectionServer(t, true)  // reflection + Health
	portB := startReflectionServer(t, false) // reflection only

	if _, err := w.AddDescriptorSource(ctx, connect.NewRequest(reflectionAddReq(portA))); err != nil {
		t.Fatalf("AddDescriptorSource A: %v", err)
	}
	addB, err := w.AddDescriptorSource(ctx, connect.NewRequest(reflectionAddReq(portB)))
	if err != nil {
		t.Fatalf("AddDescriptorSource B: %v", err)
	}
	ws := addB.Msg.GetWorkspace()
	if len(ws.GetSources()) != 2 {
		t.Fatalf("want 2 sources, got %d", len(ws.GetSources()))
	}
	if !hasService(ws.GetServices(), "Health") {
		t.Fatalf("Health service missing after adding source A")
	}

	// Remove source A. The merged view must be re-derived from B alone, so Health
	// (which only A exposed) disappears while B's reflection services remain.
	idA := ws.GetSources()[0].GetId()
	remResp, err := w.RemoveDescriptorSource(ctx, connect.NewRequest(removeReq(idA)))
	if err != nil {
		t.Fatalf("RemoveDescriptorSource: %v", err)
	}
	ws = remResp.Msg.GetWorkspace()
	if len(ws.GetSources()) != 1 {
		t.Fatalf("want 1 source after remove, got %d", len(ws.GetSources()))
	}
	if hasService(ws.GetServices(), "Health") {
		t.Fatalf("Health should be gone after removing source A")
	}
	if len(ws.GetServices()) == 0 {
		t.Fatalf("expected B's reflection services to remain after remove")
	}

	// The removal (and re-resolved services) must survive a reload.
	reloaded, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{WorkspaceName: testWorkspace}))
	if err != nil {
		t.Fatalf("Get after remove: %v", err)
	}
	if got := reloaded.Msg.GetWorkspace(); len(got.GetSources()) != 1 || hasService(got.GetServices(), "Health") {
		t.Fatalf("removal not persisted: %d sources, Health present=%v",
			len(got.GetSources()), hasService(got.GetServices(), "Health"))
	}

	// Removing the last source clears services entirely.
	idB := ws.GetSources()[0].GetId()
	if _, err := w.RemoveDescriptorSource(ctx, connect.NewRequest(removeReq(idB))); err != nil {
		t.Fatalf("RemoveDescriptorSource (last): %v", err)
	}
	final, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{WorkspaceName: testWorkspace}))
	if err != nil {
		t.Fatalf("Get final: %v", err)
	}
	if got := final.Msg.GetWorkspace(); len(got.GetSources()) != 0 || len(got.GetServices()) != 0 {
		t.Fatalf("want empty sources+services, got %d sources / %d services",
			len(got.GetSources()), len(got.GetServices()))
	}
}

// TestAddDescriptorSetSource uploads a descriptor set, asserting its services load
// and that both the source (identified by its file name) and the resolved services
// survive a reload. The descriptor bytes stay on disk — the wire form carries only
// the file name — so the assertion is on the upload's identity, not its bytes.
func TestAddDescriptorSetSource(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	set := fileDescriptorSet(t, "grpc/health/v1/health.proto")
	resp, err := w.AddDescriptorSource(ctx, connect.NewRequest(descriptorSetAddReq(set)))
	if err != nil {
		t.Fatalf("AddDescriptorSource (descriptor set): %v", err)
	}
	ws := resp.Msg.GetWorkspace()
	if len(ws.GetSources()) != 1 {
		t.Fatalf("want 1 source, got %d", len(ws.GetSources()))
	}
	if got := ws.GetSources()[0].GetUpload().GetFileName(); got != testUploadName {
		t.Fatalf("stored source is not the upload: %+v", ws.GetSources()[0])
	}
	if !hasService(ws.GetServices(), "Health") {
		t.Fatalf("Health service missing after adding descriptor-set source")
	}

	reloaded, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{WorkspaceName: testWorkspace}))
	if err != nil {
		t.Fatalf("Get after add: %v", err)
	}
	got := reloaded.Msg.GetWorkspace()
	if len(got.GetSources()) != 1 || got.GetSources()[0].GetUpload().GetFileName() != testUploadName {
		t.Fatalf("descriptor-set source not persisted: %+v", got.GetSources())
	}
	if !hasService(got.GetServices(), "Health") {
		t.Fatalf("Health service missing after reload")
	}
}

// TestRemoveReResolvesDescriptorSetSource combines a reflection source with a
// descriptor-set source, then removes the reflection source. The merged view must
// be re-derived from the remaining upload's cached resolve, so its services survive
// while the removed reflection source's services disappear.
func TestRemoveReResolvesDescriptorSetSource(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	// Source 0: reflection-only server (exposes ServerReflection, not Health).
	// Source 1: a descriptor set that defines grpc.health.v1.Health.
	port := startReflectionServer(t, false)
	if _, err := w.AddDescriptorSource(ctx, connect.NewRequest(reflectionAddReq(port))); err != nil {
		t.Fatalf("AddDescriptorSource (reflection): %v", err)
	}
	set := fileDescriptorSet(t, "grpc/health/v1/health.proto")
	addResp, err := w.AddDescriptorSource(ctx, connect.NewRequest(descriptorSetAddReq(set)))
	if err != nil {
		t.Fatalf("AddDescriptorSource (descriptor set): %v", err)
	}
	ws := addResp.Msg.GetWorkspace()
	if len(ws.GetSources()) != 2 {
		t.Fatalf("want 2 sources, got %d", len(ws.GetSources()))
	}
	if !hasService(ws.GetServices(), "Health") || !hasService(ws.GetServices(), "ServerReflection") {
		t.Fatalf("want both Health (descriptor set) and ServerReflection (reflection): %v", ws.GetServices())
	}

	// Remove the reflection source. The remaining upload's cached resolve must
	// carry the merge, so Health survives while ServerReflection is gone.
	remResp, err := w.RemoveDescriptorSource(ctx, connect.NewRequest(removeReq(ws.GetSources()[0].GetId())))
	if err != nil {
		t.Fatalf("RemoveDescriptorSource: %v", err)
	}
	ws = remResp.Msg.GetWorkspace()
	if len(ws.GetSources()) != 1 || ws.GetSources()[0].GetUpload().GetFileName() != testUploadName {
		t.Fatalf("want 1 descriptor-set source after remove, got %+v", ws.GetSources())
	}
	if !hasService(ws.GetServices(), "Health") {
		t.Fatalf("Health should survive: descriptor-set source must re-resolve on remove")
	}
	if hasService(ws.GetServices(), "ServerReflection") {
		t.Fatalf("ServerReflection should be gone after removing the reflection source")
	}

	// The re-resolved state must persist.
	final, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{WorkspaceName: testWorkspace}))
	if err != nil {
		t.Fatalf("Get after remove: %v", err)
	}
	if got := final.Msg.GetWorkspace(); len(got.GetSources()) != 1 || !hasService(got.GetServices(), "Health") {
		t.Fatalf("re-resolved descriptor-set state not persisted: %d sources, Health present=%v",
			len(got.GetSources()), hasService(got.GetServices(), "Health"))
	}
}

// TestRemoveDescriptorSourceUnknownID asserts an id that names no configured
// source is rejected with NotFound rather than mutating state.
func TestRemoveDescriptorSourceUnknownID(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	for _, id := range []string{"", "reflection:nope:1", "upload:missing.binpb"} {
		_, err := w.RemoveDescriptorSource(ctx, connect.NewRequest(removeReq(id)))
		if err == nil {
			t.Fatalf("id %q: want error, got nil", id)
		}
		if got := connect.CodeOf(err); got != connect.CodeNotFound {
			t.Fatalf("id %q: want NotFound, got %v (%v)", id, got, err)
		}
	}
}

// folderNamed returns the named top-level folder from a Workspace snapshot,
// failing the test if it is missing or not a folder — the lookup
// TestUpdateFolderRPC needs to inspect the RPC's response/reload shape.
func folderNamed(t *testing.T, ws *grpcviewv1.Workspace, name string) *grpcviewv1.Folder {
	t.Helper()
	for _, it := range ws.GetItem().GetFolder().GetItems() {
		if it.GetName() == name {
			if it.GetFolder() == nil {
				t.Fatalf("item %q is not a folder", name)
			}
			return it.GetFolder()
		}
	}
	t.Fatalf("folder %q not found", name)
	return nil
}

// TestUpdateFolderRPC covers gv-features-plan.md Feature 1 Phase 4: the
// UpdateFolder RPC patches a folder's draft_metadata_script, the change is
// visible on a fresh Get (persisted, not just echoed in the mutate response), an
// empty-but-present value clears it (mirroring UpdateRequest's
// DraftMetadataScript semantics), and an unknown item name/a non-folder item
// surface the same Connect codes UpdateRequest's mirror checks do.
func TestUpdateFolderRPC(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	if _, err := w.CreateFolder(ctx, connect.NewRequest(&grpcviewv1.CreateFolderRequest{
		WorkspaceName: testWorkspace,
		ItemName:      "Users",
	})); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	if _, err := w.CreateRequest(ctx, connect.NewRequest(&grpcviewv1.CreateRequestRequest{
		WorkspaceName: testWorkspace,
		ItemName:      "Leaf",
		Service:       "s",
		Method:        "m",
	})); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	script := "export default () => ({ ...gv.metadata.inherit(), team: ['users'] })"
	updResp, err := w.UpdateFolder(ctx, connect.NewRequest(&grpcviewv1.UpdateFolderRequest{
		WorkspaceName:       testWorkspace,
		ItemName:            "Users",
		DraftMetadataScript: &script,
	}))
	if err != nil {
		t.Fatalf("UpdateFolder: %v", err)
	}
	if got := folderNamed(t, updResp.Msg.GetWorkspace(), "Users").GetDraftMetadataScript(); got != script {
		t.Fatalf("folder script in mutate response = %q, want %q", got, script)
	}

	// The change is visible on a fresh Get (persisted, not just the mutate response).
	getResp, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{WorkspaceName: testWorkspace}))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := folderNamed(t, getResp.Msg.GetWorkspace(), "Users").GetDraftMetadataScript(); got != script {
		t.Fatalf("folder script after Get = %q, want %q", got, script)
	}

	// An empty-but-present value clears it.
	empty := ""
	if _, err := w.UpdateFolder(ctx, connect.NewRequest(&grpcviewv1.UpdateFolderRequest{
		WorkspaceName:       testWorkspace,
		ItemName:            "Users",
		DraftMetadataScript: &empty,
	})); err != nil {
		t.Fatalf("UpdateFolder clear: %v", err)
	}
	cleared, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{WorkspaceName: testWorkspace}))
	if err != nil {
		t.Fatalf("Get after clear: %v", err)
	}
	if got := folderNamed(t, cleared.Msg.GetWorkspace(), "Users").GetDraftMetadataScript(); got != "" {
		t.Fatalf("folder script after clear = %q, want empty", got)
	}

	// An unknown item name surfaces NotFound (store.ErrItemNotFound mapped by
	// toConnectError).
	if _, err := w.UpdateFolder(ctx, connect.NewRequest(&grpcviewv1.UpdateFolderRequest{
		WorkspaceName:       testWorkspace,
		ItemName:            "Ghost",
		DraftMetadataScript: &script,
	})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("UpdateFolder missing item = %v, want NotFound", err)
	}

	// An item that isn't a folder (a request) surfaces FailedPrecondition
	// (store.ErrNotAFolder mapped by toConnectError).
	if _, err := w.UpdateFolder(ctx, connect.NewRequest(&grpcviewv1.UpdateFolderRequest{
		WorkspaceName:       testWorkspace,
		ItemName:            "Leaf",
		DraftMetadataScript: &script,
	})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("UpdateFolder on a request = %v, want FailedPrecondition", err)
	}
}
