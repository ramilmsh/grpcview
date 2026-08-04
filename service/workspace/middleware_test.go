package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/store"
)

func createMiddleware(t *testing.T, w Workspace, ctx context.Context, name, source string) {
	t.Helper()
	coll, err := w.store.Open(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := coll.CreateScript(ctx, name, grpcviewv1.ScriptKind_SCRIPT_KIND_MIDDLEWARE); err != nil {
		t.Fatalf("CreateScript %q: %v", name, err)
	}
	if err := coll.UpdateScript(ctx, name, store.ScriptPatch{Source: &source}); err != nil {
		t.Fatalf("UpdateScript %q: %v", name, err)
	}
}

func saveRequestWithMiddleware(t *testing.T, w Workspace, ctx context.Context, name string, mw []string) {
	t.Helper()
	coll, err := w.store.Open(ctx, testWorkspace)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := coll.CreateRequest(ctx, nil, name, echoService, "Unary"); err != nil {
		t.Fatalf("CreateRequest %q: %v", name, err)
	}
	if err := coll.UpdateRequest(ctx, nil, name, store.RequestPatch{SetMiddleware: true, Middleware: mw}); err != nil {
		t.Fatalf("attach middleware to %q: %v", name, err)
	}
}

func echoInvoke(t *testing.T, w Workspace, ctx context.Context, port int, itemName, body string) *connect.Response[grpcviewv1.InvokeResponse] {
	t.Helper()
	resp, err := w.Invoke(ctx, connect.NewRequest(&grpcviewv1.InvokeRequest{
		Spec: &grpcviewv1.InvokeSpec{
			Collection: testWorkspace,
			ItemName:   itemName,
			Service:    echoService,
			Method:     "Unary",
			Target:     &grpcviewv1.Server{Address: fmt.Sprintf("127.0.0.1:%d", port)},
		},
		Body: tsBody(body),
	}))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	return resp
}

func echoedMessage(t *testing.T, resp *connect.Response[grpcviewv1.InvokeResponse]) string {
	t.Helper()
	if code := resp.Msg.GetResponse().GetStatus().GetCode(); code != int32(codeOK) {
		t.Fatalf("status = %d (%s)", code, resp.Msg.GetResponse().GetStatus().GetMessage())
	}
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(resp.Msg.GetResponse().GetResponse(), &payload); err != nil {
		t.Fatalf("unmarshal echo response: %v", err)
	}
	return payload.Message
}

func TestInvokeMiddlewareInjectsMetadata(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	createMiddleware(t, w, ctx, "inject", `export function handle(ctx){ ctx.metadata["x-injected"] = "yes"; return ctx }`)
	saveRequestWithMiddleware(t, w, ctx, "Echo", []string{"inject"})

	port := echoTarget(t, w, ctx, startEchoServer)
	resp := echoInvoke(t, w, ctx, port, "Echo", `{"message":"hi"}`)
	if got := resp.Msg.GetResponse().GetStatus().GetCode(); got != int32(codeOK) {
		t.Fatalf("status = %d (%s)", got, resp.Msg.GetResponse().GetStatus().GetMessage())
	}
	if got := resp.Msg.GetResponse().GetRequestMetadata().GetFields()["x-injected"].GetStringValue(); got != "yes" {
		t.Fatalf("sent metadata x-injected = %q, want yes (middleware header not applied)", got)
	}
}

func TestInvokeMiddlewareRewritesBody(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	createMiddleware(t, w, ctx, "rewrite", `export function handle(ctx){ ctx.body.message = "rewritten"; return ctx }`)
	saveRequestWithMiddleware(t, w, ctx, "Echo", []string{"rewrite"})

	port := echoTarget(t, w, ctx, startEchoServer)
	resp := echoInvoke(t, w, ctx, port, "Echo", `{"message":"orig"}`)
	if got := echoedMessage(t, resp); got != "echo: rewritten" {
		t.Fatalf("echoed message = %q, want %q (body not rewritten)", got, "echo: rewritten")
	}
}

func TestInvokeMiddlewareChainOrdered(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	createMiddleware(t, w, ctx, "first", `export function handle(ctx){ ctx.body.message = "A"; return ctx }`)
	createMiddleware(t, w, ctx, "second", `export function handle(ctx){ ctx.body.message = ctx.body.message + "B"; return ctx }`)
	saveRequestWithMiddleware(t, w, ctx, "Echo", []string{"first", "second"})

	port := echoTarget(t, w, ctx, startEchoServer)
	resp := echoInvoke(t, w, ctx, port, "Echo", `{"message":"orig"}`)
	if got := echoedMessage(t, resp); got != "echo: AB" {
		t.Fatalf("echoed message = %q, want %q (chain not run in order)", got, "echo: AB")
	}
}

func TestInvokeMiddlewareNoOp(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	saveRequestWithMiddleware(t, w, ctx, "Plain", nil)

	port := echoTarget(t, w, ctx, startEchoServer)
	if got := echoedMessage(t, echoInvoke(t, w, ctx, port, "Plain", `{"message":"orig"}`)); got != "echo: orig" {
		t.Fatalf("detached middleware changed body: %q", got)
	}
	if got := echoedMessage(t, echoInvoke(t, w, ctx, port, "", `{"message":"adhoc"}`)); got != "echo: adhoc" {
		t.Fatalf("ad-hoc invoke ran middleware: %q", got)
	}
}

func TestInvokeMiddlewareErrors(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	createMiddleware(t, w, ctx, "boom", `export function handle(ctx){ throw new Error("nope") }`)
	createMiddleware(t, w, ctx, "malformed", `export function handle(ctx){ return 42 }`)
	saveRequestWithMiddleware(t, w, ctx, "Boom", []string{"boom"})
	saveRequestWithMiddleware(t, w, ctx, "Malformed", []string{"malformed"})
	saveRequestWithMiddleware(t, w, ctx, "Missing", []string{"ghost"})

	port := echoTarget(t, w, ctx, startEchoServer)
	for _, item := range []string{"Boom", "Malformed", "Missing"} {
		_, err := w.Invoke(ctx, connect.NewRequest(&grpcviewv1.InvokeRequest{
			Spec: &grpcviewv1.InvokeSpec{
				Collection: testWorkspace,
				ItemName:   item,
				Service:    echoService,
				Method:     "Unary",
				Target:     &grpcviewv1.Server{Address: fmt.Sprintf("127.0.0.1:%d", port)},
			},
			Body: tsBody(`{"message":"hi"}`),
		}))
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("%s: code = %v, want FailedPrecondition (err=%v)", item, connect.CodeOf(err), err)
		}
	}
}

