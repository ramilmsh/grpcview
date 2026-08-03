package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

func TestResolveInvokeBody(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()

	t.Run("typescript body evaluates to its returned object", func(t *testing.T) {
		out, err := w.resolveInvokeBody(ctx, testWorkspace,
			[]string{`export default () => ({ message: "hi-" + (1 + 1) })`}, nil)
		if err != nil {
			t.Fatalf("resolveInvokeBody: %v", err)
		}
		if len(out) != 1 || out[0] != `{"message":"hi-2"}` {
			t.Fatalf("got %q, want [{\"message\":\"hi-2\"}]", out)
		}
	})

	t.Run("typescript evaluates every body (streaming shape)", func(t *testing.T) {
		out, err := w.resolveInvokeBody(ctx, testWorkspace,
			[]string{`export default () => ({ n: 1 })`, `export default () => ({ n: 2 })`}, nil)
		if err != nil {
			t.Fatalf("resolveInvokeBody: %v", err)
		}
		if len(out) != 2 || out[0] != `{"n":1}` || out[1] != `{"n":2}` {
			t.Fatalf("got %q, want [{\"n\":1} {\"n\":2}]", out)
		}
	})

	for _, c := range []struct{ name, body string }{
		{"throwing body", `export default () => { throw new Error("boom") }`},
		{"number return is not an object", `export default () => 42`},
		{"array return is not an object", `export default () => [1, 2, 3]`},
		{"string return is not an object", `export default () => "just a string"`},
		{"undefined return is not an object", `export default () => undefined`},
	} {
		t.Run(c.name+" errors FailedPrecondition", func(t *testing.T) {
			if _, err := w.resolveInvokeBody(ctx, testWorkspace,
				[]string{c.body}, nil); connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("code = %v, want FailedPrecondition (err=%v)", connect.CodeOf(err), err)
			}
		})
	}
}

func TestResolveInvokeBodyExpressionBody(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()

	for _, c := range []struct{ name, body, want string }{
		{"bare protojson object", `{"a":1}`, `{"a":1}`},
		{"already a module is unchanged", `export default () => ({"a":1})`, `{"a":1}`},
		{"multi-line bare object", "{\n  \"a\": 1,\n  \"b\": \"two\"\n}", `{"a":1,"b":"two"}`},
		{"expression calling into JS", `{ "a": 1 + 1 }`, `{"a":2}`},
		{"the emptyBody default", emptyBody, `{}`},
		{"bare empty object", `{}`, `{}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, err := w.resolveInvokeBody(ctx, testWorkspace, []string{c.body}, nil)
			if err != nil {
				t.Fatalf("resolveInvokeBody(%q): %v", c.body, err)
			}
			if len(out) != 1 || out[0] != c.want {
				t.Fatalf("got %q, want [%s]", out, c.want)
			}
		})
	}

	for _, c := range []struct{ name, body string }{
		{"bare array is not an object", `[1,2]`},
		{"bare number is not an object", `42`},
		{"bare string is not an object", `"nope"`},
	} {
		t.Run(c.name+" errors FailedPrecondition", func(t *testing.T) {
			if _, err := w.resolveInvokeBody(ctx, testWorkspace,
				[]string{c.body}, nil); connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("code = %v, want FailedPrecondition (err=%v)", connect.CodeOf(err), err)
			}
		})
	}
}

func TestResolveInvokeBodyComposition(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	createGenerator(t, w, ctx, "mkid", `export default () => "id-42"`)

	t.Run("typescript body composes a saved generator", func(t *testing.T) {
		out, err := w.resolveInvokeBody(ctx, testWorkspace,
			[]string{`export default () => ({ id: mkid(), n: 7 })`}, nil)
		if err != nil {
			t.Fatalf("resolveInvokeBody: %v", err)
		}
		if len(out) != 1 || out[0] != `{"id":"id-42","n":7}` {
			t.Fatalf("got %q, want [{\"id\":\"id-42\",\"n\":7}]", out)
		}
	})

	t.Run("an expression body composes a saved generator", func(t *testing.T) {
		out, err := w.resolveInvokeBody(ctx, testWorkspace,
			[]string{`{ "id": mkid() }`}, nil)
		if err != nil {
			t.Fatalf("resolveInvokeBody: %v", err)
		}
		if len(out) != 1 || out[0] != `{"id":"id-42"}` {
			t.Fatalf("got %q, want [{\"id\":\"id-42\"}]", out)
		}
	})

	t.Run("an unreferenced broken generator does not break the body", func(t *testing.T) {
		createGenerator(t, w, ctx, "broken", `export default () => "unterminated`)
		out, err := w.resolveInvokeBody(ctx, testWorkspace,
			[]string{`export default () => ({ id: mkid() })`}, nil)
		if err != nil {
			t.Fatalf("resolveInvokeBody (unreferenced broken generator): %v", err)
		}
		if len(out) != 1 || out[0] != `{"id":"id-42"}` {
			t.Fatalf("got %q, want [{\"id\":\"id-42\"}]", out)
		}
	})

	t.Run("a broken generator whose name collides with a key or method does not break the body", func(t *testing.T) {
		createGenerator(t, w, ctx, "id", `export default () => "unterminated`)
		createGenerator(t, w, ctx, "toString", `export default () => "also broken`)
		out, err := w.resolveInvokeBody(ctx, testWorkspace,
			[]string{`export default () => ({ id: mkid(), label: (7).toString() })`}, nil)
		if err != nil {
			t.Fatalf("resolveInvokeBody (name-collision isolation): %v", err)
		}
		if len(out) != 1 || out[0] != `{"id":"id-42","label":"7"}` {
			t.Fatalf("got %q, want [{\"id\":\"id-42\",\"label\":\"7\"}]", out)
		}
	})
}

