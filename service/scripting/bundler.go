package scripting

// bundler.go — GATE 1 of the capability system, and the TS/dependency front end.
//
// The spike's Gate 1 was a regex over `import ... from "..."` (since removed). The
// STRUCTURED profiles (generator/middleware/scenario) go through this esbuild-backed
// bundler, which:
//
//   - transpiles TypeScript (and JSX) to JS;
//   - resolves + inlines the module graph — the vendored node:* capability shims (grant-
//     gated) and, when a resolver root is configured, npm packages — into ONE blob;
//   - keeps that blob GLOBAL-evaluable so the existing eval model is preserved: the
//     script's value is still its last top-level expression (see the DCE note below), no
//     `export`/entry-point convention required;
//   - emits a source map so a runtime error maps back to the author's original line
//     (remapJSError, sourcemap.go) instead of the offset line in the assembled source.
//
// Gate 1 lives in the resolve plugin: a node:* capability import resolves to its shim
// ONLY if the grant permits it; ungranted (or unknown) => the module does not resolve =>
// the bundle fails and no call site is ever assembled. Gate 2 (the Go host functions
// refusing at call time) still applies independently at run time.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/evanw/esbuild/pkg/api"
)

// authorSource is the virtual filename esbuild records for the user's script; it is what
// shows up as the source-map `sources` entry for the author's own code (vs. an inlined
// dependency), and is the "<script>" analogue for the structured path.
const authorSource = "script.ts"

// entryGlobalName is the global esbuild's IIFE build assigns the entry module's exports
// to (its `default` and named exports). The entry-point calling convention (entry.go)
// reads the exported function off it — `__grpcview_entry.default` / `.handle` — from the
// run-time postlude. It is deliberately obscure so an author's code is unlikely to shadow
// it. Used only by the generator/middleware entry-point compile path, not the scenario/
// scratchpad last-expression path.
const entryGlobalName = "__grpcview_entry"

// esbuildTarget is the syntax level esbuild emits. ES2022 is the floor that permits
// TOP-LEVEL AWAIT (es2020 makes esbuild reject it) while staying well within what the
// bundled bellard/quickjs supports; nothing newer is required.
const esbuildTarget = api.ES2022

// compiled is the output of turning a script into runnable JS: a GLOBAL-evaluable blob
// plus its source map (nil if none). The blob is prepended with the input prelude and
// evaluated as-is; its last top-level expression is the run's value.
type compiled struct {
	code      string
	sourceMap []byte
	// authorPreludeLines is the number of synthetic-prelude lines prepended AHEAD of the
	// author's code in the esbuild INPUT — distinct from the run-time input prelude, which
	// is prepended at eval time and counted separately (runCompiled). Only the generator-
	// composition path sets it non-zero: its `import … from "grpcview:gen/…"` lines (compose.go)
	// sit in the SAME author source ("script.ts") above the body, so a body runtime error maps
	// through the source map to a line shifted down by this many lines, which remapJSError
	// undoes. It is 0 for every existing path (no synthetic author prelude there).
	authorPreludeLines int
}

// bundler compiles scripts via esbuild and caches the result by a content hash of
// (resolver identity, grant, source). It is safe for concurrent use.
//
// resolveDir/nodePaths configure esbuild's OWN resolver: resolveDir anchors RELATIVE
// imports (`./util`), nodePaths are NODE_PATH-style roots for BARE (npm) imports. In
// production both are empty, so esbuild resolves nothing from the host FS — an empty
// ResolveDir is what makes an untrusted inline script unable to read host files at bundle
// time (`import x from "/etc/passwd"`). registryDir is the extracted embedded npm registry
// (npm.go); it is resolved by registryResolverPlugin INDEPENDENTLY of resolveDir, so a
// vendored package (dayjs) resolves in production while the host FS stays closed. Tests
// additionally point resolveDir/nodePaths at their own vendored tree (ms, mustache).
type bundler struct {
	resolveDir  string
	nodePaths   []string
	registryDir string
	cacheSalt   string // resolver identity, folded into every cache key
	cache       sync.Map
}

