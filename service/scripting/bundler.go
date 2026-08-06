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
	code               string
	sourceMap          []byte
	authorPreludeLines int
}

type bundler struct {
	resolveDir  string
	nodePaths   []string
	registryDir string
	cacheSalt   string
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
		return "", false
	}
	h := sha256.New()
	io.WriteString(h, b.cacheSalt)
	h.Write([]byte{0})
	io.WriteString(h, variant)
	h.Write([]byte{0})
	h.Write(gj)
	h.Write([]byte{0})
	io.WriteString(h, source)
	return hex.EncodeToString(h.Sum(nil)), true
}

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

func (b *bundler) esbuildBundle(source string, g Grant, format api.Format, globalName string, extra ...api.Plugin) (compiled, error) {
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
		NodePaths:     b.nodePaths,
		Plugins:       b.plugins(g, extra...),
	})
	if len(result.Errors) > 0 {
		return compiled{}, bundleErrors(result.Errors)
	}
	return outputToCompiled(result.OutputFiles), nil
}

func (b *bundler) buildBundle(source string, g Grant) (compiled, error) {
	return b.esbuildBundle(source, g, api.FormatESModule, "")
}

// Captures the entry module's exports onto entryGlobalName. IIFE output cannot contain top-level await
// (esbuild rejects it).
func (b *bundler) buildEntryBundle(source string, g Grant) (compiled, error) {
	return b.esbuildBundle(source, g, api.FormatIIFE, entryGlobalName)
}

// Bypasses the compile cache: gens folds into the blob, so a key over (source, grant) alone would be
// unsound.
func (b *bundler) buildBundleComposed(source string, g Grant, gens map[string]string) (compiled, error) {
	return b.esbuildBundle(source, g, api.FormatESModule, "", generatorResolverPlugin(gens))
}

func (b *bundler) buildEntryBundleComposed(source string, g Grant, gens map[string]string) (compiled, error) {
	return b.esbuildBundle(source, g, api.FormatIIFE, entryGlobalName, generatorResolverPlugin(gens))
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
// grpcview:gen/* must be claimed before the registry plugin's `^[^./]` filter matches it.
func (b *bundler) plugins(g Grant, extra ...api.Plugin) []api.Plugin {
	ps := []api.Plugin{capabilityPlugin(g)}
	ps = append(ps, extra...)
	if b.registryDir != "" {
		ps = append(ps, registryResolverPlugin(b.registryDir))
	}
	return ps
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

const (
	generatorSpecPrefix = "grpcview:gen/"
	generatorNamespace  = "grpcview-generator"
)

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
