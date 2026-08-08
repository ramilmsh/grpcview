package scripting

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

const authorSource = "script.ts"

const entryGlobalName = "__grpcview_entry"

// ES2022 is the floor that permits top-level await (esbuild rejects it at es2020).
const esbuildTarget = api.ES2022

type compiled struct {
	code      string
	sourceMap []byte
}

// A resolved import that fed a bundle but is invisible to the plain (cacheSalt, grant, source)
// key: it lives on disk, outside the cached blob, and can change without the author's source
// changing. Recorded from the build's metafile so a cache hit can be revalidated against disk.
type cacheInput struct {
	path   string
	digest string
}

type cacheEntry struct {
	c      compiled
	inputs []cacheInput
}

type bundler struct {
	resolveDir  string
	nodePaths   []string
	registryDir string
	wsRoot      string
	cacheSalt   string
	cache       sync.Map
}

func newBundler(resolveDir string, nodePaths []string, registryDir string, wsRoot string) *bundler {
	return &bundler{
		resolveDir:  resolveDir,
		nodePaths:   nodePaths,
		registryDir: registryDir,
		wsRoot:      wsRoot,
		cacheSalt: resolveDir + "\x00" + strings.Join(nodePaths, "\x00") + "\x00" + registryDir +
			"\x00" + wsRoot,
	}
}

func (b *bundler) compile(source string, g Grant, collRoot string) (compiled, error) {
	key, keyable := b.cacheKey(source, g, collRoot, "expr")
	if keyable {
		if c, ok := b.cacheLoad(key); ok {
			return c, nil
		}
	}

	var (
		c      compiled
		inputs []cacheInput
		err    error
	)
	if needsBundling(source) {
		c, inputs, err = b.buildBundle(source, g, collRoot)
	} else {
		c, err = transformScript(source)
	}
	if err != nil {
		return compiled{}, err
	}
	if keyable {
		b.cacheStore(key, c, inputs)
	}
	return c, nil
}

func (b *bundler) cacheKey(source string, g Grant, collRoot string, variant string) (string, bool) {
	gj, err := json.Marshal(g)
	if err != nil {
		return "", false
	}
	h := sha256.New()
	io.WriteString(h, b.cacheSalt)
	h.Write([]byte{0})
	io.WriteString(h, variant)
	h.Write([]byte{0})
	h.Write(gj)
	h.Write([]byte{0})
	io.WriteString(h, collRoot)
	h.Write([]byte{0})
	io.WriteString(h, source)
	return hex.EncodeToString(h.Sum(nil)), true
}

// A cache hit is only trustworthy if every input the metafile recorded still reads back to the
// digest it had at build time; a stale entry is dropped rather than served.
func (b *bundler) cacheLoad(key string) (compiled, bool) {
	v, ok := b.cache.Load(key)
	if !ok {
		return compiled{}, false
	}
	entry := v.(cacheEntry)
	for _, in := range entry.inputs {
		data, err := os.ReadFile(in.path)
		if err != nil || sha256Hex(data) != in.digest {
			b.cache.Delete(key)
			return compiled{}, false
		}
	}
	return entry.c, true
}