func newBundler(resolveDir string, nodePaths []string, registryDir string) *bundler {
	return &bundler{
		resolveDir:  resolveDir,
		nodePaths:   nodePaths,
		registryDir: registryDir,
		cacheSalt:   resolveDir + "\x00" + strings.Join(nodePaths, "\x00") + "\x00" + registryDir,
	}
}

// compile returns the runnable form of source under grant g, served from cache when the
// same (resolver, grant, source) was compiled before. Bundling npm/TS is not free, and
// the middleware profile recompiles the same script on every invoke, so the cache is what
// keeps warm-path compilation off the hot path.
func (b *bundler) compile(source string, g Grant) (compiled, error) {
	key, keyable := b.cacheKey(source, g, "expr")
	if keyable {
		if v, ok := b.cache.Load(key); ok {
			return v.(compiled), nil
		}
	}

	var (
		c   compiled
		err error
	)
	// A script with no imports needs no module resolution, so transpile-only: esbuild's
	// bundler drops a script's bare no-op statements (e.g. a lone `undefined`) and, more
	// to the point, would need an entry/export convention — Transform keeps the body,
	// and thus the last-expression value, verbatim. Anything with an import/require must
	// go through the bundler so the graph is resolved and inlined.
	if needsBundling(source) {
		c, err = b.buildBundle(source, g)
	} else {
		c, err = transformScript(source)
	}
	if err != nil {
		return compiled{}, err
	}
	if keyable {
		b.cache.Store(key, c)
	}
	return c, nil
}

func (b *bundler) cacheKey(source string, g Grant, variant string) (string, bool) {
	gj, err := json.Marshal(g)
	if err != nil {
		return "", false // a grant that will not marshal must not share a cache slot
	}
	h := sha256.New()
	io.WriteString(h, b.cacheSalt)
	h.Write([]byte{0})
	io.WriteString(h, variant) // "expr" vs "entry" produce different blobs from one source
	h.Write([]byte{0})
	h.Write(gj)
	h.Write([]byte{0})
	io.WriteString(h, source)
	return hex.EncodeToString(h.Sum(nil)), true
}

// compileEntry compiles source for the ENTRY-POINT calling convention: an IIFE bundle
// that assigns the entry module's exports to the entryGlobalName global, so a run-time
// postlude can call the exported function (entry.go). Unlike compile it always bundles
// (there is always an export to expose) and never uses the transpile-only last-expression
// path. Cached independently of compile via the "entry" variant.
func (b *bundler) compileEntry(source string, g Grant) (compiled, error) {
	key, keyable := b.cacheKey(source, g, "entry")
	if keyable {
		if v, ok := b.cache.Load(key); ok {
			return v.(compiled), nil
		}
	}
	c, err := b.buildEntryBundle(source, g)
	if err != nil {
		return compiled{}, err
	}
	if keyable {
		b.cache.Store(key, c)
	}
	return c, nil
}

// esbuildBundle is the one place the shared esbuild BuildOptions live; every bundling path
// funnels through it. The (format, globalName) pair selects the two output shapes callers
// need — ESM (FormatESModule, "") keeps the entry's top-level statements at top level so the
// blob evaluates as GLOBAL code and its last expression is the run's value; IIFE (FormatIIFE,
// entryGlobalName) captures the entry module's exports onto that global for the entry-point
// calling convention (entry.go) — while `extra` injects run-specific resolver plugins (the
// generator-composition resolver). Everything else is identical across shapes and stays
// byte-for-byte what buildBundle/buildEntryBundle used before this was extracted:
// PlatformBrowser (resolves main/module/exports; no node core auto-polyfill), TreeShaking off
// (never drop a bundled dependency's code as "unused"), an external source map, and a
// filesystem-anchored resolve only via b.resolveDir/b.nodePaths (both empty in production).
func (b *bundler) esbuildBundle(source string, g Grant, format api.Format, globalName string, extra ...api.Plugin) (compiled, error) {
	result := api.Build(api.BuildOptions{
		Stdin: &api.StdinOptions{
			Contents:   source,
			Loader:     api.LoaderTS,
			Sourcefile: authorSource,
			ResolveDir: b.resolveDir,
		},
		Outfile:       "script.js", // required for an external source map even with Write:false
		Bundle:        true,
		Write:         false,
		Format:        format,
		GlobalName:    globalName, // ignored by esbuild unless format is IIFE
		Target:        esbuildTarget,
		Platform:      api.PlatformBrowser,
		TreeShaking:   api.TreeShakingFalse,
		Sourcemap:     api.SourceMapExternal,
		Charset:       api.CharsetUTF8,
		LegalComments: api.LegalCommentsNone,
		LogLevel:      api.LogLevelSilent,
		NodePaths:     b.nodePaths,
		Plugins:       b.plugins(g, extra...),
	})
	if len(result.Errors) > 0 {
		return compiled{}, bundleErrors(result.Errors)
	}
	return outputToCompiled(result.OutputFiles), nil
}

