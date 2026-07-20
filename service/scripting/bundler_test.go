package scripting

// bundler_test.go — validation of the esbuild front end (engine Phase 2). It proves the
// three things the bundler adds over the raw evaluator, end to end through RunScenario:
//
//   - TypeScript transpiles and the last-expression result contract survives it;
//   - real npm libraries (dayjs, mustache, ms) and relative TS modules resolve, inline,
//     and run — with the frozen input globals still visible to the bundled code;
//   - a runtime error's line maps back to the AUTHOR's line through the assembled source
//     (prelude + banner + inlined dependencies), via the emitted source map.
//
// The npm libraries are vendored under testdata/npm and embedded into the test binary, so
// the validation is hermetic: no node_modules, no registry, no network. esbuild resolves
// them from a NodePaths root pointed at the materialized tree.

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

// materializeNpm writes the embedded testdata/npm tree to a temp dir and returns its path,
// preserving structure so esbuild's node resolver finds each package by `<root>/<pkg>`.
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

// npmEngine builds an Engine whose bundler resolves the vendored npm tree: NodePaths for
// bare imports (`ms`), ResolveDir for relative imports (`./helper`) written into root.
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

// TestBundleNpmLibraries is the headline validation: popular npm libraries a scripting
// agent would reach for — duration parsing (ms), date math (dayjs), templating (mustache)
// — bundle from a vendored tree and run, with results asserted byte-for-byte.
func TestBundleNpmLibraries(t *testing.T) {
	e, _ := npmEngine(t)

	t.Run("ms/parse", func(t *testing.T) {
		// CommonJS default export: `module.exports = fn`.
		assertValue(t, mustRun(t, e, `import ms from "ms"; ms("2 days")`, Input{}), "172800000")
		assertValue(t, mustRun(t, e, `import ms from "ms"; ms("1h")`, Input{}), "3600000")
	})

	t.Run("dayjs/date-math", func(t *testing.T) {
		// UMD default export; calendar add is TZ-independent for these tokens.
		src := `import dayjs from "dayjs"; dayjs("2024-03-14").add(1, "day").format("YYYY-MM-DD")`
		assertValue(t, mustRun(t, e, src, Input{}), `"2024-03-15"`)
	})

	t.Run("mustache/render-with-input", func(t *testing.T) {
		// ESM default export, fed the frozen `request` global — proving injected inputs
		// and a bundled dependency compose.
		src := `import Mustache from "mustache";
Mustache.render("Hello {{name}}!", { name: request.body.who })`
		in := Input{Request: RequestInput{Body: map[string]any{"who": "gRPC"}}}
		assertValue(t, mustRun(t, e, src, in), `"Hello gRPC!"`)
	})

	t.Run("combined/two-deps-and-vars", func(t *testing.T) {
		// Two dependencies in one graph, reading a var; object result keeps insertion order.
		src := `import dayjs from "dayjs";
import ms from "ms";
({ ttlMs: ms(vars.ttl), day: dayjs("2024-01-01").add(1, "month").format("YYYY-MM") })`
		in := Input{Vars: map[string]any{"ttl": "15m"}}
		assertValue(t, mustRun(t, e, src, in), `{"ttlMs":900000,"day":"2024-02"}`)
	})
}

// TestProductionEngineBundlesEmbeddedNpm is the PRODUCTION analogue of TestBundleNpmLibraries:
// it proves that the default Engine — constructed exactly as workspace.go does, with NO
// WithNodePaths/WithResolveDir — resolves and runs a bare npm import from the registry the
// Engine embeds and self-provisions (npm.go). This is the path the RunScript RPC drives;
// before the embedded registry it failed because production left NodePaths empty. (newEngine
// differs from workspace.go only in the outer page ceiling, which is immaterial to resolution.)
func TestProductionEngineBundlesEmbeddedNpm(t *testing.T) {
	e := newEngine(t) // no npm wiring — the production construction

	t.Run("vendored-dayjs-resolves-and-runs", func(t *testing.T) {
		src := `import dayjs from "dayjs"; dayjs("2024-03-14").add(1, "day").format("YYYY-MM-DD")`
		assertValue(t, mustRun(t, e, src, Input{}), `"2024-03-15"`)
	})

	t.Run("non-vendored-package-still-fails", func(t *testing.T) {
		// A package NOT in the embedded registry must still fail cleanly. This proves the
		// registry is a closed allowlist and the host's node_modules was never opened — a
		// bare import can reach only what we vendored, not arbitrary host code.
		_, err := e.RunScenario(context.Background(), `import _ from "lodash"; _`, Grant{}, Input{})
		if err == nil {
			t.Fatal("importing a non-vendored package: got nil error, want a bundle failure")
		}
		if !strings.Contains(err.Error(), "bundle failed") || !strings.Contains(err.Error(), "lodash") {
			t.Fatalf("error = %q, want it to mention the bundle failure and the missing package", err)
		}
	})
}

