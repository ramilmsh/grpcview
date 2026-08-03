package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/store"
)

func gvTarget(t *testing.T, w Workspace, ctx context.Context, parent []string, name, body string, port int) {
	t.Helper()
	saveRequest(t, w, ctx, parent, name, "Unary", body, loopback(port))
}

func TestGvInvokeNestedReentrancy(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	port := startEchoServer(t)

	gvTarget(t, w, ctx, nil, "B", `export default () => ({ message: "id-" + gv.request.params.id })`, port)

	aBody := `export default async () => {
  const b = await gv.invoke("B", { id: 7 });
  return { message: "A-saw-ok=" + b.ok + "-body=" + b.body.message };
}`

	resp, err := w.Invoke(ctx, connect.NewRequest(&grpcviewv1.InvokeRequest{
		Spec: &grpcviewv1.InvokeSpec{
			WorkspaceName: testWorkspace,
			Service:       echoService,
			Method:        "Unary",
			Target:        &grpcviewv1.Server{Address: fmt.Sprintf("127.0.0.1:%d", port)},
		},
		Body: aBody,
	}))
	if err != nil {
		t.Fatalf("Invoke A: %v", err)
	}
	if code := resp.Msg.GetResponse().GetStatus().GetCode(); code != int32(codeOK) {
		t.Fatalf("status = %d (%s)", code, resp.Msg.GetResponse().GetStatus().GetMessage())
	}
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(resp.Msg.GetResponse().GetResponse(), &payload); err != nil {
		t.Fatalf("unmarshal echo response: %v", err)
	}
	want := "echo: A-saw-ok=true-body=echo: id-7"
	if payload.Message != want {
		t.Fatalf("echoed message = %q, want %q (nested gv.invoke did not run to completion / "+
			"params or response not threaded correctly)", payload.Message, want)
	}
}

// The Invoker rides resolvePreSend, not invokeUnary: a STREAMING request's body must be able to
// call gv.invoke too. While it was installed by invokeUnary alone this rejected with
// "invoke is not available in this context" and streamInvoke returned FailedPrecondition.
func TestGvInvokeFromStreamingPath(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	port := startEchoServer(t)

	gvTarget(t, w, ctx, nil, "B", `export default () => ({ message: "id-" + gv.request.params.id })`, port)

	body := `export default async () => {
  const b = await gv.invoke("B", { id: 3 });
  return { message: "S-ok=" + b.ok + "-body=" + b.body.message, count: 1 };
}`
	frames, err := collectStream(ctx, w, &grpcviewv1.InvokeStreamRequest{
		Spec: &grpcviewv1.InvokeSpec{
			WorkspaceName: testWorkspace,
			Service:       echoService,
			Method:        "ServerStream",
			Target:        &grpcviewv1.Server{Address: loopback(port)},
		},
		Messages: []string{body},
	})
	if err != nil {
		t.Fatalf("streamInvoke: %v (gv.invoke must be available on the streaming path)", err)
	}
	msgs, result := splitFrames(t, frames)
	if code := result.GetStatus().GetCode(); code != int32(codeOK) {
		t.Fatalf("terminal status = %d (%s), want OK", code, result.GetStatus().GetMessage())
	}
	if len(msgs) != 1 {
		t.Fatalf("message frames = %d, want 1 (the body asked for count 1)", len(msgs))
	}
	want := "echo #0: S-ok=true-body=echo: id-3"
	if got := decodeEchoMessage(t, msgs[0]); got != want {
		t.Fatalf("echoed message = %q, want %q (nested gv.invoke did not run from the "+
			"streaming path)", got, want)
	}
}

// The same seam covers the saved-request DRY RUN, which stops after resolvePreSend and so never
// reaches invokeUnary at all: its body's gv.invoke used to reject unconditionally.
func TestGvInvokeFromSavedDryRun(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	port := startEchoServer(t)

	gvTarget(t, w, ctx, nil, "B", `export default () => ({ message: "id-" + gv.request.params.id })`, port)
	// A's own target is dead: a dry run must not dial it, but B's real one is still invoked.
	saveRequest(t, w, ctx, nil, "A", "Unary", `export default async () => {
  const b = await gv.invoke("B", { id: 5 });
  return { message: "D-ok=" + b.ok + "-body=" + b.body.message };
}`, loopback(deadPort(t)))

	got, err := invokeSaved(t, w, ctx, &grpcviewv1.InvokeSavedRequest{
		WorkspaceName: testWorkspace,
		ItemName:      "A",
		DryRun:        true,
	})
	if err != nil {
		t.Fatalf("InvokeSaved (dry_run): %v (gv.invoke must be available on the dry-run path)", err)
	}
	msgs := got.GetResolved().GetMessages()
	if len(msgs) != 1 {
		t.Fatalf("resolved messages = %d, want 1", len(msgs))
	}
	want := "D-ok=true-body=echo: id-5"
	if evaluated := decodeEchoMessage(t, []byte(msgs[0])); evaluated != want {
		t.Fatalf("evaluated body message = %q, want %q (nested gv.invoke did not run from the "+
			"dry-run path)", evaluated, want)
	}
}

