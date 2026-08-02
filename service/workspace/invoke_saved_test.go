package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	echov1 "codeberg.org/ramilmsh/grpcview/proto/echo/v1"
	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/store"
)

// invoke_saved_test.go covers cli-generator-exploration.md C1a: the addressed invoke
// (InvokeSaved / InvokeSavedStreaming), which resolves the SAVED body/metadata/middleware/target
// server-side from a display-name path instead of taking the caller's editor buffers.

// saveRequest saves a request named name inside parent, pointed at addr (an explicit per-request
// target, so resolveMethod can dial+reflect it with no workspace reflection source configured)
// with method and a TS body. It is the general form of gvinvoke_test.go's gvTarget: an arbitrary
// method (so a streaming saved request can be built) and a raw address (so a DEAD one can be).
func saveRequest(t *testing.T, w Workspace, ctx context.Context, parent []string, name, method, body, addr string) {
	t.Helper()
	coll, err := w.store.Open(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := coll.CreateRequest(ctx, parent, name, echoService, method); err != nil {
		t.Fatalf("CreateRequest %q: %v", name, err)
	}
	if err := coll.UpdateRequest(ctx, parent, name, store.RequestPatch{
		DraftBody: &body, SetTarget: true, Target: &grpcviewv1.Server{Address: addr},
	}); err != nil {
		t.Fatalf("UpdateRequest %q: %v", name, err)
	}
}

// loopback renders a loopback address for a port the tests stood a server up on.
func loopback(port int) string { return fmt.Sprintf("127.0.0.1:%d", port) }

// deadPort returns a loopback port with nothing listening on it: a listener is opened to have
// the OS pick a free port, then closed immediately. Pointing a dry run here is what proves the
// dry run never dials — a real call to this address fails.
func deadPort(t *testing.T) int {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := lis.Addr().(*net.TCPAddr).Port
	if err := lis.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return port
}

// failingEchoServer serves the reflectable EchoService but fails every Unary call with a non-OK
// gRPC status — the target behavior the (response, nil) invariant below is about.
type failingEchoServer struct {
	echov1.UnimplementedEchoServiceServer
}

func (failingEchoServer) Unary(context.Context, *echov1.EchoRequest) (*echov1.EchoResponse, error) {
	return nil, status.Error(codes.PermissionDenied, "denied by test")
}

// startFailingEchoServer stands up an in-process EchoService (plus reflection, which the invoke
// path needs to resolve the method) whose Unary always returns PermissionDenied.
func startFailingEchoServer(t *testing.T) int {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	echov1.RegisterEchoServiceServer(srv, failingEchoServer{})
	reflection.Register(srv)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().(*net.TCPAddr).Port
}

// invokeSaved calls the RPC with the given request and returns the response message.
func invokeSaved(t *testing.T, w Workspace, ctx context.Context, msg *grpcviewv1.InvokeSavedRequest) (*grpcviewv1.InvokeSavedResponse, error) {
	t.Helper()
	resp, err := w.InvokeSaved(ctx, connect.NewRequest(msg))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// decodeEchoMessage decodes an echo response payload's `message` field.
func decodeEchoMessage(t *testing.T, payload []byte) string {
	t.Helper()
	var got struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("decode echo response %s: %v", payload, err)
	}
	return got.Message
}

// paramsStruct builds the params Struct a caller sends on the wire.
func paramsStruct(t *testing.T, m map[string]any) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(m)
	if err != nil {
		t.Fatalf("NewStruct: %v", err)
	}
	return s
}

// TestInvokeSavedRunsTheSavedRequest is the headline case: nothing about the request is sent —
// only its path — and the run uses the request's OWN saved body, method and target. The echo
// server's "echo: " prefix on the saved body's message proves the saved body reached a real
// call.
func TestInvokeSavedRunsTheSavedRequest(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	port := startEchoServer(t)

	saveRequest(t, w, ctx, nil, "Echo", "Unary",
		`export default () => ({ message: "saved-body" })`, loopback(port))

	got, err := invokeSaved(t, w, ctx, &grpcviewv1.InvokeSavedRequest{
		WorkspaceName: testWorkspace,
		ItemName:      "Echo",
	})
	if err != nil {
		t.Fatalf("InvokeSaved: %v", err)
	}
	if code := got.GetResponse().GetStatus().GetCode(); code != int32(codeOK) {
		t.Fatalf("status = %d (%s), want OK", code, got.GetResponse().GetStatus().GetMessage())
	}
	if msg := decodeEchoMessage(t, got.GetResponse().GetResponse()); msg != "echo: saved-body" {
		t.Fatalf("echoed message = %q, want %q", msg, "echo: saved-body")
	}
	if got.GetResolved() != nil {
		t.Fatalf("resolved is set on a real run: %v (it is dry-run only)", got.GetResolved())
	}
}