// TestBundleRelativeTypeScriptModule: a relative import of a .ts helper resolves against
// ResolveDir, transpiles, inlines, and runs — the local-module story for scenarios.
func TestBundleRelativeTypeScriptModule(t *testing.T) {
	e, root := npmEngine(t)
	writeFile(t, filepath.Join(root, "helper.ts"),
		"export const greet = (who: string): string => `hello ${who}`;\n")

	src := `import { greet } from "./helper"; greet(request.body.who)`
	in := Input{Request: RequestInput{Body: map[string]any{"who": "world"}}}
	assertValue(t, mustRun(t, e, src, in), `"hello world"`)
}

// TestBundleTypeScript: an import-free TS script goes through the transpile-only path;
// type annotations, interfaces, and arrow generics are stripped and the last expression
// remains the value (the result contract survives transpilation).
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

// TestBundleUnknownPackageErrors: a bare import that resolves to nothing fails the bundle
// (no instance is ever created — see runFresh), and the error preserves both our wrapper
// and esbuild's diagnostic so callers can tell what was missing.
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

// TestBundleErrorLineRemapped: a runtime throw surfaces at the AUTHOR's line, not the
// offset line in the assembled source. Two paths, both of which shift the line before the
// map corrects it: transpile-only (esbuild drops leading blank lines) and bundling (a
// whole inlined dependency precedes the user's code).
func TestBundleErrorLineRemapped(t *testing.T) {
	t.Run("transform-path", func(t *testing.T) {
		e := newEngine(t)
		// Three leading blank lines esbuild strips; the throw the user wrote is on line 4.
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
		// dayjs is inlined above the user code, so the throw's generated line is deep in
		// the bundle; the source map must still map it back to author line 3.
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

// TestBundleCacheReuse: compiling the same (source, grant) twice serves the second from
// the content-hash cache — the property the middleware hot path relies on to keep esbuild
// off every invoke. Proven by seeding the cache slot with a sentinel and observing it back.
func TestBundleCacheReuse(t *testing.T) {
	b := newBundler("", nil, "")
	const src = `6 * 7`

	c1, err := b.compile(src, Grant{})
	if err != nil {
		t.Fatalf("first compile: %v", err)
	}
	key, ok := b.cacheKey(src, Grant{})
	if !ok {
		t.Fatal("cacheKey not derivable for an empty grant")
	}
	if _, ok := b.cache.Load(key); !ok {
		t.Fatal("cache not populated after first compile")
	}

	// Overwrite the slot with a sentinel; a cached second compile returns it verbatim,
	// a re-compile would return the real transpiled code instead.
	b.cache.Store(key, compiled{code: "SENTINEL"})
	c2, err := b.compile(src, Grant{})
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

// TestDecodeVLQ exercises the base64-VLQ decoder that underpins source-map remapping:
// zero, a positive multi-group value, a negative value, a full 4-field segment, and a
// rejected non-alphabet byte.
func TestDecodeVLQ(t *testing.T) {
	cases := []struct {
		in   string
		want []int
	}{
		{"A", []int{0}},
		{"AAAA", []int{0, 0, 0, 0}},
		{"AACA", []int{0, 0, 1, 0}}, // C -> 2 -> +1
		{"D", []int{-1}},            // D -> 3 -> sign bit set -> -1
		{"gB", []int{16}},           // continuation group: 0|cont, then 1 -> (1<<5)>>1 = 16
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
