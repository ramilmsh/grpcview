package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	echov1 "codeberg.org/ramilmsh/grpcview/grpcview/echo/v1"
	grpcviewv1 "codeberg.org/ramilmsh/grpcview/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/store"
)

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

func loopback(port int) string { return fmt.Sprintf("127.0.0.1:%d", port) }

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

type failingEchoServer struct {
	echov1.UnimplementedEchoServiceServer
}

func (failingEchoServer) Unary(context.Context, *echov1.UnaryRequest) (*echov1.UnaryResponse, error) {
	return nil, status.Error(codes.PermissionDenied, "denied by test")
}

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

func invokeSaved(t *testing.T, w Workspace, ctx context.Context, msg *grpcviewv1.InvokeSavedRequest) (*grpcviewv1.InvokeSavedResponse, error) {
	t.Helper()
	resp, err := w.InvokeSaved(ctx, connect.NewRequest(msg))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

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

func paramsStruct(t *testing.T, m map[string]any) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(m)
	if err != nil {
		t.Fatalf("NewStruct: %v", err)
	}
	return s
}

func TestInvokeSavedRunsTheSavedRequest(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	port := echoTarget(t, w, ctx, startEchoServer)

	saveRequest(t, w, ctx, nil, "Echo", "Unary",
		`export default () => ({ message: "saved-body" })`, loopback(port))

	got, err := invokeSaved(t, w, ctx, &grpcviewv1.InvokeSavedRequest{
		Spec: &grpcviewv1.SavedInvokeSpec{
			Collection: testWorkspace,
			ItemName:   "Echo",
		},
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

func TestInvokeSavedRejectsAMissingSpec(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	for _, tc := range []struct {
		name string
		spec *grpcviewv1.SavedInvokeSpec
	}{
		{"no spec at all", nil},
		{"no collection", &grpcviewv1.SavedInvokeSpec{ItemName: "Echo"}},
		{"no item name", &grpcviewv1.SavedInvokeSpec{Collection: testWorkspace}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := invokeSaved(t, w, ctx, &grpcviewv1.InvokeSavedRequest{Spec: tc.spec})
			if err == nil {
				t.Fatal("InvokeSaved succeeded, want INVALID_ARGUMENT")
			}
			if code := connect.CodeOf(err); code != connect.CodeInvalidArgument {
				t.Fatalf("code = %v (%v), want INVALID_ARGUMENT", code, err)
			}

			serr := w.InvokeSavedStream(ctx, &grpcviewv1.InvokeSavedStreamingRequest{Spec: tc.spec}, func(*grpcviewv1.InvokeStreamingResponse) error { return nil })
			if code := connect.CodeOf(serr); code != connect.CodeInvalidArgument {
				t.Fatalf("streaming code = %v (%v), want INVALID_ARGUMENT", code, serr)
			}
		})
	}
}

func TestInvokeSavedNestedPath(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	port := echoTarget(t, w, ctx, startEchoServer)

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
		Spec: &grpcviewv1.SavedInvokeSpec{
			Collection: testWorkspace,
			Path:       []string{"Users"},
			ItemName:   "GetUser",
		},
	})
	if err != nil {
		t.Fatalf("InvokeSaved: %v", err)
	}
	if msg := decodeEchoMessage(t, got.GetResponse().GetResponse()); msg != "echo: nested" {
		t.Fatalf("echoed message = %q, want %q", msg, "echo: nested")
	}
}

func TestInvokeSavedParamsReachTheBody(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	port := echoTarget(t, w, ctx, startEchoServer)

	writeScript(t, w, ctx, "scripts/prefix.ts", `export default () => "p:"`)
	saveRequest(t, w, ctx, nil, "Echo", "Unary",
		"import prefix from \"#/scripts/prefix.ts\";\n"+requestParamsImport+
			`export default () => ({ message: prefix() + params.who })`, loopback(port))

	got, err := invokeSaved(t, w, ctx, &grpcviewv1.InvokeSavedRequest{
		Spec: &grpcviewv1.SavedInvokeSpec{
			Collection: testWorkspace,
			ItemName:   "Echo",
			Params:     paramsStruct(t, map[string]any{"who": "ada"}),
		},
	})
	if err != nil {
		t.Fatalf("InvokeSaved: %v", err)
	}
	if msg := decodeEchoMessage(t, got.GetResponse().GetResponse()); msg != "echo: p:ada" {
		t.Fatalf("echoed message = %q, want %q (params did not reach the body)", msg, "echo: p:ada")
	}
}