// TestInvokeSavedNestedPath: the address is a parent-folder display-name path plus an item name,
// the same split gv.invoke's paths produce — a request inside a folder is reachable.
func TestInvokeSavedNestedPath(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	port := startEchoServer(t)

	coll, err := w.store.Open(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := coll.CreateFolder(ctx, nil, "Users"); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	saveRequest(t, w, ctx, []string{"Users"}, "GetUser", "Unary",
		`export default () => ({ message: "nested" })`, loopback(port))

	got, err := invokeSaved(t, w, ctx, &grpcviewv1.InvokeSavedRequest{
		WorkspaceName: testWorkspace,
		Path:          []string{"Users"},
		ItemName:      "GetUser",
	})
	if err != nil {
		t.Fatalf("InvokeSaved: %v", err)
	}
	if msg := decodeEchoMessage(t, got.GetResponse().GetResponse()); msg != "echo: nested" {
		t.Fatalf("echoed message = %q, want %q", msg, "echo: nested")
	}
}

// TestInvokeSavedParamsReachTheBody: params is the reason this RPC exists as more than a
// convenience — InvokeRequest has no such field, so before C1a only gv.invoke could
// parameterize a run. The assertion is on a field the SERVER echoed back, so the value must have
// travelled through the QuickJS body evaluation into a real call. A generator in the mix proves
// params and generator composition coexist.
func TestInvokeSavedParamsReachTheBody(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	port := startEchoServer(t)

	createGenerator(t, w, ctx, "prefix", `export default () => "p:"`)
	saveRequest(t, w, ctx, nil, "Echo", "Unary",
		`export default () => ({ message: prefix() + gv.request.params.who })`, loopback(port))

	got, err := invokeSaved(t, w, ctx, &grpcviewv1.InvokeSavedRequest{
		WorkspaceName: testWorkspace,
		ItemName:      "Echo",
		Params:        paramsStruct(t, map[string]any{"who": "ada"}),
	})
	if err != nil {
		t.Fatalf("InvokeSaved: %v", err)
	}
	if msg := decodeEchoMessage(t, got.GetResponse().GetResponse()); msg != "echo: p:ada" {
		t.Fatalf("echoed message = %q, want %q (params did not reach the body)", msg, "echo: p:ada")
	}
}

// TestInvokeSavedOverrides: target and messages override the saved request for ONE run. The
// saved request points at a dead port with a body of its own, so an echoed reply can only mean
// both overrides took effect.
func TestInvokeSavedOverrides(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	port := startEchoServer(t)

	saveRequest(t, w, ctx, nil, "Echo", "Unary",
		`export default () => ({ message: "saved" })`, loopback(deadPort(t)))

	got, err := invokeSaved(t, w, ctx, &grpcviewv1.InvokeSavedRequest{
		WorkspaceName: testWorkspace,
		ItemName:      "Echo",
		Target:        &grpcviewv1.Server{Address: loopback(port)},
		Messages:      []string{tsBody(`{"message":"overridden"}`)},
	})
	if err != nil {
		t.Fatalf("InvokeSaved: %v", err)
	}
	if msg := decodeEchoMessage(t, got.GetResponse().GetResponse()); msg != "echo: overridden" {
		t.Fatalf("echoed message = %q, want %q", msg, "echo: overridden")
	}
}

// TestInvokeSavedAddressErrors maps the store's addressing sentinels onto Connect codes, which
// nothing did before C1a (both ResolveRequest sentinels used to reach the caller unwrapped, so
// connect.CodeOf read Unknown). The CLI turns a Connect error into exit 2 and needs the code to
// tell "no such request" from "that path is a folder".
func TestInvokeSavedAddressErrors(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	coll, err := w.store.Open(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := coll.CreateFolder(ctx, nil, "Folder"); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}

	cases := []struct {
		name     string
		itemName string
		want     connect.Code
	}{
		{"unknown item", "NoSuchRequest", connect.CodeNotFound},
		{"path names a folder", "Folder", connect.CodeFailedPrecondition},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := invokeSaved(t, w, ctx, &grpcviewv1.InvokeSavedRequest{
				WorkspaceName: testWorkspace,
				ItemName:      tc.itemName,
			})
			if err == nil {
				t.Fatalf("want an error for item %q, got nil", tc.itemName)
			}
			if code := connect.CodeOf(err); code != tc.want {
				t.Fatalf("connect.CodeOf(%v) = %v, want %v", err, code, tc.want)
			}
		})
	}
}