// buildBundle runs esbuild's bundler: TS entry via stdin, everything inlined to one ESM
// blob whose top-level statements stay at top level (so it evaluates as GLOBAL code).
func (b *bundler) buildBundle(source string, g Grant) (compiled, error) {
	return b.esbuildBundle(source, g, api.FormatESModule, "")
}

// buildEntryBundle runs esbuild's bundler in IIFE format with a GlobalName, so the entry
// module's exports (`default`, named `handle`, …) are captured onto entryGlobalName. The
// blob evaluates as global code that ends by assigning that global; the run-time postlude
// then reads and calls the exported function. Top-level await in the module body is not
// available in IIFE output (esbuild rejects it) — the authored contract is an exported
// (possibly async) function, awaited by the postlude, not module-level TLA.
func (b *bundler) buildEntryBundle(source string, g Grant) (compiled, error) {
	return b.esbuildBundle(source, g, api.FormatIIFE, entryGlobalName)
}

// buildBundleComposed is buildBundle with the workspace's saved generators available for
// composition: generatorResolverPlugin resolves the composition prelude's `grpcview:gen/<name>`
// imports (compose.go) by inlining each generator's source into the ESM bundle. Used for a
// composing TypeScript request body with NO default export (last-expression form). It
// deliberately does NOT consult or populate the compile cache — the generator set folds into
// the blob per run and a request-body eval is uncached (RunRequestBody), so a cache keyed on
// (source, grant) alone would be unsound (it ignores gens).
func (b *bundler) buildBundleComposed(source string, g Grant, gens map[string]string) (compiled, error) {
	return b.esbuildBundle(source, g, api.FormatESModule, "", generatorResolverPlugin(gens))
}

// buildEntryBundleComposed is buildEntryBundle for a composing body that DOES declare a
// default export (the entry-point convention): the same IIFE export capture, plus the
// generator resolver. Like buildBundleComposed it bypasses the compile cache.
func (b *bundler) buildEntryBundleComposed(source string, g Grant, gens map[string]string) (compiled, error) {
	return b.esbuildBundle(source, g, api.FormatIIFE, entryGlobalName, generatorResolverPlugin(gens))
}

// transformScript transpiles a no-import script (TS -> JS) without bundling, preserving
// its statements — and hence its last-expression value — as written.
func transformScript(source string) (compiled, error) {
	result := api.Transform(source, api.TransformOptions{
		Loader:     api.LoaderTS,
		Format:     api.FormatESModule, // makes top-level await valid
		Target:     esbuildTarget,
		Sourcemap:  api.SourceMapExternal,
		Sourcefile: authorSource,
		LogLevel:   api.LogLevelSilent,
	})
	if len(result.Errors) > 0 {
		return compiled{}, bundleErrors(result.Errors)
	}
	return compiled{code: string(result.Code), sourceMap: result.Map}, nil
}

func outputToCompiled(files []api.OutputFile) compiled {
	var c compiled
	for _, f := range files {
		if strings.HasSuffix(f.Path, ".map") {
			c.sourceMap = f.Contents
		} else {
			c.code = string(f.Contents)
		}
	}
	return c
}

