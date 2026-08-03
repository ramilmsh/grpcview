package workspace

import (
	"context"
	"fmt"
	"net"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/grpc"

	echov1 "codeberg.org/ramilmsh/grpcview/proto/echo/v1"
	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/echo"
)

// startEchoServerWithoutReflection has the shape of a real deployment: it serves its RPCs and
// nothing else — there is no reflection service to interrogate.
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
	req := descriptorSetAddReq(fileDescriptorSet(t, "proto/echo/v1/echo.proto"))
	if _, err := w.AddDescriptorSource(ctx, connect.NewRequest(req)); err != nil {
		t.Fatalf("AddDescriptorSource (echo upload): %v", err)
	}
}

// TestInvokeTargetWithoutReflection is the case invoke used to be unable to serve: the definitions
// come from an uploaded descriptor set, and the call goes to a server that serves no reflection.
// Reflecting on the target at invoke time would have failed here with Unimplemented.
func TestInvokeTargetWithoutReflection(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	addEchoUpload(t, w, ctx)

	port := startEchoServerWithoutReflection(t)
	resp, err := w.Invoke(ctx, connect.NewRequest(&grpcviewv1.InvokeRequest{
		Spec: &grpcviewv1.InvokeSpec{
			WorkspaceName: testWorkspace,
			Service:       echoService,
			Method:        "Unary",
			Target:        &grpcviewv1.Server{Address: fmt.Sprintf("127.0.0.1:%d", port)},
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

// TestStreamInvokeTargetWithoutReflection covers the same for the streaming path, which resolves
// its method through the very same seam.
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

// TestInvokeWithoutDefinitionsIsRefused pins the other side of the contract: invoke no longer
// discovers a method on its own, so an unresolved workspace is a FailedPrecondition that names
// the fix rather than a reflection error from the target.
func TestInvokeWithoutDefinitionsIsRefused(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	port := startEchoServer(t) // reflects, but nothing points at it
	_, err := w.Invoke(ctx, connect.NewRequest(&grpcviewv1.InvokeRequest{
		Spec: &grpcviewv1.InvokeSpec{
			WorkspaceName: testWorkspace,
			Service:       echoService,
			Method:        "Unary",
			Target:        &grpcviewv1.Server{Address: fmt.Sprintf("127.0.0.1:%d", port)},
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

// TestDefinitionsMemoizesLinking asserts the descriptor set is linked once and reused: identical
// bytes must hand back the very same descriptors, and changed bytes must not.
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

	// A second upload changes the merged bytes, so the memo must invalidate.
	grown := descriptorSetAddReq(fileDescriptorSet(t, "proto/grpcview/v1/workspace.proto"))
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
		t.Error("definitions() reused a stale link after the descriptor set changed")
	}
}
