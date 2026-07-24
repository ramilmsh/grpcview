package workspace

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

// TestResolveInvokeBody covers the §T1 pre-send body-evaluation step: the happy path (a body
// is run as a generator and its returned object replaces the body, for one and for many
// bodies) and the error modes (throw / non-object / undefined return → FailedPrecondition).
func TestResolveInvokeBody(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()

	t.Run("typescript body evaluates to its returned object", func(t *testing.T) {
		// `export default` fires the entry-point convention; the returned object literal
		// (with a computed field, to prove real evaluation) becomes the JSON body.
		out, err := w.resolveInvokeBody(ctx, testWorkspace,
			[]string{`export default () => ({ message: "hi-" + (1 + 1) })`})
		if err != nil {
			t.Fatalf("resolveInvokeBody: %v", err)
		}
		if len(out) != 1 || out[0] != `{"message":"hi-2"}` {
			t.Fatalf("got %q, want [{\"message\":\"hi-2\"}]", out)
		}
	})

	t.Run("typescript evaluates every body (streaming shape)", func(t *testing.T) {
		out, err := w.resolveInvokeBody(ctx, testWorkspace,
			[]string{`export default () => ({ n: 1 })`, `export default () => ({ n: 2 })`})
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
				[]string{c.body}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("code = %v, want FailedPrecondition (err=%v)", connect.CodeOf(err), err)
			}
		})
	}
}

// TestResolveInvokeBodyComposition covers pillar C on the invoke path (ts-request-body-plan
// T3): a TYPESCRIPT body calls a generator saved in the workspace (via the store), and the
// produced JSON reflects the composed call. It also proves FAILURE ISOLATION — a broken
// generator the body does NOT reference cannot break the body — because referencedGenerators
// bounds each per-invoke bundle to the generators the body actually names.
func TestResolveInvokeBodyComposition(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	createGenerator(t, w, ctx, "mkid", `export default () => "id-42"`)

	t.Run("typescript body composes a saved generator", func(t *testing.T) {
		out, err := w.resolveInvokeBody(ctx, testWorkspace,
			[]string{`export default () => ({ id: mkid(), n: 7 })`})
		if err != nil {
			t.Fatalf("resolveInvokeBody: %v", err)
		}
		if len(out) != 1 || out[0] != `{"id":"id-42","n":7}` {
			t.Fatalf("got %q, want [{\"id\":\"id-42\",\"n\":7}]", out)
		}
	})

	t.Run("an unreferenced broken generator does not break the body", func(t *testing.T) {
		// A generator whose source does not compile lives in the workspace, but the body never
		// names it — so referencedGenerators excludes it and the body still bundles and runs.
		createGenerator(t, w, ctx, "broken", `export default () => "unterminated`)
		out, err := w.resolveInvokeBody(ctx, testWorkspace,
			[]string{`export default () => ({ id: mkid() })`})
		if err != nil {
			t.Fatalf("resolveInvokeBody (unreferenced broken generator): %v", err)
		}
		if len(out) != 1 || out[0] != `{"id":"id-42"}` {
			t.Fatalf("got %q, want [{\"id\":\"id-42\"}]", out)
		}
	})

	t.Run("a broken generator whose name collides with a key or method does not break the body", func(t *testing.T) {
		// The regression the call-site scan closes: a broken generator named like a common object
		// key (id) or method (toString) must NOT be folded into the bundle when the body only USES
		// those as a key / method — it calls mkid(), never id() or toString(). A bare-identifier
		// scan matched the key/method tokens, pulled the broken generators in, and failed the whole
		// (valid) body; the call-site scan excludes a non-call occurrence, so the body still runs.
		createGenerator(t, w, ctx, "id", `export default () => "unterminated`)
		createGenerator(t, w, ctx, "toString", `export default () => "also broken`)
		out, err := w.resolveInvokeBody(ctx, testWorkspace,
			[]string{`export default () => ({ id: mkid(), label: (7).toString() })`})
		if err != nil {
			t.Fatalf("resolveInvokeBody (name-collision isolation): %v", err)
		}
		if len(out) != 1 || out[0] != `{"id":"id-42","label":"7"}` {
			t.Fatalf("got %q, want [{\"id\":\"id-42\",\"label\":\"7\"}]", out)
		}
	})
}

// TestInvokeTypeScriptBody is the §T1 must-pass end-to-end: a unary Invoke with
// body_language=TYPESCRIPT and the plan's example body runs the body as a generator,
// the returned object unmarshals into the request message, and the echo server sees
// it — proving the produced JSON flows through UnmarshalJSON and the send unchanged.
func TestInvokeTypeScriptBody(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	port := startEchoServer(t)
	resp, err := w.Invoke(ctx, connect.NewRequest(&grpcviewv1.InvokeRequest{
		WorkspaceName: testWorkspace,
		Service:       echoService,
		Method:        "Unary",
		Body:          `export default () => ({ message: "hi-" + Math.random() })`,
		BodyLanguage:  grpcviewv1.BodyLanguage_BODY_LANGUAGE_TYPESCRIPT,
		Target:        &grpcviewv1.Server{Host: "127.0.0.1", Port: int32(port)},
	}))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if code := resp.Msg.GetResponse().GetStatus().GetCode(); code != int32(codeOK) {
		t.Fatalf("status = %d (%s)", code, resp.Msg.GetResponse().GetStatus().GetMessage())
	}
	// Echo replies "echo: " + message; the TS body produced {"message":"hi-<random>"}.
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