func TestGvInvokeSuppressesNestedHistory(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	port := startEchoServer(t)

	gvTarget(t, w, ctx, nil, "B", `export default () => ({ message: "b" })`, port)
	coll, err := w.store.Open(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := coll.CreateRequest(ctx, nil, "A", echoService, "Unary"); err != nil {
		t.Fatalf("CreateRequest A: %v", err)
	}
	aBody := `export default async () => { await gv.invoke("B", {}); return { message: "a" }; }`

	resp, err := w.Invoke(ctx, connect.NewRequest(&grpcviewv1.InvokeRequest{
		Spec: &grpcviewv1.InvokeSpec{
			WorkspaceName: testWorkspace,
			ItemName:      "A",
			Service:       echoService,
			Method:        "Unary",
			Target:        &grpcviewv1.Server{Address: fmt.Sprintf("127.0.0.1:%d", port)},
		},
		Body: aBody,
	}))
	if err != nil {
		t.Fatalf("Invoke A: %v", err)
	}
	if code := resp.Msg.GetResponse().GetStatus().GetCode(); code != int32(codeOK) {
		t.Fatalf("status = %d (%s)", code, resp.Msg.GetResponse().GetStatus().GetMessage())
	}

	ws, err := coll.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var aHist, bHist []*grpcviewv1.History
	for _, it := range ws.GetItem().GetFolder().GetItems() {
		switch it.GetName() {
		case "A":
			aHist = it.GetRequest().GetHistory()
		case "B":
			bHist = it.GetRequest().GetHistory()
		}
	}
	if len(aHist) != 1 {
		t.Fatalf("A history len = %d, want 1 (the outer public Invoke must still record)", len(aHist))
	}
	if len(bHist) != 0 {
		t.Fatalf("B history len = %d, want 0 (a nested gv.invoke must not record history — D6)", len(bHist))
	}
}

func TestScriptInvokerAddressingSplitsOnSlash(t *testing.T) {
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
	gvTarget(t, w, ctx, []string{"Users"}, "GetUser", `export default () => ({ message: "nested-ok" })`, port)

	inv := w.scriptInvoker(testWorkspace)
	respBytes, err := inv(ctx, []byte(`{"path":"Users/GetUser","params":{}}`))
	if err != nil {
		t.Fatalf("invoke Users/GetUser: %v", err)
	}
	var got struct {
		OK   bool `json:"ok"`
		Body struct {
			Message string `json:"message"`
		} `json:"body"`
	}
	if err := json.Unmarshal(respBytes, &got); err != nil {
		t.Fatalf("decode result %s: %v", respBytes, err)
	}
	if !got.OK || got.Body.Message != "echo: nested-ok" {
		t.Fatalf("result = %+v, want ok=true body.message=%q", got, "echo: nested-ok")
	}
}

func TestScriptInvokerUnknownPathRejects(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	inv := w.scriptInvoker(testWorkspace)
	if _, err := inv(ctx, []byte(`{"path":"NoSuchRequest","params":{}}`)); err == nil {
		t.Fatalf("want an error for an unknown path, got nil")
	}
}

func TestScriptInvokerNotARequestRejects(t *testing.T) {
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

	inv := w.scriptInvoker(testWorkspace)
	if _, err := inv(ctx, []byte(`{"path":"Folder","params":{}}`)); err == nil {
		t.Fatalf("want an error for a path naming a folder, got nil")
	}
}

func TestScriptInvokerStreamingTargetRejects(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	port := startEchoServer(t)

	coll, err := w.store.Open(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := coll.CreateRequest(ctx, nil, "Stream", echoService, "ServerStream"); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	target := &grpcviewv1.Server{Address: fmt.Sprintf("127.0.0.1:%d", port)}
	if err := coll.UpdateRequest(ctx, nil, "Stream", store.RequestPatch{SetTarget: true, Target: target}); err != nil {
		t.Fatalf("UpdateRequest: %v", err)
	}

	inv := w.scriptInvoker(testWorkspace)
	if _, err := inv(ctx, []byte(`{"path":"Stream","params":{}}`)); err == nil {
		t.Fatalf("want an error for a streaming target, got nil")
	}
}

func TestScriptInvokerDepthCapRejects(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	port := startEchoServer(t)

	gvTarget(t, w, ctx, nil, "Target", `export default () => ({ message: "hi" })`, port)
	envelope := []byte(`{"path":"Target","params":{}}`)
	inv := w.scriptInvoker(testWorkspace)

	t.Run("just below the cap proceeds", func(t *testing.T) {
		below := withGvInvokeDepth(ctx, maxInvokeDepth-1)
		if _, err := inv(below, envelope); err != nil {
			t.Fatalf("depth %d (below the cap) should proceed, got %v", maxInvokeDepth-1, err)
		}
	})

	t.Run("at the cap rejects", func(t *testing.T) {
		atCap := withGvInvokeDepth(ctx, maxInvokeDepth)
		if _, err := inv(atCap, envelope); err == nil {
			t.Fatalf("depth %d (at the cap) should reject, got nil", maxInvokeDepth)
		}
	})
}