// TestInvokeSavedTargetStatusIsInThePayload is the invariant the CLI's exit-code split depends
// on (D9: a Connect error → exit 2, a non-OK status in the payload → exit 1). A target that
// rejects the call is NOT a failure of this RPC: the call reached the server and answered, so
// the status rides INSIDE the response and the RPC error is nil. Getting this wrong collapses
// "the target said no" into "grpcview broke".
func TestInvokeSavedTargetStatusIsInThePayload(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	port := startFailingEchoServer(t)

	saveRequest(t, w, ctx, nil, "Denied", "Unary",
		`export default () => ({ message: "hi" })`, loopback(port))

	got, err := invokeSaved(t, w, ctx, &grpcviewv1.InvokeSavedRequest{
		WorkspaceName: testWorkspace,
		ItemName:      "Denied",
	})
	if err != nil {
		t.Fatalf("InvokeSaved returned a Connect error (%v) for a non-OK gRPC status; it must "+
			"return (response, nil) with the status inside — the CLI's exit 1 vs. exit 2 split", err)
	}
	st := got.GetResponse().GetStatus()
	if st.GetCode() != int32(codes.PermissionDenied) {
		t.Fatalf("status code = %d, want %d (PermissionDenied)", st.GetCode(), codes.PermissionDenied)
	}
	if st.GetMessage() != "denied by test" {
		t.Fatalf("status message = %q, want %q", st.GetMessage(), "denied by test")
	}
}

// TestInvokeSavedRecordHistory: record_history defaults to TRUE (D7 — an addressed run is a real
// user-initiated one, unlike gv.invoke's fan-out), and an explicit false opts out. Both are
// asserted against the same freshly saved request so the counts cannot come from anywhere else.
func TestInvokeSavedRecordHistory(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	port := startEchoServer(t)

	saveRequest(t, w, ctx, nil, "Echo", "Unary",
		`export default () => ({ message: "hi" })`, loopback(port))

	history := func(t *testing.T) []*grpcviewv1.History {
		t.Helper()
		coll, err := w.store.Open(ctx, testWorkspace)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		ws, err := coll.Load(ctx)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		for _, it := range ws.GetItem().GetFolder().GetItems() {
			if it.GetName() == "Echo" {
				return it.GetRequest().GetHistory()
			}
		}
		t.Fatalf("request Echo not found")
		return nil
	}

	off := false
	if _, err := invokeSaved(t, w, ctx, &grpcviewv1.InvokeSavedRequest{
		WorkspaceName: testWorkspace,
		ItemName:      "Echo",
		RecordHistory: &off,
	}); err != nil {
		t.Fatalf("InvokeSaved (record_history=false): %v", err)
	}
	if n := len(history(t)); n != 0 {
		t.Fatalf("history len = %d after record_history=false, want 0", n)
	}

	if _, err := invokeSaved(t, w, ctx, &grpcviewv1.InvokeSavedRequest{
		WorkspaceName: testWorkspace,
		ItemName:      "Echo",
	}); err != nil {
		t.Fatalf("InvokeSaved (default): %v", err)
	}
	if n := len(history(t)); n != 1 {
		t.Fatalf("history len = %d after the default run, want exactly 1 (record_history "+
			"defaults to true)", n)
	}
}

