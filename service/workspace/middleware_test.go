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

// createMiddleware writes a script at a collection-relative path, e.g. "scripts/inject.ts".
// Middleware has no kind any more — a request attaches it by specifier (middleware_test.go's own
// callers use "#/scripts/...", against the collection this helper always writes into).
func createMiddleware(t *testing.T, w Workspace, ctx context.Context, path, source string) {
	t.Helper()
	writeScript(t, w, ctx, path, source)
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
	createMiddleware(t, w, ctx, "scripts/inject.ts", `export function handle(ctx){ ctx.metadata["x-injected"] = "yes"; return ctx }`)
	saveRequestWithMiddleware(t, w, ctx, "Echo", []string{"#/scripts/inject.ts"})

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
	createMiddleware(t, w, ctx, "scripts/rewrite.ts", `export function handle(ctx){ ctx.body.message = "rewritten"; return ctx }`)
	saveRequestWithMiddleware(t, w, ctx, "Echo", []string{"#/scripts/rewrite.ts"})

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
	createMiddleware(t, w, ctx, "scripts/first.ts", `export function handle(ctx){ ctx.body.message = "A"; return ctx }`)
	createMiddleware(t, w, ctx, "scripts/second.ts", `export function handle(ctx){ ctx.body.message = ctx.body.message + "B"; return ctx }`)
	saveRequestWithMiddleware(t, w, ctx, "Echo", []string{"#/scripts/first.ts", "#/scripts/second.ts"})

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
	createMiddleware(t, w, ctx, "scripts/boom.ts", `export function handle(ctx){ throw new Error("nope") }`)
	createMiddleware(t, w, ctx, "scripts/malformed.ts", `export function handle(ctx){ return 42 }`)
	saveRequestWithMiddleware(t, w, ctx, "Boom", []string{"#/scripts/boom.ts"})
	saveRequestWithMiddleware(t, w, ctx, "Malformed", []string{"#/scripts/malformed.ts"})
	saveRequestWithMiddleware(t, w, ctx, "Missing", []string{"#/scripts/ghost.ts"})
	saveRequestWithMiddleware(t, w, ctx, "BadGrammar", []string{"ghost"})

	port := echoTarget(t, w, ctx, startEchoServer)
	for _, item := range []string{"Boom", "Malformed", "Missing", "BadGrammar"} {
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

func TestInvokeMiddlewareSpecifierEscapingItsRootIsRejected(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	saveRequestWithMiddleware(t, w, ctx, "Echo", []string{"#/../../etc/passwd"})

	port := echoTarget(t, w, ctx, startEchoServer)
	_, err := w.Invoke(ctx, connect.NewRequest(&grpcviewv1.InvokeRequest{
		Spec: &grpcviewv1.InvokeSpec{
			Collection: testWorkspace,
			ItemName:   "Echo",
			Service:    echoService,
			Method:     "Unary",
			Target:     &grpcviewv1.Server{Address: fmt.Sprintf("127.0.0.1:%d", port)},
		},
		Body: tsBody(`{"message":"hi"}`),
	}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition (err=%v)", connect.CodeOf(err), err)
	}
	if !strings.Contains(err.Error(), "#/../../etc/passwd") {
		t.Fatalf("error = %v, want it to name the offending specifier", err)
	}
}

func TestInvokeMiddlewareByWorkspaceSpecifier(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	other, err := w.store.Open(ctx, "shared")
	if err != nil {
		t.Fatalf("Open shared: %v", err)
	}
	if err := other.Create(ctx, ""); err != nil {
		t.Fatalf("Create shared: %v", err)
	}
	if err := other.CreateScript(ctx, "scripts/mw/auth.ts"); err != nil {
		t.Fatalf("CreateScript: %v", err)
	}
	src := `export function handle(ctx){ ctx.metadata["x-shared"] = "yes"; return ctx }`
	if err := other.UpdateScript(ctx, "scripts/mw/auth.ts", store.ScriptPatch{Source: &src}); err != nil {
		t.Fatalf("UpdateScript: %v", err)
	}
	saveRequestWithMiddleware(t, w, ctx, "Echo", []string{"@/shared/scripts/mw/auth.ts"})

	port := echoTarget(t, w, ctx, startEchoServer)
	resp := echoInvoke(t, w, ctx, port, "Echo", `{"message":"hi"}`)
	if got := resp.Msg.GetResponse().GetStatus().GetCode(); got != int32(codeOK) {
		t.Fatalf("status = %d (%s)", got, resp.Msg.GetResponse().GetStatus().GetMessage())
	}
	if got := resp.Msg.GetResponse().GetRequestMetadata().GetFields()["x-shared"].GetStringValue(); got != "yes" {
		t.Fatalf("sent metadata x-shared = %q, want yes (a middleware attached by @/ specifier did not run)", got)
	}
}

func TestStreamInvokeRunsMiddleware(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	createMiddleware(t, w, ctx, "scripts/streamrewrite.ts", `export function handle(ctx){ ctx.body.message = "streamed"; return ctx }`)
	saveRequestWithMiddleware(t, w, ctx, "Stream", []string{"#/scripts/streamrewrite.ts"})

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

// The natural use for a middleware — stamp a trace id, sign a request — is exactly what an
// imported script is for, so the middleware path composes them like every body/metadata path does.
func TestInvokeMiddlewareCallsAnImportedScript(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	writeScript(t, w, ctx, "scripts/traceId.ts", `export default () => "t-42"`)
	createMiddleware(t, w, ctx, "scripts/stamp.ts",
		"import traceId from \"#/scripts/traceId.ts\";\n"+
			`export function handle(ctx){ ctx.metadata["x-trace"] = traceId(); return ctx }`)
	saveRequestWithMiddleware(t, w, ctx, "Echo", []string{"#/scripts/stamp.ts"})

	port := echoTarget(t, w, ctx, startEchoServer)
	resp := echoInvoke(t, w, ctx, port, "Echo", `{"message":"hi"}`)
	if got := resp.Msg.GetResponse().GetStatus().GetCode(); got != int32(codeOK) {
		t.Fatalf("status = %d (%s)", got, resp.Msg.GetResponse().GetStatus().GetMessage())
	}
	if got := resp.Msg.GetResponse().GetRequestMetadata().GetFields()["x-trace"].GetStringValue(); got != "t-42" {
		t.Fatalf("sent metadata x-trace = %q, want t-42 (middleware could not call the imported script)", got)
	}
}