// bundleErrors renders esbuild diagnostics into one Go error. It preserves each message's
// text verbatim, so a Gate-1 denial keeps its "capability not granted" wording for callers
// (and tests) that match on it.
func bundleErrors(msgs []api.Message) error {
	parts := make([]string, 0, len(msgs))
	for _, m := range msgs {
		s := m.Text
		if m.Location != nil {
			s = fmt.Sprintf("%s (%s:%d:%d)", s, m.Location.File, m.Location.Line, m.Location.Column)
		}
		parts = append(parts, s)
	}
	return fmt.Errorf("scripting: bundle failed: %s", strings.Join(parts, "; "))
}

// needsBundling reports whether source has module syntax (static import/export or a
// dynamic import()/require()) and therefore must go through the bundler. A false positive
// (the word appearing in a string/comment) only routes a dependency-free script through
// the heavier path, which is still correct; a false negative cannot happen for real
// top-level module syntax, which is what matters for resolution.
func needsBundling(source string) bool {
	return importStmtRe.MatchString(source) || dynamicImportRe.MatchString(source)
}

var (
	importStmtRe    = regexp.MustCompile(`(?m)^[ \t]*(import|export)\b`)
	dynamicImportRe = regexp.MustCompile(`\b(import|require)\s*\(`)
)

// ---- Gate 1: the capability resolve/load plugin ----------------------------------
//
// node:* (and the bare fs/path/net aliases libraries use) are intercepted here BEFORE
// esbuild's own resolver. An inert module (path) always resolves; a capability module
// (fs/net) resolves only if the grant permits it, otherwise the resolve fails and the
// bundle cannot be assembled. The check runs for the user's imports AND any transitive
// dependency's imports, so a dependency cannot smuggle in an ungranted capability.

const capNamespace = "grpcview-cap"

// capFilter matches node:fs / fs / node:path / path / node:net / net.
var capFilter = `^(node:)?(fs|path|net)$`

// capModule is one entry in the capability/shim registry. A nil `granted` marks an INERT
// module (pure computation, always injected); a non-nil `granted` marks a CAPABILITY
// module (injected only when the grant permits it — Gate 1).
type capModule struct {
	shim    string
	granted func(Grant) bool
}

// The vendored node:* shims, as ES modules (default + named exports so both
// `import fs from "node:fs"` and `import { readFileSync } from "node:fs"` resolve). The
// capability shims are the ergonomic JS surface over the __grpcview_* marshallers that
// qjs_wasm.c registers; they do no I/O themselves.
const (
	capShimPath = `const join = (...parts) => parts.join("/").replace(/\/+/g, "/");
const basename = (p) => { p = String(p); const i = p.lastIndexOf("/"); return i < 0 ? p : p.slice(i + 1); };
export { join, basename };
export default { join, basename };`

	capShimFS = `const readFileSync = (p, _enc) => globalThis.__grpcview_fs_read(String(p));
export { readFileSync };
export default { readFileSync };`

	capShimNet = `const fetch = (u) => globalThis.__grpcview_net_fetch(String(u));
export { fetch };
export default { fetch };`
)

var capModules = map[string]capModule{
	"path": {shim: capShimPath}, // inert
	"fs":   {shim: capShimFS, granted: func(g Grant) bool { return g.FS != nil }},
	"net":  {shim: capShimNet, granted: func(g Grant) bool { return g.Net != nil }},
}

// plugins is the ordered esbuild plugin chain for one run. The capability plugin (Gate 1)
// runs FIRST so it owns the node:* names; any `extra` plugins (the generator-composition
// resolver, passed by the composed builds) run NEXT; the registry plugin (when a registry is
// provisioned) resolves bare npm specifiers against the embedded tree LAST. esbuild tries each
// plugin's OnResolve in order and takes the first that returns a path, so this ordering is the
// contract that keeps grpcview:gen/* claimed by the generator plugin before the npm registry
// plugin (whose `^[^./]` filter would otherwise match it) — and keeps every plugin from
// fighting over a name.
func (b *bundler) plugins(g Grant, extra ...api.Plugin) []api.Plugin {
	ps := []api.Plugin{capabilityPlugin(g)}
	ps = append(ps, extra...)
	if b.registryDir != "" {
		ps = append(ps, registryResolverPlugin(b.registryDir))
	}
	return ps
}