func TestInvokeSavedOverrides(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	port := echoTarget(t, w, ctx, startEchoServer)

	saveRequest(t, w, ctx, nil, "Echo", "Unary",
		`export default () => ({ message: "saved" })`, loopback(deadPort(t)))

	got, err := invokeSaved(t, w, ctx, &grpcviewv1.InvokeSavedRequest{
		Spec: &grpcviewv1.SavedInvokeSpec{
			Collection: testWorkspace,
			ItemName:   "Echo",
			Target:     &grpcviewv1.Server{Address: loopback(port)},
			Messages:   []string{tsBody(`{"message":"overridden"}`)},
		},
	})
	if err != nil {
		t.Fatalf("InvokeSaved: %v", err)
	}
	if msg := decodeEchoMessage(t, got.GetResponse().GetResponse()); msg != "echo: overridden" {
		t.Fatalf("echoed message = %q, want %q", msg, "echo: overridden")
	}
}

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
				Spec: &grpcviewv1.SavedInvokeSpec{
					Collection: testWorkspace,
					ItemName:   tc.itemName,
				},
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

func TestInvokeSavedTargetStatusIsInThePayload(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	port := echoTarget(t, w, ctx, startFailingEchoServer)

	saveRequest(t, w, ctx, nil, "Denied", "Unary",
		`export default () => ({ message: "hi" })`, loopback(port))

	got, err := invokeSaved(t, w, ctx, &grpcviewv1.InvokeSavedRequest{
		Spec: &grpcviewv1.SavedInvokeSpec{
			Collection: testWorkspace,
			ItemName:   "Denied",
		},
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

func TestInvokeSavedRecordHistory(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	port := echoTarget(t, w, ctx, startEchoServer)

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
		Spec: &grpcviewv1.SavedInvokeSpec{
			Collection:    testWorkspace,
			ItemName:      "Echo",
			RecordHistory: &off,
		},
	}); err != nil {
		t.Fatalf("InvokeSaved (record_history=false): %v", err)
	}
	if n := len(history(t)); n != 0 {
		t.Fatalf("history len = %d after record_history=false, want 0", n)
	}

	if _, err := invokeSaved(t, w, ctx, &grpcviewv1.InvokeSavedRequest{
		Spec: &grpcviewv1.SavedInvokeSpec{
			Collection: testWorkspace,
			ItemName:   "Echo",
		},
	}); err != nil {
		t.Fatalf("InvokeSaved (default): %v", err)
	}
	if n := len(history(t)); n != 1 {
		t.Fatalf("history len = %d after the default run, want exactly 1 (record_history "+
			"defaults to true)", n)
	}
}

func TestInvokeSavedDryRun(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	dead := loopback(deadPort(t))

	saveRequest(t, w, ctx, nil, "Echo", "Unary",
		requestParamsImport+`export default () => ({ message: "dry-" + params.n })`, dead)
	coll, err := w.store.Open(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	mdScript := requestParamsImport + `export default () => ({ "x-token": ["t-" + params.n] })`
	if err := coll.UpdateRequest(ctx, nil, "Echo", store.RequestPatch{DraftMetadataScript: &mdScript}); err != nil {
		t.Fatalf("UpdateRequest: %v", err)
	}

	got, err := invokeSaved(t, w, ctx, &grpcviewv1.InvokeSavedRequest{
		Spec: &grpcviewv1.SavedInvokeSpec{
			Collection: testWorkspace,
			ItemName:   "Echo",
			Params:     paramsStruct(t, map[string]any{"n": "7"}),
		},
		DryRun: true,
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

func TestInvokeSavedStreamingSendsEveryMessage(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	port := echoTarget(t, w, ctx, startEchoServer)

	saveRequest(t, w, ctx, nil, "Upload", "ClientStream",
		`export default () => ({ message: "saved" })`, loopback(port))

	var frames []*grpcviewv1.InvokeStreamingResponse
	send := func(resp *grpcviewv1.InvokeStreamingResponse) error {
		frames = append(frames, resp)
		return nil
	}
	err := w.invokeSavedStream(ctx, &grpcviewv1.InvokeSavedStreamingRequest{
		Spec: &grpcviewv1.SavedInvokeSpec{
			Collection: testWorkspace,
			ItemName:   "Upload",
			Messages: []string{
				tsBody(`{"message":"a"}`),
				tsBody(`{"message":"b"}`),
				tsBody(`{"message":"c"}`),
			},
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

func TestInvokeSavedStreamingParamsReachTheBody(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	port := echoTarget(t, w, ctx, startEchoServer)

	saveRequest(t, w, ctx, nil, "Stream", "ServerStream",
		requestParamsImport+`export default () => ({ message: "s-" + params.n, count: 2 })`, loopback(port))

	var frames []*grpcviewv1.InvokeStreamingResponse
	send := func(resp *grpcviewv1.InvokeStreamingResponse) error {
		frames = append(frames, resp)
		return nil
	}
	if err := w.invokeSavedStream(ctx, &grpcviewv1.InvokeSavedStreamingRequest{
		Spec: &grpcviewv1.SavedInvokeSpec{
			Collection: testWorkspace,
			ItemName:   "Stream",
			Params:     paramsStruct(t, map[string]any{"n": "9"}),
		},
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

func TestInvokeSavedStreamingMethodViaUnary(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	port := echoTarget(t, w, ctx, startEchoServer)

	saveRequest(t, w, ctx, nil, "Stream", "ServerStream",
		`export default () => ({ message: "hi" })`, loopback(port))

	_, err := invokeSaved(t, w, ctx, &grpcviewv1.InvokeSavedRequest{
		Spec: &grpcviewv1.SavedInvokeSpec{
			Collection: testWorkspace,
			ItemName:   "Stream",
		},
	})
	if code := connect.CodeOf(err); code != connect.CodeUnimplemented {
		t.Fatalf("connect.CodeOf(%v) = %v, want Unimplemented", err, code)
	}
}

func TestInvokeSavedEmptyBodyRuns(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	port := echoTarget(t, w, ctx, startEchoServer)

	saveRequest(t, w, ctx, nil, "Empty", "Unary", "", loopback(port))

	got, err := invokeSaved(t, w, ctx, &grpcviewv1.InvokeSavedRequest{
		Spec: &grpcviewv1.SavedInvokeSpec{
			Collection: testWorkspace,
			ItemName:   "Empty",
		},
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

// A caller supplying explicit Messages is evaluating its own bytes, not the saved request's
// body.ts — so an evaluation error must not point at a file that was never read.
func TestInvokeSavedBodyAttributionOnlyWhenReadFromDisk(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	port := echoTarget(t, w, ctx, startEchoServer)

	saveRequest(t, w, ctx, nil, "Echo", "Unary",
		`export default () => { unterminated`, loopback(port))

	t.Run("body read from disk names body.ts", func(t *testing.T) {
		_, err := invokeSaved(t, w, ctx, &grpcviewv1.InvokeSavedRequest{
			Spec: &grpcviewv1.SavedInvokeSpec{
				Collection: testWorkspace,
				ItemName:   "Echo",
			},
		})
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("code = %v (%v), want FailedPrecondition", connect.CodeOf(err), err)
		}
		if !strings.Contains(err.Error(), "body.ts") {
			t.Fatalf("error = %v, want it to name body.ts", err)
		}
	})

	t.Run("explicit messages do not name body.ts", func(t *testing.T) {
		_, err := invokeSaved(t, w, ctx, &grpcviewv1.InvokeSavedRequest{
			Spec: &grpcviewv1.SavedInvokeSpec{
				Collection: testWorkspace,
				ItemName:   "Echo",
				Messages:   []string{`export default () => { also unterminated`},
			},
		})
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("code = %v (%v), want FailedPrecondition", connect.CodeOf(err), err)
		}
		if strings.Contains(err.Error(), "body.ts") {
			t.Fatalf("error = %v, must not name body.ts: the bytes came from the caller's "+
				"explicit messages, not the file on disk", err)
		}
	})
}
