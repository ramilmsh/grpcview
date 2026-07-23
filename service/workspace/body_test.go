package workspace

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

// TestResolveInvokeBody covers the §T1 pre-send body-evaluation step: the TYPESCRIPT
// happy path (a body is run as a generator and its returned object replaces the body,
// for one and for many bodies), the JSON/UNSPECIFIED no-op (bodies pass through
// byte-identical and are never evaluated), and the error modes (throw / non-object /
// undefined return → FailedPrecondition).
func TestResolveInvokeBody(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()

	t.Run("typescript body evaluates to its returned object", func(t *testing.T) {
		// `export default` fires the entry-point convention; the returned object literal
		// (with a computed field, to prove real evaluation) becomes the JSON body.
		out, err := w.resolveInvokeBody(ctx, testWorkspace,
			[]string{`export default () => ({ message: "hi-" + (1 + 1) })`},
			grpcviewv1.BodyLanguage_BODY_LANGUAGE_TYPESCRIPT)
		if err != nil {
			t.Fatalf("resolveInvokeBody: %v", err)
		}
		if len(out) != 1 || out[0] != `{"message":"hi-2"}` {
			t.Fatalf("got %q, want [{\"message\":\"hi-2\"}]", out)
		}
	})

	t.Run("typescript evaluates every body (streaming shape)", func(t *testing.T) {
		out, err := w.resolveInvokeBody(ctx, testWorkspace,
			[]string{`export default () => ({ n: 1 })`, `export default () => ({ n: 2 })`},
			grpcviewv1.BodyLanguage_BODY_LANGUAGE_TYPESCRIPT)
		if err != nil {
			t.Fatalf("resolveInvokeBody: %v", err)
		}
		if len(out) != 2 || out[0] != `{"n":1}` || out[1] != `{"n":2}` {
			t.Fatalf("got %q, want [{\"n\":1} {\"n\":2}]", out)
		}
	})

	// JSON and UNSPECIFIED are both no-ops: the bodies are returned byte-identical and
	// are NEVER evaluated — a body that is not even valid JS proves the eval is skipped.
	for _, lang := range []grpcviewv1.BodyLanguage{
		grpcviewv1.BodyLanguage_BODY_LANGUAGE_JSON,
		grpcviewv1.BodyLanguage_BODY_LANGUAGE_UNSPECIFIED,
	} {
		t.Run("no-op for "+lang.String(), func(t *testing.T) {
			in := []string{`{"a": 1, "b": "text with } braces {"}`, `not valid js {{ still passes through`}
			out, err := w.resolveInvokeBody(ctx, testWorkspace, in, lang)
			if err != nil {
				t.Fatalf("resolveInvokeBody: %v", err)
			}
			if len(out) != len(in) {
				t.Fatalf("got %d bodies, want %d", len(out), len(in))
			}
			for i := range in {
				if out[i] != in[i] {
					t.Fatalf("body[%d] = %q, want byte-identical %q", i, out[i], in[i])
				}
			}
		})
	}

	for _, c := range []struct{ name, body string }{
		{"throwing body", `export default () => { throw new Error("boom") }`},
		{"number return is not an object", `export default () => 42`},
		{"array return is not an object", `export default () => [1, 2, 3]`},
		{"string return is not an object", `export default () => "just a string"`},
		{"undefined return is not an object", `export default () => undefined`},
	} {
		t.Run(c.name+" errors FailedPrecondition", func(t *testing.T) {
			if _, err := w.resolveInvokeBody(ctx, testWorkspace,
				[]string{c.body}, grpcviewv1.BodyLanguage_BODY_LANGUAGE_TYPESCRIPT); connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("code = %v, want FailedPrecondition (err=%v)", connect.CodeOf(err), err)
			}
		})
	}
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
