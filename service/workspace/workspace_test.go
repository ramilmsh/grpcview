package workspace

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/store"
)

const testWorkspace = "default"

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