// ---- Embedded npm registry resolver ----------------------------------------------
//
// registryResolverPlugin resolves a BARE npm import against the extracted embedded registry
// (npm.go) WITHOUT giving esbuild a filesystem-anchored entry — the property that keeps an
// untrusted inline script from reading host files at bundle time (see the bundler doc). It
// maps a bare specifier to a file inside registryDir and hands esbuild that absolute path;
// esbuild's own resolver (build.Resolve) reads the package's package.json (main/module/
// exports) and, for a real file on disk, resolves the package's transitive RELATIVE imports
// against their own directory — all staying inside the registry.
//
// It fires only for specifiers whose package is actually vendored (a package.json exists),
// leaving every other bare import to esbuild's default resolver — which, with resolveDir
// empty in production, fails cleanly (that is the "non-vendored import must fail" contract).
// A containment guard rejects any resolved path that escapes the registry, so a crafted
// subpath ("dayjs/../../../etc/passwd") cannot read a host file even though the package
// prefix is vendored.
func registryResolverPlugin(registryDir string) api.Plugin {
	return api.Plugin{
		Name: "grpcview-npm-registry",
		Setup: func(build api.PluginBuild) {
			// Filter: specifiers that are neither relative (`./`, `../`) nor absolute (`/`).
			// node:* also matches this, but the capability plugin (registered first) owns
			// those; the explicit prefix skip below avoids a pointless stat for them.
			build.OnResolve(api.OnResolveOptions{Filter: `^[^./]`},
				func(args api.OnResolveArgs) (api.OnResolveResult, error) {
					if strings.HasPrefix(args.Path, "node:") {
						return api.OnResolveResult{}, nil
					}
					pkg := npmPackageName(args.Path)
					pkgDir := filepath.Join(registryDir, filepath.FromSlash(pkg))
					if _, err := os.Stat(filepath.Join(pkgDir, "package.json")); err != nil {
						// Not vendored — let esbuild's default resolver handle it (and, in
						// production, fail cleanly since resolveDir is empty).
						return api.OnResolveResult{}, nil
					}
					target := filepath.Join(registryDir, filepath.FromSlash(args.Path))
					r := build.Resolve(target, api.ResolveOptions{ResolveDir: registryDir, Kind: args.Kind})
					if len(r.Errors) > 0 {
						return api.OnResolveResult{Errors: r.Errors}, nil
					}
					if !withinDir(registryDir, r.Path) {
						return api.OnResolveResult{}, fmt.Errorf("cannot resolve %q: resolves outside the npm registry", args.Path)
					}
					return api.OnResolveResult{Path: r.Path}, nil
				})
		},
	}
}

// npmPackageName returns the package portion of a bare specifier: the first path segment,
// or the first two for a scoped package (`@scope/name/sub` -> `@scope/name`).
func npmPackageName(importPath string) string {
	if strings.HasPrefix(importPath, "@") {
		if i := strings.IndexByte(importPath, '/'); i >= 0 {
			if j := strings.IndexByte(importPath[i+1:], '/'); j >= 0 {
				return importPath[:i+1+j]
			}
		}
		return importPath
	}
	if i := strings.IndexByte(importPath, '/'); i >= 0 {
		return importPath[:i]
	}
	return importPath
}

