package scripting

import (
	"context"
	"errors"
	"testing"
)

// TestRunRequestBody covers pillar C (composition, ts-request-body-plan T3): a TypeScript
// request body calling the workspace's saved generators as ambient globals. It asserts on
// Result.Value (raw JSON), and covers the no-generator fallback and the failure-surfaces path.
// newEngine is the production construction (embedded npm registry), so a generator's own bare
// import (dayjs) resolves transitively.
func TestRunRequestBody(t *testing.T) {
	e := newEngine(t)
	ctx := context.Background()

	t.Run("body calls a saved generator", func(t *testing.T) {
		gens := map[string]string{"mkmsg": `export default () => 42`}
		res, err := e.RunRequestBody(ctx, `export default () => ({ x: mkmsg() })`, gens, Grant{}, Input{})
		if err != nil {
			t.Fatalf("RunRequestBody: %v", err)
		}
		if string(res.Value) != `{"x":42}` {
			t.Fatalf("value = %s, want {\"x\":42}", res.Value)
		}
	})

	t.Run("generator receives args from the body", func(t *testing.T) {
		gens := map[string]string{"dbl": `export default (n) => n * 2`}
		res, err := e.RunRequestBody(ctx, `export default () => ({ n: dbl(21) })`, gens, Grant{}, Input{})
		if err != nil {
			t.Fatalf("RunRequestBody: %v", err)
		}
		if string(res.Value) != `{"n":42}` {
			t.Fatalf("value = %s, want {\"n\":42}", res.Value)
		}
	})

	t.Run("a generator's own npm import is inlined transitively", func(t *testing.T) {
		// The generator imports dayjs (from the embedded registry) and the body calls it, so
		// the whole graph — body -> generator -> dayjs — must bundle and run. This exercises the
		// registry plugin resolving a bare import from WITHIN the generator namespace.
		gens := map[string]string{
			"stamp": `import dayjs from "dayjs"; export default () => dayjs("2024-03-14").add(1, "day").format("YYYY-MM-DD")`,
		}
		res, err := e.RunRequestBody(ctx, `export default () => ({ when: stamp() })`, gens, Grant{}, Input{})
		if err != nil {
			t.Fatalf("RunRequestBody: %v", err)
		}
		if string(res.Value) != `{"when":"2024-03-15"}` {
			t.Fatalf("value = %s, want {\"when\":\"2024-03-15\"}", res.Value)
		}
	})

	t.Run("no generators falls back to the plain body path", func(t *testing.T) {
		res, err := e.RunRequestBody(ctx, `export default () => ({ ok: true })`, map[string]string{}, Grant{}, Input{})
		if err != nil {
			t.Fatalf("RunRequestBody: %v", err)
		}
		if string(res.Value) != `{"ok":true}` {
			t.Fatalf("value = %s, want {\"ok\":true}", res.Value)
		}
	})

	t.Run("a broken generator the body calls surfaces the compile error", func(t *testing.T) {
		// The generator EXISTS but its source does not parse; esbuild fails to inline it, so the
		// composed compile fails and RunRequestBody returns a non-nil error (not a silent empty).
		gens := map[string]string{"broken": `export default () => "unterminated`}
		if _, err := e.RunRequestBody(ctx, `export default () => ({ x: broken() })`, gens, Grant{}, Input{}); err == nil {
			t.Fatal("want a non-nil error for a generator whose source does not compile, got nil")
		}
	})

	t.Run("a body runtime error maps back to the author line despite the prelude", func(t *testing.T) {
		// One simple-identifier generator => a 2-line synthetic prelude (authorPreludeLines=2).
		// The throw is on body line 3, which lands on composed-source line 5; the source map +
		// authorPreludeLines offset must report the BODY's line 3, not the composed line 5.
		gens := map[string]string{"mkmsg": `export default () => 42`}
		body := `export default () => {
	mkmsg();
	throw new Error("composed boom");
}`
		_, err := e.RunRequestBody(ctx, body, gens, Grant{}, Input{})
		var je *JSError
		if !errors.As(err, &je) {
			t.Fatalf("got %v, want *JSError", err)
		}
		if je.Line != 3 {
			t.Fatalf("line = %d, want 3 (author body line through the prelude offset; stack=%q)", je.Line, je.Stack)
		}
	})
}
