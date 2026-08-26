package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/store"
)

func TestResolveInvokeBody(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()

	t.Run("typescript body evaluates to its returned object", func(t *testing.T) {
		out, err := w.resolveInvokeBody(ctx, testWorkspace,
			[]string{`export default () => ({ message: "hi-" + (1 + 1) })`}, nil, "")
		if err != nil {
			t.Fatalf("resolveInvokeBody: %v", err)
		}
		if len(out) != 1 || out[0] != `{"message":"hi-2"}` {
			t.Fatalf("got %q, want [{\"message\":\"hi-2\"}]", out)
		}
	})

	t.Run("typescript evaluates every body (streaming shape)", func(t *testing.T) {
		out, err := w.resolveInvokeBody(ctx, testWorkspace,
			[]string{`export default () => ({ n: 1 })`, `export default () => ({ n: 2 })`}, nil, "")
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
				[]string{c.body}, nil, ""); connect.CodeOf(err) != connect.CodeFailedPrecondition {
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
			out, err := w.resolveInvokeBody(ctx, testWorkspace, []string{c.body}, nil, "")
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
				[]string{c.body}, nil, ""); connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("code = %v, want FailedPrecondition (err=%v)", connect.CodeOf(err), err)
			}
		})
	}
}

func TestResolveInvokeBodyComposition(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	writeScript(t, w, ctx, "scripts/mkid.ts", `export default () => "id-42"`)

	t.Run("typescript body imports and calls a saved script", func(t *testing.T) {
		out, err := w.resolveInvokeBody(ctx, testWorkspace, []string{
			"import mkid from \"#/scripts/mkid.ts\";\n" +
				`export default () => ({ id: mkid(), n: 7 })`,
		}, nil, "")
		if err != nil {
			t.Fatalf("resolveInvokeBody: %v", err)
		}
		if len(out) != 1 || out[0] != `{"id":"id-42","n":7}` {
			t.Fatalf("got %q, want [{\"id\":\"id-42\",\"n\":7}]", out)
		}
	})

	t.Run("an expression body composes a saved script via require", func(t *testing.T) {
		out, err := w.resolveInvokeBody(ctx, testWorkspace,
			[]string{`{ "id": require("#/scripts/mkid.ts").default() }`}, nil, "")
		if err != nil {
			t.Fatalf("resolveInvokeBody: %v", err)
		}
		if len(out) != 1 || out[0] != `{"id":"id-42"}` {
			t.Fatalf("got %q, want [{\"id\":\"id-42\"}]", out)
		}
	})

	t.Run("an unreferenced broken script does not break the body", func(t *testing.T) {
		writeScript(t, w, ctx, "scripts/broken.ts", `export default () => "unterminated`)
		out, err := w.resolveInvokeBody(ctx, testWorkspace, []string{
			"import mkid from \"#/scripts/mkid.ts\";\n" +
				`export default () => ({ id: mkid() })`,
		}, nil, "")
		if err != nil {
			t.Fatalf("resolveInvokeBody (unreferenced broken script): %v", err)
		}
		if len(out) != 1 || out[0] != `{"id":"id-42"}` {
			t.Fatalf("got %q, want [{\"id\":\"id-42\"}]", out)
		}
	})
}

func TestResolveInvokeBodyTransitiveComposition(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)
	writeScript(t, w, ctx, "scripts/inner.ts", `export default () => "in"`)
	writeScript(t, w, ctx, "scripts/outer.ts",
		"import inner from \"#/scripts/inner.ts\";\n"+`export default () => inner()`)

	t.Run("body importing outer resolves the transitive import chain", func(t *testing.T) {
		out, err := w.resolveInvokeBody(ctx, testWorkspace, []string{
			"import outer from \"#/scripts/outer.ts\";\n" +
				`export default () => ({ v: outer() })`,
		}, nil, "")
		if err != nil {
			t.Fatalf("resolveInvokeBody: %v", err)
		}
		if len(out) != 1 || out[0] != `{"v":"in"}` {
			t.Fatalf("got %q, want [{\"v\":\"in\"}]", out)
		}
	})

	t.Run("an unrelated broken script not reachable does not break the body", func(t *testing.T) {
		writeScript(t, w, ctx, "scripts/broken.ts", `export default () => "unterminated`)
		out, err := w.resolveInvokeBody(ctx, testWorkspace, []string{
			"import outer from \"#/scripts/outer.ts\";\n" +
				`export default () => ({ v: outer() })`,
		}, nil, "")
		if err != nil {
			t.Fatalf("resolveInvokeBody (unreachable broken script): %v", err)
		}
		if len(out) != 1 || out[0] != `{"v":"in"}` {
			t.Fatalf("got %q, want [{\"v\":\"in\"}]", out)
		}
	})
}

func TestResolveInvokeBodyImportsAcrossCollections(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	other, err := w.store.Open(ctx, "other")
	if err != nil {
		t.Fatalf("Open other: %v", err)
	}
	if err := other.Create(ctx, ""); err != nil {
		t.Fatalf("Create other: %v", err)
	}
	if err := other.CreateScript(ctx, "scripts/shared.ts"); err != nil {
		t.Fatalf("CreateScript: %v", err)
	}
	src := `export default () => "shared-42"`
	if err := other.UpdateScript(ctx, "scripts/shared.ts", store.ScriptPatch{Source: &src}); err != nil {
		t.Fatalf("UpdateScript: %v", err)
	}

	out, err := w.resolveInvokeBody(ctx, testWorkspace, []string{
		"import shared from \"@/other/scripts/shared.ts\";\n" +
			`export default () => ({ id: shared() })`,
	}, nil, "")
	if err != nil {
		t.Fatalf("resolveInvokeBody (cross-collection @/ import): %v", err)
	}
	if len(out) != 1 || out[0] != `{"id":"shared-42"}` {
		t.Fatalf("got %q, want [{\"id\":\"shared-42\"}]", out)
	}
}

func TestInvokeTypeScriptBody(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	ctx := context.Background()
	ensureWorkspace(t, w, ctx)

	port := echoTarget(t, w, ctx, startEchoServer)
	resp, err := w.Invoke(ctx, connect.NewRequest(&grpcviewv1.InvokeRequest{
		Spec: &grpcviewv1.InvokeSpec{
			Collection: testWorkspace,
			Service:    echoService,
			Method:     "Unary",
			Target:     &grpcviewv1.Server{Address: fmt.Sprintf("127.0.0.1:%d", port)},
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