// withinDir reports whether path is dir itself or lies under it, comparing already-clean
// absolute paths (build.Resolve returns a realpath; registryDir is EvalSymlinks'd in npm.go
// so the two are on the same footing). It is the containment guard that stops a vendored
// package prefix from being used to reach a host file via `..`.
func withinDir(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// ---- Generator composition resolver ----------------------------------------------
//
// generatorResolverPlugin lets a TypeScript request body call the workspace's saved
// generators as ambient globals (ts-request-body-plan T3 / pillar C). compose.go emits a
// synthetic prelude of `import __gen$i from "grpcview:gen/<name>"` lines; this plugin resolves
// each such specifier to the named generator's SOURCE, which esbuild then inlines into the
// bundle like any other module (mirroring how registryResolverPlugin serves npm packages).
// The gens map is the per-run generator set (name -> source), so the plugin is built fresh per
// composed build and never shares a cache slot.
//
// It is registered AHEAD of the npm registry plugin (see plugins) so it claims the
// grpcview:gen/* specifiers first — the registry plugin's `^[^./]` filter would otherwise match
// them. A generator's OWN bare imports (e.g. `import dayjs from "dayjs"`) still resolve: the
// registry plugin's OnResolve carries no namespace constraint, so it fires for an import from
// the generator namespace exactly as it does for the author's own — esbuild resolves by import
// path regardless of which module holds the import.

const (
	// generatorSpecPrefix is the synthetic import-specifier prefix the composition prelude
	// uses; the text after it is the generator's display name.
	generatorSpecPrefix = "grpcview:gen/"
	// generatorNamespace tags a resolved generator module so OnLoad serves its source from the
	// gens map (the analogue of capNamespace for the capability shims).
	generatorNamespace = "grpcview-generator"
)

// generatorResolverPlugin builds the composition resolver for one run's generator set,
// mirroring registryResolverPlugin. OnResolve claims a grpcview:gen/<name> specifier and maps
// it to a generatorNamespace module named for the generator; a <name> not present in gens is
// an esbuild resolve error, so the composed bundle fails to assemble (surfaced as a Go bundle
// error) rather than silently dropping the call. OnLoad hands esbuild the generator's source
// as TypeScript.
func generatorResolverPlugin(gens map[string]string) api.Plugin {
	return api.Plugin{
		Name: "grpcview-generators",
		Setup: func(build api.PluginBuild) {
			build.OnResolve(api.OnResolveOptions{Filter: `^grpcview:gen/`},
				func(args api.OnResolveArgs) (api.OnResolveResult, error) {
					name := strings.TrimPrefix(args.Path, generatorSpecPrefix)
					if _, ok := gens[name]; !ok {
						return api.OnResolveResult{}, fmt.Errorf("cannot resolve %q: no generator named %q", args.Path, name)
					}
					return api.OnResolveResult{Path: name, Namespace: generatorNamespace}, nil
				})
			build.OnLoad(api.OnLoadOptions{Filter: `.*`, Namespace: generatorNamespace},
				func(args api.OnLoadArgs) (api.OnLoadResult, error) {
					contents := gens[args.Path]
					loader := api.LoaderTS
					return api.OnLoadResult{Contents: &contents, Loader: loader}, nil
				})
		},
	}
}

// capabilityPlugin builds the Gate-1 esbuild plugin for one run's grant.
func capabilityPlugin(g Grant) api.Plugin {
	return api.Plugin{
		Name: "grpcview-capabilities",
		Setup: func(build api.PluginBuild) {
			build.OnResolve(api.OnResolveOptions{Filter: capFilter},
				func(args api.OnResolveArgs) (api.OnResolveResult, error) {
					name := strings.TrimPrefix(args.Path, "node:")
					mod, ok := capModules[name]
					if !ok {
						return api.OnResolveResult{}, fmt.Errorf("cannot resolve %q: unknown module", args.Path)
					}
					if mod.granted != nil && !mod.granted(g) {
						return api.OnResolveResult{}, fmt.Errorf("cannot resolve %q: capability not granted", args.Path)
					}
					return api.OnResolveResult{Path: name, Namespace: capNamespace}, nil
				})
			build.OnLoad(api.OnLoadOptions{Filter: `.*`, Namespace: capNamespace},
				func(args api.OnLoadArgs) (api.OnLoadResult, error) {
					contents := capModules[args.Path].shim
					loader := api.LoaderJS
					return api.OnLoadResult{Contents: &contents, Loader: loader}, nil
				})
		},
	}
}