// TestInvokeSavedDryRun: a dry run reports the request as it would have been SENT — bodies
// evaluated (the stored source is TypeScript; only the server can produce the JSON), metadata
// evaluated, middleware applied — and dials nothing. The target is a DEAD port, so any dial
// would fail the RPC: that is what makes "never dials" an assertion rather than a claim.
func TestInvokeSavedDryRun(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	dead := loopback(deadPort(t))

	saveRequest(t, w, ctx, nil, "Echo", "Unary",
		`export default () => ({ message: "dry-" + gv.request.params.n })`, dead)
	coll, err := w.store.Open(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	mdScript := `export default () => ({ "x-token": ["t-" + gv.request.params.n] })`
	if err := coll.UpdateRequest(ctx, nil, "Echo", store.RequestPatch{DraftMetadataScript: &mdScript}); err != nil {
		t.Fatalf("UpdateRequest: %v", err)
	}

	got, err := invokeSaved(t, w, ctx, &grpcviewv1.InvokeSavedRequest{
		WorkspaceName: testWorkspace,
		ItemName:      "Echo",
		Params:        paramsStruct(t, map[string]any{"n": "7"}),
		DryRun:        true,
	})
	if err != nil {
		t.Fatalf("InvokeSaved (dry_run): %v (a dry run must not dial — the target is dead)", err)
	}
	if got.GetResponse() != nil {
		t.Fatalf("response is set on a dry run: %v (nothing was sent)", got.GetResponse())
	}
	resolved := got.GetResolved()
	if resolved == nil {
		t.Fatalf("resolved is unset on a dry run")
	}
	if resolved.GetService() != echoService || resolved.GetMethod() != "Unary" {
		t.Fatalf("resolved service/method = %q/%q, want %q/%q",
			resolved.GetService(), resolved.GetMethod(), echoService, "Unary")
	}
	if addr := resolved.GetTarget().GetAddress(); addr != dead {
		t.Fatalf("resolved target = %q, want %q", addr, dead)
	}
	if msgs := resolved.GetMessages(); len(msgs) != 1 || decodeEchoMessage(t, []byte(msgs[0])) != "dry-7" {
		t.Fatalf("resolved messages = %q, want one evaluated body carrying message=%q", msgs, "dry-7")
	}
	if vals := valueToStrings(resolved.GetMetadata().GetFields()["x-token"]); len(vals) != 1 || vals[0] != "t-7" {
		t.Fatalf("resolved metadata x-token = %v, want [t-7]", vals)
	}
}

// TestInvokeSavedStreamingSendsEveryMessage: a client-streaming saved request receives every
// message of the per-run override, in order, composed up-front (D13 — the existing convention,
// which the CLI inherits rather than replacing). The echo server's reply names the count and the
// messages it actually received, so order and completeness are the server's testimony.
func TestInvokeSavedStreamingSendsEveryMessage(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	port := startEchoServer(t)

	saveRequest(t, w, ctx, nil, "Upload", "ClientStream",
		`export default () => ({ message: "saved" })`, loopback(port))

	var frames []*grpcviewv1.InvokeStreamResponse
	send := func(resp *grpcviewv1.InvokeStreamResponse) error {
		frames = append(frames, resp)
		return nil
	}
	err := w.invokeSavedStream(ctx, &grpcviewv1.InvokeSavedRequest{
		WorkspaceName: testWorkspace,
		ItemName:      "Upload",
		Messages: []string{
			tsBody(`{"message":"a"}`),
			tsBody(`{"message":"b"}`),
			tsBody(`{"message":"c"}`),
		},
	}, send)
	if err != nil {
		t.Fatalf("invokeSavedStream: %v", err)
	}
	msgs, result := splitFrames(t, frames)
	if code := result.GetStatus().GetCode(); code != int32(codeOK) {
		t.Fatalf("terminal status = %d (%s), want OK", code, result.GetStatus().GetMessage())
	}
	if len(msgs) != 1 {
		t.Fatalf("message frames = %d, want 1 (client-streaming answers once)", len(msgs))
	}
	if got := decodeEchoMessage(t, msgs[0]); got != "received 3 messages: a, b, c" {
		t.Fatalf("echoed message = %q, want %q (all three messages must be sent, in order)",
			got, "received 3 messages: a, b, c")
	}
}

// TestInvokeSavedStreamingParamsReachTheBody: the streaming form threads params through the same
// pre-send evaluation the unary one does (streamInvoke used to hard-code nil there).
func TestInvokeSavedStreamingParamsReachTheBody(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	port := startEchoServer(t)

	saveRequest(t, w, ctx, nil, "Stream", "ServerStream",
		`export default () => ({ message: "s-" + gv.request.params.n, count: 2 })`, loopback(port))

	var frames []*grpcviewv1.InvokeStreamResponse
	send := func(resp *grpcviewv1.InvokeStreamResponse) error {
		frames = append(frames, resp)
		return nil
	}
	if err := w.invokeSavedStream(ctx, &grpcviewv1.InvokeSavedRequest{
		WorkspaceName: testWorkspace,
		ItemName:      "Stream",
		Params:        paramsStruct(t, map[string]any{"n": "9"}),
	}, send); err != nil {
		t.Fatalf("invokeSavedStream: %v", err)
	}
	msgs, result := splitFrames(t, frames)
	if code := result.GetStatus().GetCode(); code != int32(codeOK) {
		t.Fatalf("terminal status = %d (%s), want OK", code, result.GetStatus().GetMessage())
	}
	if len(msgs) != 2 {
		t.Fatalf("message frames = %d, want 2 (the saved body asked for count 2)", len(msgs))
	}
	if got := decodeEchoMessage(t, msgs[0]); got != "echo #0: s-9" {
		t.Fatalf("first echoed message = %q, want %q (params did not reach the body)", got, "echo #0: s-9")
	}
}

// TestInvokeSavedStreamingRejectsDryRun: the streaming response has no frame that could carry a
// resolved request, and the unary form dry-runs a saved request of any streaming kind without
// dialing — so this is an explicit InvalidArgument rather than a silently ignored flag.
func TestInvokeSavedStreamingRejectsDryRun(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	sent := 0
	send := func(*grpcviewv1.InvokeStreamResponse) error { sent++; return nil }
	err := w.invokeSavedStream(ctx, &grpcviewv1.InvokeSavedRequest{
		WorkspaceName: testWorkspace,
		ItemName:      "Whatever",
		DryRun:        true,
	}, send)
	if code := connect.CodeOf(err); code != connect.CodeInvalidArgument {
		t.Fatalf("connect.CodeOf(%v) = %v, want InvalidArgument", err, code)
	}
	if sent != 0 {
		t.Fatalf("%d frames sent for a rejected dry run, want 0", sent)
	}
}

// TestInvokeSavedStreamingMethodViaUnary: the unary form against a streaming saved request
// reuses invokeUnary's existing streaming guard verbatim (Unimplemented), rather than inventing
// a second code for the same condition. The CLI picks the right RPC from the resolved method
// kind, so this is a fallback, not a normal path.
func TestInvokeSavedStreamingMethodViaUnary(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	port := startEchoServer(t)

	saveRequest(t, w, ctx, nil, "Stream", "ServerStream",
		`export default () => ({ message: "hi" })`, loopback(port))

	_, err := invokeSaved(t, w, ctx, &grpcviewv1.InvokeSavedRequest{
		WorkspaceName: testWorkspace,
		ItemName:      "Stream",
	})
	if code := connect.CodeOf(err); code != connect.CodeUnimplemented {
		t.Fatalf("connect.CodeOf(%v) = %v, want Unimplemented", err, code)
	}
}

// TestInvokeSavedEmptyBodyRuns: a saved request whose body was never authored still runs — the
// blank source becomes the empty object instead of reaching the engine as an unparseable empty
// expression.
func TestInvokeSavedEmptyBodyRuns(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	port := startEchoServer(t)

	saveRequest(t, w, ctx, nil, "Empty", "Unary", "", loopback(port))

	got, err := invokeSaved(t, w, ctx, &grpcviewv1.InvokeSavedRequest{
		WorkspaceName: testWorkspace,
		ItemName:      "Empty",
	})
	if err != nil {
		t.Fatalf("InvokeSaved: %v", err)
	}
	if code := got.GetResponse().GetStatus().GetCode(); code != int32(codeOK) {
		t.Fatalf("status = %d (%s), want OK", code, got.GetResponse().GetStatus().GetMessage())
	}
	if msg := decodeEchoMessage(t, got.GetResponse().GetResponse()); msg != "echo: " {
		t.Fatalf("echoed message = %q, want %q", msg, "echo: ")
	}
}