func TestStreamInvokeRunsMiddleware(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	createMiddleware(t, w, ctx, "streamrewrite", `export function handle(ctx){ ctx.body.message = "streamed"; return ctx }`)
	saveRequestWithMiddleware(t, w, ctx, "Stream", []string{"streamrewrite"})

	port := echoTarget(t, w, ctx, startEchoServer)
	msg := echoStreamReq(port, "ServerStream", `{"message":"orig","count":2}`)
	msg.Spec.ItemName = "Stream"
	frames, err := collectStream(ctx, w, msg)
	if err != nil {
		t.Fatalf("streamInvoke: %v", err)
	}
	msgs, result := splitFrames(t, frames)
	if code := result.GetStatus().GetCode(); code != int32(codeOK) {
		t.Fatalf("status = %d (%s)", code, result.GetStatus().GetMessage())
	}
	if len(msgs) != 2 {
		t.Fatalf("want 2 message frames (count:2), got %d", len(msgs))
	}
	for i, m := range msgs {
		var payload struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(m, &payload); err != nil {
			t.Fatalf("frame %d not JSON: %v", i, err)
		}
		if !strings.Contains(payload.Message, "streamed") {
			t.Fatalf("frame %d message = %q, want it to carry the rewritten 'streamed'", i, payload.Message)
		}
	}
}
