package workspace

import (
	"context"
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
			Reflection: &grpcviewv1.Server{Host: "127.0.0.1", Port: int32(port)},
		},
	}
}

func removeReq(index int32) *grpcviewv1.RemoveDescriptorSourceRequest {
	return &grpcviewv1.RemoveDescriptorSourceRequest{WorkspaceName: testWorkspace, Index: index}
}

func descriptorSetAddReq(set []byte) *grpcviewv1.AddDescriptorSourceRequest {
	return &grpcviewv1.AddDescriptorSourceRequest{
		WorkspaceName: testWorkspace,
		Source:        &grpcviewv1.AddDescriptorSourceRequest_DescriptorSet{DescriptorSet: set},
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

	// Remove source A (index 0). Services must re-resolve from B alone, so
	// Health (which only A exposed) disappears while B's reflection services
	// remain.
	remResp, err := w.RemoveDescriptorSource(ctx, connect.NewRequest(removeReq(0)))
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
	if _, err := w.RemoveDescriptorSource(ctx, connect.NewRequest(removeReq(0))); err != nil {
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

// TestAddDescriptorSetSource uploads a descriptor set, asserting its services
// load and that both the descriptor-set bytes (round-tripped through the store's
// typed FileDescriptorSet) and the resolved services survive a reload.
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
	if ws.GetSources()[0].GetDescriptorSet() == nil {
		t.Fatalf("stored source is not a descriptor set: %+v", ws.GetSources()[0])
	}
	if !hasService(ws.GetServices(), "Health") {
		t.Fatalf("Health service missing after adding descriptor-set source")
	}

	reloaded, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{WorkspaceName: testWorkspace}))
	if err != nil {
		t.Fatalf("Get after add: %v", err)
	}
	got := reloaded.Msg.GetWorkspace()
	if len(got.GetSources()) != 1 || got.GetSources()[0].GetDescriptorSet() == nil {
		t.Fatalf("descriptor-set source not persisted: %+v", got.GetSources())
	}
	if !hasService(got.GetServices(), "Health") {
		t.Fatalf("Health service missing after reload")
	}
}

// TestRemoveReResolvesDescriptorSetSource combines a reflection source with a
// descriptor-set source, then removes the reflection source. Re-resolution must
// walk the remaining descriptor-set source (the N2c branch of
// resolveServicesFromSources) so its services survive while the removed
// reflection source's services disappear.
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

	// Remove the reflection source (index 0). The remaining descriptor-set
	// source must re-resolve, so Health survives while ServerReflection is gone.
	remResp, err := w.RemoveDescriptorSource(ctx, connect.NewRequest(removeReq(0)))
	if err != nil {
		t.Fatalf("RemoveDescriptorSource: %v", err)
	}
	ws = remResp.Msg.GetWorkspace()
	if len(ws.GetSources()) != 1 || ws.GetSources()[0].GetDescriptorSet() == nil {
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

// TestRemoveDescriptorSourceOutOfRange asserts an index outside [0,len) is
// rejected with InvalidArgument rather than panicking or mutating state.
func TestRemoveDescriptorSourceOutOfRange(t *testing.T) {
	w := newTestWorkspace(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	for _, index := range []int32{-1, 0, 5} {
		_, err := w.RemoveDescriptorSource(ctx, connect.NewRequest(removeReq(index)))
		if err == nil {
			t.Fatalf("index %d: want error, got nil", index)
		}
		if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
			t.Fatalf("index %d: want InvalidArgument, got %v (%v)", index, got, err)
		}
	}
}