func TestResolveInvokeBodyTransitiveComposition(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	createGenerator(t, w, ctx, "inner", `export default () => "in"`)
	createGenerator(t, w, ctx, "outer", `export default () => inner()`)

	t.Run("body calling only outer folds in inner transitively", func(t *testing.T) {
		out, err := w.resolveInvokeBody(ctx, testWorkspace,
			[]string{`export default () => ({ v: outer() })`}, nil)
		if err != nil {
			t.Fatalf("resolveInvokeBody: %v", err)
		}
		if len(out) != 1 || out[0] != `{"v":"in"}` {
			t.Fatalf("got %q, want [{\"v\":\"in\"}]", out)
		}
	})

	t.Run("an unrelated broken generator not reachable does not break the body", func(t *testing.T) {
		createGenerator(t, w, ctx, "broken", `export default () => "unterminated`)
		out, err := w.resolveInvokeBody(ctx, testWorkspace,
			[]string{`export default () => ({ v: outer() })`}, nil)
		if err != nil {
			t.Fatalf("resolveInvokeBody (unreachable broken generator): %v", err)
		}
		if len(out) != 1 || out[0] != `{"v":"in"}` {
			t.Fatalf("got %q, want [{\"v\":\"in\"}]", out)
		}
	})
}

func TestInvokeTypeScriptBody(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	port := echoTarget(t, w, ctx, startEchoServer)
	resp, err := w.Invoke(ctx, connect.NewRequest(&grpcviewv1.InvokeRequest{
		Spec: &grpcviewv1.InvokeSpec{
			WorkspaceName: testWorkspace,
			Service:       echoService,
			Method:        "Unary",
			Target:        &grpcviewv1.Server{Address: fmt.Sprintf("127.0.0.1:%d", port)},
		},
		Body: `export default () => ({ message: "hi-" + Math.random() })`,
	}))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
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
	if !strings.HasPrefix(payload.Message, "echo: hi-") {
		t.Fatalf("echoed message = %q, want it to start with %q", payload.Message, "echo: hi-")
	}
}
