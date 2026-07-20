package scripting

// bundler.go — GATE 1 of the capability system, and the TS/dependency front end.
//
// The spike's Gate 1 was a regex over `import ... from "..."` (see Bundle, still used by
// the legacy string-mode API). The STRUCTURED profiles (generator/middleware/scenario)
// instead go through this esbuild-backed bundler, which:
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
	key, keyable := b.cacheKey(source, g)
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

func (b *bundler) cacheKey(source string, g Grant) (string, bool) {
	gj, err := json.Marshal(g)
	if err != nil {
		return "", false // a grant that will not marshal must not share a cache slot
	}
	h := sha256.New()
	io.WriteString(h, b.cacheSalt)
	h.Write([]byte{0})
	h.Write(gj)
	h.Write([]byte{0})
	io.WriteString(h, source)
	return hex.EncodeToString(h.Sum(nil)), true
}

// buildBundle runs esbuild's bundler: TS entry via stdin, everything inlined to one ESM
// blob whose top-level statements stay at top level (so it evaluates as GLOBAL code).
func (b *bundler) buildBundle(source string, g Grant) (compiled, error) {
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
		Format:        api.FormatESModule, // keeps entry top-level statements at top level
		Target:        esbuildTarget,
		Platform:      api.PlatformBrowser, // resolves main/module/exports; no node core auto-polyfill
		TreeShaking:   api.TreeShakingFalse, // never drop a bundled dependency's code as "unused"
		Sourcemap:     api.SourceMapExternal,
		Charset:       api.CharsetUTF8,
		LegalComments: api.LegalCommentsNone,
		LogLevel:      api.LogLevelSilent,
		NodePaths:     b.nodePaths,
		Plugins:       b.plugins(g),
	})
	if len(result.Errors) > 0 {
		return compiled{}, bundleErrors(result.Errors)
	}
	return outputToCompiled(result.OutputFiles), nil
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
// qjs_wasm.c registers; they do no I/O themselves. (These mirror the legacy expression
// shims in engine.go, restated as modules for esbuild.)
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
// runs first so it owns the node:* names; the registry plugin (when a registry is
// provisioned) resolves bare npm specifiers against the embedded tree. esbuild tries each
// plugin's OnResolve in order and takes the first that returns a path, so ordering is the
// contract that keeps the two from fighting over a name.
func (b *bundler) plugins(g Grant) []api.Plugin {
	ps := []api.Plugin{capabilityPlugin(g)}
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
