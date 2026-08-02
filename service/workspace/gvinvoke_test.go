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

// gvinvoke_test.go covers gv-features-plan.md Feature 3 Phase 2, "Workspace re-entry,
// addressing, depth guard": scriptInvoker's addressing/depth-guard/reject-vs-resolve policy,
// and the load-bearing headline case — a nested gv.invoke actually running a fresh child
// wazero instance to completion inside the parent's suspended host call, on one goroutine.

// gvTarget saves a request named name pointed at the loopback echo server, so it is a valid
// gv.invoke target: a TS body (defaulting to a trivial one) and an explicit per-request Target
// (so resolveMethod can dial+reflect it without a workspace reflection source configured).
func gvTarget(t *testing.T, w Workspace, ctx context.Context, parent []string, name, body string, port int) {
	t.Helper()
	saveRequest(t, w, ctx, parent, name, "Unary", body, loopback(port))
}

// TestGvInvokeNestedReentrancy is the headline test (gv-features-plan.md Feature 3 Phase 2's
// load-bearing risk): request A's body does `await gv.invoke("B", {id:7})` from INSIDE the
// scripting engine — a nested wazero instance for B must run to completion inside A's
// suspended host call, on the same goroutine, and A must consume B's real result.
//
// B's body reads gv.request.params.id and folds it into the message it sends to the REAL echo
// server; the server's own "echo: " prefix can only appear if B actually dialed and called it
// (not a faked/short-circuited result), so B's reply proves BOTH that gv.request.params.id was
// visible to B (===7, spliced into "id-7") and that B genuinely ran end to end. A's body then
// folds gv.invoke's whole resolved InvokeResult (ok + body.message) into its OWN outgoing
// message, so A's own echoed reply proves A consumed B's actual settled value, not a stale,
// default, or dropped one.
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
		WorkspaceName: testWorkspace,
		Service:       echoService,
		Method:        "Unary",
		Body:          aBody,
		Target:        &grpcviewv1.Server{Address: fmt.Sprintf("127.0.0.1:%d", port)},
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

// TestGvInvokeSuppressesNestedHistory is the required D6 case: a nested gv.invoke must record
// NOTHING to its target's history (a script fan-out must not spam N requests' histories), while
// the outer, publicly-invoked request still records its own single entry as usual.
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
		WorkspaceName: testWorkspace,
		ItemName:      "A",
		Service:       echoService,
		Method:        "Unary",
		Body:          aBody,
		Target:        &grpcviewv1.Server{Address: fmt.Sprintf("127.0.0.1:%d", port)},
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

// TestScriptInvokerAddressingSplitsOnSlash exercises scriptInvoker directly (bytes in, bytes
// out — the documented scripting.Invoker contract) against a request nested inside a folder,
// proving gv.invoke's addressing: the path splits on "/" into the parent folder's display-name
// path (leading segments) and the item name (last segment), e.g. "Users/GetUser".
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

// TestScriptInvokerUnknownPathRejects: a path that resolves to nothing must reject (a Go error
// out of the Invoker), not panic or silently resolve.
func TestScriptInvokerUnknownPathRejects(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	inv := w.scriptInvoker(testWorkspace)
	if _, err := inv(ctx, []byte(`{"path":"NoSuchRequest","params":{}}`)); err == nil {
		t.Fatalf("want an error for an unknown path, got nil")
	}
}

// TestScriptInvokerNotARequestRejects: a path naming a FOLDER (not a request) must also reject
// — gv.invoke only addresses requests.
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

// TestScriptInvokerStreamingTargetRejects: gv.invoke is unary-only in v1 (plan §"Return
// shape"); a path resolving to a streaming method must reject via invokeUnary's shared
// streaming guard — the same check the public Invoke RPC uses.
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

// TestScriptInvokerDepthCapRejects drives scriptInvoker directly with a ctx pre-seeded at (and
// just below) the depth cap via withGvInvokeDepth — the exact ctx seam a real N-level-deep
// gv.invoke chain would have threaded — proving the boundary itself: one below the cap
// proceeds, AT the cap rejects. (A real 9-level-deep recursive chain would exercise the same
// comparison after 8 real hops; short-circuiting the "getting there" is a deliberate,
// deterministic test of the policy invokeUnary's caller enforces, not a numerical coincidence —
// withGvInvokeDepth is the very function that threads the counter across a real hop.)
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
