package scripting

import (
	"context"
	"strings"
	"testing"
)

func TestGeneratorEntryPoint(t *testing.T) {
	e := newEngine(t)
	cases := []struct {
		name, src string
		args      []any
		want      string
	}{
		{"sync-args", `export default (a, b) => a + b`, []any{20, 22}, "42"},
		{"async-args", `export default async (x) => x * 2`, []any{21}, "42"},
		{"no-args", `export default () => ({ ok: true })`, nil, `{"ok":true}`},
		{"json-round-trip", `export default (o) => ({ echo: o, n: o.a + 1 })`, []any{map[string]any{"a": 1}}, `{"echo":{"a":1},"n":2}`},
		{"const-default", `const gen = () => "hi"; export default gen`, nil, `"hi"`},
		{"fallback-expr", `1 + 1`, nil, "2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := e.runGenerator(context.Background(), c.src, Grant{}, Input{Args: c.args})
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if string(res.Value) != c.want {
				t.Fatalf("value = %s, want %s", res.Value, c.want)
			}
		})
	}
}

func TestGeneratorArgsVaryPerRun(t *testing.T) {
	e := newEngine(t)
	const src = `export default (x) => x * 10`
	r1, err := e.runGenerator(context.Background(), src, Grant{}, Input{Args: []any{2}})
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	r2, err := e.runGenerator(context.Background(), src, Grant{}, Input{Args: []any{5}})
	if err != nil {
		t.Fatalf("run 5: %v", err)
	}
	if string(r1.Value) != "20" || string(r2.Value) != "50" {
		t.Fatalf("args not threaded per run: %s / %s, want 20 / 50", r1.Value, r2.Value)
	}
}

func TestMiddlewareEntryPoint(t *testing.T) {
	e := newEngine(t)
	in := Input{Request: RequestInput{
		Body:     map[string]any{"n": float64(1)},
		Metadata: map[string]string{"authorization": "Bearer t"},
		Target:   "localhost:10000",
	}}
	cases := []struct {
		name, src, want string
	}{
		{
			"handle-mutates-metadata",
			`export function handle(ctx) { ctx.metadata["x-signature"] = "sig"; return ctx; }`,
			`{"body":{"n":1},"metadata":{"authorization":"Bearer t","x-signature":"sig"},"target":"localhost:10000"}`,
		},
		{
			"handle-mutates-body-field",
			`export function handle(ctx) { ctx.body.n = 2; return ctx; }`,
			`{"body":{"n":2},"metadata":{"authorization":"Bearer t"},"target":"localhost:10000"}`,
		},
		{
			"async-default-rewrites-target",
			`export default async (ctx) => { ctx.target = "other:1"; return ctx; }`,
			`{"body":{"n":1},"metadata":{"authorization":"Bearer t"},"target":"other:1"}`,
		},
		{
			"in-place-no-return-falls-back-to-ctx",
			`export function handle(ctx) { ctx.body = { replaced: true }; }`,
			`{"body":{"replaced":true},"metadata":{"authorization":"Bearer t"},"target":"localhost:10000"}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := e.RunMiddleware(context.Background(), c.src, Grant{}, in)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if string(res.Value) != c.want {
				t.Fatalf("value = %s, want %s", res.Value, c.want)
			}
		})
	}

	res, err := e.RunMiddleware(context.Background(), `({ passthrough: true })`, Grant{}, in)
	if err != nil {
		t.Fatalf("fallback run: %v", err)
	}
	if string(res.Value) != `{"passthrough":true}` {
		t.Fatalf("fallback value = %s, want {\"passthrough\":true}", res.Value)
	}
}

func TestEntryExportDetection(t *testing.T) {
	defaults := []string{
		`export default () => 1`,
		`  export default function () {}`,
		"const f = 1;\nexport default f",
		"// mentions export default in a comment above the real one\nexport default () => 1",
	}
	for _, s := range defaults {
		if !hasDefaultExport(s) {
			t.Errorf("hasDefaultExport(%q) = false, want true", s)
		}
	}
	handles := []string{
		`export function handle(ctx) { return ctx; }`,
		`export async function handle(ctx) {}`,
		`const handle = (ctx) => ctx; export { handle }`,
	}
	for _, s := range handles {
		if !hasHandleOrDefaultExport(s) {
			t.Errorf("hasHandleOrDefaultExport(%q) = false, want true", s)
		}
	}
	for _, s := range []string{`1 + 1`, `({ ok: true })`, `const x = 5; x`} {
		if hasDefaultExport(s) || hasHandleOrDefaultExport(s) {
			t.Errorf("plain expression %q wrongly detected as an entry point", s)
		}
	}
}

// Regression for the example/ folder-metadata script: `export default` mentioned inside a comment,
// a block comment, or a string literal must not make an expression look like a module.
func TestEntryExportDetectionIgnoresCommentsAndStrings(t *testing.T) {
	notModule := []string{
		"// export default foo\n({ ok: true })",
		"/* export default */\n({ ok: true })",
		`const s = "export default x"; ({ ok: true })`,
	}
	for _, s := range notModule {
		if hasDefaultExport(s) {
			t.Errorf("hasDefaultExport(%q) = true, want false (text is in a comment or string)", s)
		}
	}

	notHandle := []string{
		"// export function handle(ctx) {}\n({ ok: true })",
		"/* export const handle = 1 */\n({ ok: true })",
		`const s = "export function handle() {}"; ({ ok: true })`,
	}
	for _, s := range notHandle {
		if hasHandleOrDefaultExport(s) {
			t.Errorf("hasHandleOrDefaultExport(%q) = true, want false (text is in a comment or string)", s)
		}
	}

	realModule := "// export default is used below\nexport default { ok: true }"
	if !hasDefaultExport(realModule) {
		t.Errorf("hasDefaultExport(%q) = false, want true (real export default follows a comment mentioning it)", realModule)
	}

	realHandle := "// this script exports a handle\nexport function handle(ctx) { return ctx; }"
	if !hasHandleOrDefaultExport(realHandle) {
		t.Errorf("hasHandleOrDefaultExport(%q) = false, want true (real handle export follows a comment mentioning it)", realHandle)
	}
}

// The exact example/ folder-metadata script, reproduced verbatim: a comment mentioning
// `export default` must not stop the bare object literal from being wrapped and run.
func TestFolderMetadataCommentMentioningExportDefaultNotRejected(t *testing.T) {
	e := newEngine(t)
	src := "{\n" +
		"  // Expression form: there is no `export default` here, so this object IS the\n" +
		"  // whole script. An `import` statement cannot stand in expression position, so\n" +
		"  // the module comes in through `require(...)` instead — same resolver, other\n" +
		"  // grammar. Specifiers must be string literals either way.\n" +
		"  ...require(\"grpcview:metadata\").inherit(),\n" +
		"  \"x-demo-folder\": [\"streaming\"],\n" +
		"}"
	if hasDefaultExport(src) {
		t.Fatalf("hasDefaultExport wrongly true for a comment mentioning export default")
	}
	res, err := e.RunRequestBody(context.Background(), WrapExpression(src), Grant{}, Input{})
	if err != nil {
		t.Fatalf("comment mentioning export default wrongly rejected: %v", err)
	}
	if !strings.Contains(string(res.Value), "x-demo-folder") {
		t.Fatalf("value = %s, want it to contain x-demo-folder", res.Value)
	}
}
