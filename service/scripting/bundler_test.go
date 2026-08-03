package scripting

// Validates the esbuild front end: TypeScript transpilation, npm/relative-module
// bundling, and error-line remapping back to the author's source through source maps.

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
Mustache.render("Hello {{name}}!", { name: request.body.who })`
		in := Input{Request: RequestInput{Body: map[string]any{"who": "gRPC"}}}
		assertValue(t, mustRun(t, e, src, in), `"Hello gRPC!"`)
	})

	t.Run("combined/two-deps-and-vars", func(t *testing.T) {
		src := `import dayjs from "dayjs";
import ms from "ms";
({ ttlMs: ms(vars.ttl), day: dayjs("2024-01-01").add(1, "month").format("YYYY-MM") })`
		in := Input{Vars: map[string]any{"ttl": "15m"}}
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

	src := `import { greet } from "./helper"; greet(request.body.who)`
	in := Input{Request: RequestInput{Body: map[string]any{"who": "world"}}}
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
		// esbuild strips leading blank lines, shifting generated positions; the map must
		// still land on the author's line 4.
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
	b := newBundler("", nil, "")
	const src = `6 * 7`

	c1, err := b.compile(src, Grant{})
	if err != nil {
		t.Fatalf("first compile: %v", err)
	}
	key, ok := b.cacheKey(src, Grant{}, "expr")
	if !ok {
		t.Fatal("cacheKey not derivable for an empty grant")
	}
	if _, ok := b.cache.Load(key); !ok {
		t.Fatal("cache not populated after first compile")
	}

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