func (b *bundler) cacheStore(key string, c compiled, inputs []cacheInput) {
	b.cache.Store(key, cacheEntry{c: c, inputs: inputs})
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (b *bundler) compileEntry(source string, g Grant, collRoot string) (compiled, error) {
	key, keyable := b.cacheKey(source, g, collRoot, "entry")
	if keyable {
		if c, ok := b.cacheLoad(key); ok {
			return c, nil
		}
	}
	c, inputs, err := b.buildEntryBundle(source, g, collRoot)
	if err != nil {
		return compiled{}, err
	}
	if keyable {
		b.cacheStore(key, c, inputs)
	}
	return c, nil
}

func (b *bundler) esbuildBundle(source string, g Grant, collRoot string, format api.Format, globalName string) (compiled, []cacheInput, error) {
	if err := rejectComputedImports(source); err != nil {
		return compiled{}, nil, err
	}
	result := api.Build(api.BuildOptions{
		Stdin: &api.StdinOptions{
			Contents:   source,
			Loader:     api.LoaderTS,
			Sourcefile: authorSource,
			ResolveDir: b.resolveDir,
		},
		Outfile:       "script.js",
		Bundle:        true,
		Write:         false,
		Format:        format,
		GlobalName:    globalName,
		Target:        esbuildTarget,
		Platform:      api.PlatformBrowser,
		TreeShaking:   api.TreeShakingFalse,
		Sourcemap:     api.SourceMapExternal,
		Charset:       api.CharsetUTF8,
		LegalComments: api.LegalCommentsNone,
		LogLevel:      api.LogLevelSilent,
		Metafile:      true,
		NodePaths:     b.nodePaths,
		Plugins:       b.plugins(g, collRoot),
	})
	if len(result.Errors) > 0 {
		return compiled{}, nil, bundleErrors(result.Errors)
	}
	return outputToCompiled(result.OutputFiles), collectCacheInputs(result.Metafile), nil
}

func (b *bundler) buildBundle(source string, g Grant, collRoot string) (compiled, []cacheInput, error) {
	return b.esbuildBundle(source, g, collRoot, api.FormatESModule, "")
}

// Captures the entry module's exports onto entryGlobalName. IIFE output cannot contain top-level await
// (esbuild rejects it).
func (b *bundler) buildEntryBundle(source string, g Grant, collRoot string) (compiled, []cacheInput, error) {
	return b.esbuildBundle(source, g, collRoot, api.FormatIIFE, entryGlobalName)
}

// The metafile is esbuild's own account of what fed the bundle, so the key built from it cannot
// drift from what actually ran. Synthetic entries (stdin, plugin-namespaced virtual modules) never
// stat as a real file and drop out here.
func collectCacheInputs(metafile string) []cacheInput {
	var mf struct {
		Inputs map[string]json.RawMessage `json:"inputs"`
	}
	if err := json.Unmarshal([]byte(metafile), &mf); err != nil {
		return nil
	}
	inputs := make([]cacheInput, 0, len(mf.Inputs))
	for p := range mf.Inputs {
		info, err := os.Stat(p)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		inputs = append(inputs, cacheInput{path: p, digest: sha256Hex(data)})
	}
	return inputs
}

func transformScript(source string) (compiled, error) {
	result := api.Transform(source, api.TransformOptions{
		Loader:     api.LoaderTS,
		Format:     api.FormatESModule,
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

func needsBundling(source string) bool {
	return importStmtRe.MatchString(source) || dynamicImportRe.MatchString(source)
}

var (
	importStmtRe    = regexp.MustCompile(`(?m)^[ \t]*(import|export)\b`)
	dynamicImportRe = regexp.MustCompile(`\b(import|require)\s*\(`)
)

// esbuild reports neither an error nor a warning for a computed require(p)/import(p) specifier: it
// emits code that throws at runtime, or for import(p), a live dynamic import into QuickJS. The scan
// runs over maskLiterals(source) rather than the raw text: a false positive here rejects correct
// code with an error that names no line and so cannot be found, whereas a false negative only lets
// the pre-existing runtime failure through. Every ambiguous case below is resolved toward not
// rejecting.
func rejectComputedImports(source string) error {
	masked := maskLiterals(source)
	for _, loc := range dynamicImportRe.FindAllStringIndex(masked, -1) {
		i := loc[1]
		for i < len(masked) && (masked[i] == ' ' || masked[i] == '\t' || masked[i] == '\n' || masked[i] == '\r') {
			i++
		}
		if i >= len(masked) || (masked[i] != '"' && masked[i] != '\'' && masked[i] != '`') {
			return fmt.Errorf("scripting: computed import specifier: require(...) and import(...) need a string literal")
		}
	}
	return nil
}

// maskLiterals returns source with the interior of //, /* */ comments and "…"/'…'/`…` literals
// replaced by spaces, byte for byte, so a text scan for import syntax does not fire on an occurrence
// that is merely quoted or commented out. Newlines are left in place. Regex literals are not
// recognized (deliberately: getting one wrong can only produce a false negative, see
// rejectComputedImports), and an unterminated comment or string is masked to end of input rather
// than rejected.
func maskLiterals(source string) string {
	b := []byte(source)
	n := len(b)
	blank := func(i int) {
		if b[i] != '\n' {
			b[i] = ' '
		}
	}
	for i := 0; i < n; {
		switch {
		case b[i] == '/' && i+1 < n && b[i+1] == '/':
			i += 2
			for i < n && b[i] != '\n' {
				blank(i)
				i++
			}
		case b[i] == '/' && i+1 < n && b[i+1] == '*':
			i += 2
			for i < n && !(b[i] == '*' && i+1 < n && b[i+1] == '/') {
				blank(i)
				i++
			}
			if i < n {
				i += 2
			}
		case b[i] == '"' || b[i] == '\'' || b[i] == '`':
			q := b[i]
			i++
			for i < n && b[i] != q {
				if b[i] == '\\' && i+1 < n {
					blank(i)
					i++
				}
				blank(i)
				i++
			}
			if i < n {
				i++
			}
		default:
			i++
		}
	}
	return string(b)
}

const capNamespace = "grpcview-cap"

var capFilter = `^(node:)?(fs|path)$`

type capModule struct {
	shim    string
	granted func(Grant) bool
}

const (
	capShimPath = `const join = (...parts) => parts.join("/").replace(/\/+/g, "/");
const basename = (p) => { p = String(p); const i = p.lastIndexOf("/"); return i < 0 ? p : p.slice(i + 1); };
export { join, basename };
export default { join, basename };`

	capShimFS = `const readFileSync = (p, _enc) => globalThis.__grpcview_fs_read(String(p));
export { readFileSync };
export default { readFileSync };`
)

var capModules = map[string]capModule{
	"path": {shim: capShimPath},
	"fs":   {shim: capShimFS, granted: func(g Grant) bool { return g.FS != nil }},
}

// esbuild takes the first plugin whose OnResolve returns a path, so the order is a CONTRACT:
// pathResolverPlugin and grpcviewModulesPlugin must be claimed before the registry plugin's
// `^[^./]` filter, which also matches `@/`/`~/`/`grpcview:*`.
func (b *bundler) plugins(g Grant, collRoot string) []api.Plugin {
	ps := []api.Plugin{capabilityPlugin(g), grpcviewModulesPlugin(), pathResolverPlugin(b.wsRoot, collRoot)}
	if b.registryDir != "" {
		ps = append(ps, registryResolverPlugin(b.registryDir))
	}
	return ps
}

// The workspace root is fixed at engine construction; the collection root rides each compile call
// and is empty whenever a run has none, which `~/` then reports as unresolvable.
//
// Both roots are canonicalized once here, up front: esbuild resolves through symlinks (macOS puts
// both /tmp and /var behind one), so an un-resolved root would never satisfy withinDir against its
// own, symlink-resolved r.Path even for a file that is genuinely inside it.
func pathResolverPlugin(wsRoot, collRoot string) api.Plugin {
	wsRoot = canonicalDir(wsRoot)
	collRoot = canonicalDir(collRoot)
	return api.Plugin{
		Name: "grpcview-path-sigils",
		Setup: func(build api.PluginBuild) {
			build.OnResolve(api.OnResolveOptions{Filter: `^[@~]/`},
				func(args api.OnResolveArgs) (api.OnResolveResult, error) {
					root, which := wsRoot, "workspace"
					if args.Path[0] == '~' {
						root, which = collRoot, "collection"
					}
					if root == "" {
						return api.OnResolveResult{}, fmt.Errorf("cannot resolve %q: no %s root for this run", args.Path, which)
					}
					r := build.Resolve("./"+args.Path[2:], api.ResolveOptions{ResolveDir: root, Kind: args.Kind})
					if len(r.Errors) > 0 {
						return api.OnResolveResult{Errors: r.Errors}, nil
					}
					if !withinDir(root, r.Path) {
						return api.OnResolveResult{}, fmt.Errorf("cannot resolve %q: resolves outside the %s", args.Path, which)
					}
					return api.OnResolveResult{Path: r.Path}, nil
				})
		},
	}
}

func canonicalDir(dir string) string {
	if dir == "" {
		return dir
	}
	if canon, err := filepath.EvalSymlinks(dir); err == nil {
		return canon
	}
	return dir
}

func registryResolverPlugin(registryDir string) api.Plugin {
	return api.Plugin{
		Name: "grpcview-npm-registry",
		Setup: func(build api.PluginBuild) {
			build.OnResolve(api.OnResolveOptions{Filter: `^[^./]`},
				func(args api.OnResolveArgs) (api.OnResolveResult, error) {
					if strings.HasPrefix(args.Path, "node:") {
						return api.OnResolveResult{}, nil
					}
					pkg := npmPackageName(args.Path)
					pkgDir := filepath.Join(registryDir, filepath.FromSlash(pkg))
					if _, err := os.Stat(filepath.Join(pkgDir, "package.json")); err != nil {
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

// Containment guard: stops a vendored package prefix from reaching a host file via `..`.
func withinDir(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

const grpcviewNamespace = "grpcview-modules"

var grpcviewModuleFilter = `^grpcview:(invoke|assert|metadata|request)$`

// Per-run data (params, inherited metadata, the invoke/net bridges) never appears in this text — it
// stays in the prelude under `__grpcview_*` globals the shims read. That keeps the module text
// constant, which is the point: it is what makes the bundle cacheable across runs.
var grpcviewModuleShims = map[string]string{
	"invoke": `export function invoke(path, params) {
  try {
    var req = JSON.stringify({ path: String(path), params: (params == null ? {} : params) });
    return Promise.resolve(JSON.parse(globalThis.__grpcview_invoke(req)));
  } catch (e) {
    return Promise.reject(e);
  }
}`,

	// The sync path must throw synchronously and return undefined: a rejected promise nobody awaits
	// is dropped by the top-level settle in evalRaw, so a wrapped failure would read as a pass. Only
	// a thenable condition yields a promise, which the caller must await.
	// Both frames are NAMED so they can be dropped from the stack below: remapJSError reads the
	// first frame's line, so an unfiltered stack would report a frame inside this shim instead of
	// the failing assertion's own line. A bundler-appended disambiguating suffix (`gvAssert2`) still
	// matches the substring filter, so it survives being bundled alongside the author's code.
	"assert": `function gvAssert(description, condition) {
  if (typeof description !== "string" || description === "") {
    throw new TypeError("assert: description must be a non-empty string");
  }
  var fail = function gvAssertFail(reason) {
    var msg = "assertion failed: " + description;
    if (reason) { msg = msg + ": " + reason; }
    var e = new Error(msg);
    e.name = "AssertionError";
    try {
      if (typeof e.stack === "string") {
        e.stack = e.stack.split("\n").filter(function (line) {
          return line.indexOf("gvAssert") === -1;
        }).join("\n");
      }
    } catch (ignored) {}
    throw e;
  };
  var reasonOf = function (e) {
    return String((e && e.message) ? e.message : e);
  };
  var c = condition;
  if (typeof c === "function") {
    try {
      c = c();
    } catch (e) {
      fail(reasonOf(e));
    }
  }
  if (c && typeof c.then === "function") {
    return Promise.resolve(c).then(
      function (v) { if (!v) { fail(); } },
      function (e) { fail(reasonOf(e)); }
    );
  }
  if (!c) { fail(); }
}
export { gvAssert as assert };`,

	"metadata": `export function inherit() {
  return globalThis.__grpcview_inherited || {};
}`,

	"request": `export const params = globalThis.__grpcview_params || {};`,
}

func grpcviewModulesPlugin() api.Plugin {
	return api.Plugin{
		Name: "grpcview-modules",
		Setup: func(build api.PluginBuild) {
			build.OnResolve(api.OnResolveOptions{Filter: grpcviewModuleFilter},
				func(args api.OnResolveArgs) (api.OnResolveResult, error) {
					name := strings.TrimPrefix(args.Path, "grpcview:")
					if _, ok := grpcviewModuleShims[name]; !ok {
						return api.OnResolveResult{}, fmt.Errorf("cannot resolve %q: unknown module", args.Path)
					}
					return api.OnResolveResult{Path: name, Namespace: grpcviewNamespace}, nil
				})
			build.OnLoad(api.OnLoadOptions{Filter: `.*`, Namespace: grpcviewNamespace},
				func(args api.OnLoadArgs) (api.OnLoadResult, error) {
					contents := grpcviewModuleShims[args.Path]
					loader := api.LoaderJS
					return api.OnLoadResult{Contents: &contents, Loader: loader}, nil
				})
		},
	}
}

// Gate 1: a capability module resolves only if the grant permits it, so an ungranted import leaves no
// call site at all.
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
