package scripting

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

//go:embed testdata/npm
var npmFixtures embed.FS

func materializeNpm(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	err := fs.WalkDir(npmFixtures, "testdata/npm", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel("testdata/npm", p)
		if err != nil {
			return err
		}
		dst := filepath.Join(root, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, err := npmFixtures.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
	if err != nil {
		t.Fatalf("materialize npm fixtures: %v", err)
	}
	return root
}

func npmEngine(t *testing.T) (*Engine, string) {
	t.Helper()
	root := materializeNpm(t)
	e := newEngine(t, WithNodePaths(root), WithResolveDir(root))
	return e, root
}

func mustRun(t *testing.T, e *Engine, src string, in Input) Result {
	t.Helper()
	res, err := e.RunScenario(context.Background(), src, Grant{}, in)
	if err != nil {
		t.Fatalf("run %q: %v", src, err)
	}
	return res
}

func assertValue(t *testing.T, res Result, want string) {
	t.Helper()
	if string(res.Value) != want {
		t.Fatalf("value = %s, want %s", res.Value, want)
	}
}

func TestBundleNpmLibraries(t *testing.T) {
	e, _ := npmEngine(t)

	t.Run("ms/parse", func(t *testing.T) {
		assertValue(t, mustRun(t, e, `import ms from "ms"; ms("2 days")`, Input{}), "172800000")
		assertValue(t, mustRun(t, e, `import ms from "ms"; ms("1h")`, Input{}), "3600000")
	})

	t.Run("dayjs/date-math", func(t *testing.T) {
		src := `import dayjs from "dayjs"; dayjs("2024-03-14").add(1, "day").format("YYYY-MM-DD")`
		assertValue(t, mustRun(t, e, src, Input{}), `"2024-03-15"`)
	})

	t.Run("mustache/render-with-input", func(t *testing.T) {
		src := `import Mustache from "mustache";
import { params } from "grpcview:request";
Mustache.render("Hello {{name}}!", { name: params.who })`
		in := Input{Params: map[string]any{"who": "gRPC"}}
		assertValue(t, mustRun(t, e, src, in), `"Hello gRPC!"`)
	})

	t.Run("combined/two-deps-and-params", func(t *testing.T) {
		src := `import dayjs from "dayjs";
import ms from "ms";
import { params } from "grpcview:request";
({ ttlMs: ms(params.ttl), day: dayjs("2024-01-01").add(1, "month").format("YYYY-MM") })`
		in := Input{Params: map[string]any{"ttl": "15m"}}
		assertValue(t, mustRun(t, e, src, in), `{"ttlMs":900000,"day":"2024-02"}`)
	})
}

func TestProductionEngineBundlesEmbeddedNpm(t *testing.T) {
	e := newEngine(t)

	t.Run("vendored-dayjs-resolves-and-runs", func(t *testing.T) {
		src := `import dayjs from "dayjs"; dayjs("2024-03-14").add(1, "day").format("YYYY-MM-DD")`
		assertValue(t, mustRun(t, e, src, Input{}), `"2024-03-15"`)
	})

	t.Run("non-vendored-package-still-fails", func(t *testing.T) {
		_, err := e.RunScenario(context.Background(), `import _ from "lodash"; _`, Grant{}, Input{})
		if err == nil {
			t.Fatal("importing a non-vendored package: got nil error, want a bundle failure")
		}
		if !strings.Contains(err.Error(), "bundle failed") || !strings.Contains(err.Error(), "lodash") {
			t.Fatalf("error = %q, want it to mention the bundle failure and the missing package", err)
		}
	})
}

func TestBundleRelativeTypeScriptModule(t *testing.T) {
	e, root := npmEngine(t)
	writeFile(t, filepath.Join(root, "helper.ts"),
		"export const greet = (who: string): string => `hello ${who}`;\n")

	src := `import { greet } from "./helper"; import { params } from "grpcview:request"; greet(params.who)`
	in := Input{Params: map[string]any{"who": "world"}}
	assertValue(t, mustRun(t, e, src, in), `"hello world"`)
}

func TestBundleTypeScript(t *testing.T) {
	e := newEngine(t)
	cases := []struct{ name, src, want string }{
		{"typed-const", `const n: number = 20; const add = (a: number, b: number): number => a + b; add(n, 22)`, "42"},
		{"interface", `interface P { id: number } const p: P = { id: 7 }; p.id * 6`, "42"},
		{"as-cast", `const v = ("42" as unknown as string); Number(v)`, "42"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertValue(t, mustRun(t, e, c.src, Input{}), c.want)
		})
	}
}

func TestBundleUnknownPackageErrors(t *testing.T) {
	e, _ := npmEngine(t)
	_, err := e.RunScenario(context.Background(),
		`import x from "totally-not-a-real-pkg"; x`, Grant{}, Input{})
	if err == nil {
		t.Fatal("importing a nonexistent package: got nil error, want a bundle failure")
	}
	if !strings.Contains(err.Error(), "bundle failed") ||
		!strings.Contains(err.Error(), "totally-not-a-real-pkg") {
		t.Fatalf("error = %q, want it to mention the bundle failure and the missing package", err)
	}
}

func TestBundleErrorLineRemapped(t *testing.T) {
	t.Run("transform-path", func(t *testing.T) {
		e := newEngine(t)
		_, err := e.RunScenario(context.Background(),
			"\n\n\nthrow new Error(\"boom\")", Grant{}, Input{})
		var je *JSError
		if !errors.As(err, &je) {
			t.Fatalf("got %v, want *JSError", err)
		}
		if je.Line != 4 {
			t.Fatalf("line = %d, want 4 (author line; stack=%q)", je.Line, je.Stack)
		}
	})

	t.Run("bundle-path", func(t *testing.T) {
		e, _ := npmEngine(t)
		src := `import dayjs from "dayjs";
const now = dayjs("2024-01-01");
throw new Error("boom after dep");`
		_, err := e.RunScenario(context.Background(), src, Grant{}, Input{})
		var je *JSError
		if !errors.As(err, &je) {
			t.Fatalf("got %v, want *JSError", err)
		}
		if !strings.Contains(je.Message, "boom after dep") {
			t.Fatalf("message = %q, want it to contain the thrown text", je.Message)
		}
		if je.Line != 3 {
			t.Fatalf("line = %d, want 3 (author line through the inlined bundle; stack=%q)", je.Line, je.Stack)
		}
	})
}

func TestBundleCacheReuse(t *testing.T) {
	b := newBundler("", nil, "", "")
	const src = `6 * 7`

	c1, err := b.compile(src, Grant{}, "")
	if err != nil {
		t.Fatalf("first compile: %v", err)
	}
	key, ok := b.cacheKey(src, Grant{}, "", "expr")
	if !ok {
		t.Fatal("cacheKey not derivable for an empty grant")
	}
	if _, ok := b.cache.Load(key); !ok {
		t.Fatal("cache not populated after first compile")
	}

	b.cache.Store(key, cacheEntry{c: compiled{code: "SENTINEL"}})
	c2, err := b.compile(src, Grant{}, "")
	if err != nil {
		t.Fatalf("second compile: %v", err)
	}
	if c2.code != "SENTINEL" {
		t.Fatalf("second compile = %q, want the cached SENTINEL (cache not consulted)", c2.code)
	}
	if c1.code == "" {
		t.Fatal("first compile produced empty code")
	}
}

func TestPathSigilResolution(t *testing.T) {
	wsRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(wsRoot, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(wsRoot, "lib", "x.ts"), `export const x = 42;`)

	collRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(collRoot, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(collRoot, "scripts", "y.ts"), `export const y = 10;`)

	e := newEngine(t, WithWorkspaceRoot(wsRoot))
	in := Input{CollectionRoot: collRoot}

	res, err := e.RunScenario(context.Background(),
		`import { x } from "@/lib/x"; import { y } from "~/scripts/y"; x + y`, Grant{}, in)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if string(res.Value) != "52" {
		t.Fatalf("value = %s, want 52", res.Value)
	}
}

func TestPathSigilContainmentGuard(t *testing.T) {
	// base/outside.ts sits one level above wsRoot (base/ws) and two levels above collRoot
	// (base/coll/nested), so it is a real, resolvable file that both escapes land on.
	base := t.TempDir()
	wsRoot := filepath.Join(base, "ws")
	collRoot := filepath.Join(base, "coll", "nested")
	if err := os.MkdirAll(wsRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(collRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(base, "outside.ts"), `export default "leaked";`)

	e := newEngine(t, WithWorkspaceRoot(wsRoot))

	t.Run("workspace-escape", func(t *testing.T) {
		_, err := e.RunScenario(context.Background(), `import x from "@/../outside"; x`,
			Grant{}, Input{CollectionRoot: collRoot})
		if err == nil || !strings.Contains(err.Error(), "resolves outside the workspace") {
			t.Fatalf("got %v, want a workspace containment error", err)
		}
	})

	t.Run("collection-escape", func(t *testing.T) {
		_, err := e.RunScenario(context.Background(), `import x from "~/../../outside"; x`,
			Grant{}, Input{CollectionRoot: collRoot})
		if err == nil || !strings.Contains(err.Error(), "resolves outside the collection") {
			t.Fatalf("got %v, want a collection containment error", err)
		}
	})
}

func TestPathSigilNoCollectionRoot(t *testing.T) {
	wsRoot := t.TempDir()
	e := newEngine(t, WithWorkspaceRoot(wsRoot))

	_, err := e.RunScenario(context.Background(), `import x from "~/scripts/y"; x`, Grant{}, Input{})
	if err == nil || !strings.Contains(err.Error(), "no collection root for this run") {
		t.Fatalf("got %v, want a clear no-collection-root error", err)
	}
}

func TestPathSigilInExpressionPosition(t *testing.T) {
	wsRoot := t.TempDir()
	writeFile(t, filepath.Join(wsRoot, "x.ts"), `export default 42;`)
	e := newEngine(t, WithWorkspaceRoot(wsRoot))

	// Mirrors what the body/metadata hidden wrapper produces for an expression-position source.
	body := WrapExpression(`require("@/x").default`)
	res, err := e.RunRequestBody(context.Background(), body, Grant{}, Input{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if string(res.Value) != "42" {
		t.Fatalf("value = %s, want 42", res.Value)
	}
}

func TestBundleCacheInvalidatesOnImportedFileChange(t *testing.T) {
	wsRoot := t.TempDir()
	libPath := filepath.Join(wsRoot, "lib.ts")
	writeFile(t, libPath, `export const v = 1;`)
	e := newEngine(t, WithWorkspaceRoot(wsRoot))
	const src = `import { v } from "@/lib"; v`

	res1, err := e.RunScenario(context.Background(), src, Grant{}, Input{})
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if string(res1.Value) != "1" {
		t.Fatalf("first value = %s, want 1", res1.Value)
	}

	writeFile(t, libPath, `export const v = 2;`)
	res2, err := e.RunScenario(context.Background(), src, Grant{}, Input{})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if string(res2.Value) != "2" {
		t.Fatalf("second value = %s, want 2 (cache must invalidate on an edited import)", res2.Value)
	}
}

func TestComputedImportSpecifierRejected(t *testing.T) {
	e := newEngine(t)
	for _, tc := range []struct{ name, src string }{
		{"require-computed", `const p = "ms"; require(p)`},
		{"dynamic-import-computed", `const p = "ms"; import(p)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := e.RunScenario(context.Background(), tc.src, Grant{}, Input{})
			if err == nil || !strings.Contains(err.Error(), "computed import specifier") {
				t.Fatalf("got %v, want a computed-specifier rejection", err)
			}
		})
	}
}

func TestLiteralImportSpecifierNotRejected(t *testing.T) {
	e, _ := npmEngine(t)
	for _, tc := range []struct{ name, src string }{
		{"require-literal", `require("ms")`},
		{"dynamic-import-literal", `import("ms").then(m => "ok")`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := e.RunScenario(context.Background(), tc.src, Grant{}, Input{})
			if err != nil && strings.Contains(err.Error(), "computed import specifier") {
				t.Fatalf("literal specifier wrongly rejected: %v", err)
			}
		})
	}
}

// Regression for the folder-metadata script that shipped in example/: rejectComputedImports
// used to scan raw source text, so a comment merely mentioning `require(...)` tripped the
// computed-import rejection even though the actual call two lines down is a string literal.
func TestComputedImportsCommentNotRejected(t *testing.T) {
	e := newEngine(t)
	// Expression-position source, same as folder-metadata scripts author it; the hidden wrapper
	// (WrapExpression) is what turns the bare object literal into a valid expression.
	src := "{\n" +
		"  // Expression form: there is no `export default` here, so this object IS the\n" +
		"  // whole script. An `import` statement cannot stand in expression position, so\n" +
		"  // the module comes in through `require(...)` instead — same resolver, other\n" +
		"  // grammar. Specifiers must be string literals either way.\n" +
		"  ...require(\"grpcview:metadata\").inherit(),\n" +
		"  \"x-demo-folder\": [\"streaming\"],\n" +
		"}"
	res, err := e.RunRequestBody(context.Background(), WrapExpression(src), Grant{}, Input{})
	if err != nil {
		t.Fatalf("comment mentioning require(...) wrongly rejected: %v", err)
	}
	if !strings.Contains(string(res.Value), "x-demo-folder") {
		t.Fatalf("value = %s, want it to contain x-demo-folder", res.Value)
	}
}

func TestRejectComputedImports(t *testing.T) {
	accept := []struct{ name, src string }{
		{"require-simple", `require("x")`},
		{"require-space-before-paren", `require ("x")`},
		{"require-multiline", "require(\n  \"x\"\n)"},
		{"dynamic-import-literal", `import("x")`},
		{"string-contains-syntax", `const s = "require(x)"`},
		{"block-comment", `/* require(x) */`},
		{"escaped-quote-in-string", `"a \" require(b)"`},
	}
	for _, tc := range accept {
		t.Run(tc.name, func(t *testing.T) {
			if err := rejectComputedImports(tc.src); err != nil {
				t.Fatalf("rejectComputedImports(%q) = %v, want nil", tc.src, err)
			}
		})
	}

	reject := []struct{ name, src string }{
		{"require-identifier", `require(p)`},
		{"import-identifier", `import(specifier)`},
		{"require-ternary", `require(cond ? "a" : "b")`},
	}
	for _, tc := range reject {
		t.Run(tc.name, func(t *testing.T) {
			if err := rejectComputedImports(tc.src); err == nil {
				t.Fatalf("rejectComputedImports(%q) = nil, want a rejection", tc.src)
			}
		})
	}
}

func TestMaskLiteralsUnterminated(t *testing.T) {
	cases := []struct{ name, src string }{
		{"unterminated-block-comment", "/* never closed require(x)"},
		{"unterminated-string", `"never closed require(x)`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			masked := maskLiterals(tc.src)
			if len(masked) != len(tc.src) {
				t.Fatalf("maskLiterals(%q) length = %d, want %d", tc.src, len(masked), len(tc.src))
			}
			if err := rejectComputedImports(tc.src); err != nil {
				t.Fatalf("rejectComputedImports(%q) = %v, want nil (unterminated literal masks to EOF)", tc.src, err)
			}
		})
	}
}

func TestDecodeVLQ(t *testing.T) {
	cases := []struct {
		in   string
		want []int
	}{
		{"A", []int{0}},
		{"AAAA", []int{0, 0, 0, 0}},
		{"AACA", []int{0, 0, 1, 0}},
		{"D", []int{-1}},
		{"gB", []int{16}},
	}
	for _, c := range cases {
		got, err := decodeVLQ(c.in)
		if err != nil {
			t.Fatalf("decodeVLQ(%q): %v", c.in, err)
		}
		if len(got) != len(c.want) {
			t.Fatalf("decodeVLQ(%q) = %v, want %v", c.in, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("decodeVLQ(%q) = %v, want %v", c.in, got, c.want)
			}
		}
	}
	if _, err := decodeVLQ("!"); err == nil {
		t.Fatal("decodeVLQ(\"!\"): got nil error, want a bad-VLQ-char error")
	}
}
