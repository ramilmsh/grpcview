package scripting

import (
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/evanw/esbuild/pkg/api"
)

//go:embed codegen_entry.js
var codegenEntryJS string

// CodegenPolyfillJS is the TextEncoder/TextDecoder polyfill callers must Eval on its own,
// before the bundle — see codegen_polyfill.js for why it can't just be prepended.
//
//go:embed codegen_polyfill.js
var CodegenPolyfillJS string

// BuildCodegenBundle bundles codegen_entry.js (vendored under npm/@bufbuild) against the npm
// registry rooted at registryDir (see MaterializeNpmRegistry), via plain NodePaths resolution
// rather than bundler.go's registryResolverPlugin — @bufbuild/protobuf's subpath exports need
// real package.json "exports" resolution, which that plugin's literal path-join doesn't do.
// Result is a standalone IIFE that, once Eval'd, defines globalThis.__grpcview_codegen.
// Fails unless every memoPatches entry applied.
func BuildCodegenBundle(registryDir string) (string, error) {
	return buildCodegenBundle(registryDir, memoPatches)
}

func buildCodegenBundle(registryDir string, patches map[string][][2]string) (string, error) {
	opts := codegenBuildOptions(registryDir)
	plugin, unapplied := memoPlugin(patches)
	opts.Plugins = []api.Plugin{plugin}
	result := api.Build(opts)
	if len(result.Errors) > 0 {
		return "", bundleErrors(result.Errors)
	}
	if missing := unapplied(); len(missing) > 0 {
		return "", fmt.Errorf("codegen memo: patches not applied (file not loaded): %s", strings.Join(missing, ", "))
	}
	var code strings.Builder
	for _, f := range result.OutputFiles {
		code.Write(f.Contents)
	}
	return code.String(), nil
}

// codegenBuildOptions is everything but the plugins; CodegenBundleHash hashes it.
func codegenBuildOptions(registryDir string) api.BuildOptions {
	return api.BuildOptions{
		Stdin: &api.StdinOptions{
			Contents:   codegenEntryJS,
			Loader:     api.LoaderJS,
			Sourcefile: "codegen-entry.js",
			ResolveDir: registryDir,
		},
		Bundle:      true,
		Write:       false,
		Format:      api.FormatIIFE,
		Target:      esbuildTarget,
		Platform:    api.PlatformNeutral, // no DOM, no node fs/process shims
		TreeShaking: api.TreeShakingFalse,
		LogLevel:    api.LogLevelSilent,
		NodePaths:   []string{registryDir},
	}
}

// goMod is the repo's go.mod (BUILD.bazel copies it), for the esbuild version it pins.
//
//go:embed go.mod.txt
var goMod string

const esbuildModule = "github.com/evanw/esbuild"

// esbuildVersion is goMod's require of esbuildModule.
func esbuildVersion(goMod string) (string, error) {
	for line := range strings.Lines(goMod) {
		if f := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "require ")); len(f) >= 2 && f[0] == esbuildModule {
			return f[1], nil
		}
	}
	return "", fmt.Errorf("codegen hash: go.mod requires no %s", esbuildModule)
}

// CodegenBundleHash identifies BuildCodegenBundle's output plus the polyfill, from their inputs
// alone — no registry materialized, no esbuild run: the embedded npm registry, memoPatches,
// the entry point, the polyfill, the build options and the esbuild version.
func CodegenBundleHash() ([sha256.Size]byte, error) {
	v, err := esbuildVersion(goMod)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return codegenInputsHash(npmRegistry, npmRegistryRoot, CodegenPolyfillJS, memoPatches, codegenBuildOptions(""), v)
}

func codegenInputsHash(registry fs.FS, root, polyfill string, patches map[string][][2]string, opts api.BuildOptions, version string) ([sha256.Size]byte, error) {
	h := sha256.New()
	field := func(s string) { fmt.Fprintf(h, "%d:%s", len(s), s) }
	field(version)
	o, err := json.Marshal(opts) // Stdin carries the entry point; Plugins (funcs) must be unset
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("codegen hash: build options: %w", err)
	}
	field(string(o))
	field(polyfill)
	for _, suffix := range slices.Sorted(maps.Keys(patches)) {
		field(suffix)
		for _, p := range patches[suffix] {
			field(p[0])
			field(p[1])
		}
	}
	err = fs.WalkDir(registry, root, func(p string, d fs.DirEntry, err error) error { // lexical order
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(registry, p)
		if err != nil {
			return err
		}
		field(p)
		field(string(b))
		return nil
	})
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("codegen hash: npm registry: %w", err)
	}
	return [sha256.Size]byte(h.Sum(nil)), nil
}

// CodegenInputGlobal is the global callers SetBytes the FileDescriptorSet into before
// Eval'ing CodegenCallScript.
const CodegenInputGlobal = "__grpcview_codegen_input"

// CodegenParameter is the protoc-gen-es plugin parameter, matching codegen/testdata/golden.test.cjs.
const CodegenParameter = "target=ts,json_types=true"

// CodegenCallScript invokes the bundle's entry point on CodegenInputGlobal, generating
// fileToGenerate (nil = every non-WKT file in the set).
func CodegenCallScript(fileToGenerate []string) string {
	files := []byte("null")
	if fileToGenerate != nil {
		files, _ = json.Marshal(fileToGenerate) // []string can't fail
	}
	param, _ := json.Marshal(CodegenParameter)
	return fmt.Sprintf("globalThis.__grpcview_codegen(globalThis.%s, %s, %s)", CodegenInputGlobal, files, param)
}

// memoPatches memoize two protoplugin helpers that are pure but recomputed per call: allNames
// (re-walks the whole file for every name lookup) and findComments (linear scan of
// sourceCodeInfo per declaration). Quadratic either way; ~5x of QuickJS time. Output unchanged.
// Keyed by path suffix under the registry; each old string must occur exactly once.
var memoPatches = map[string][][2]string{
	"@bufbuild/protoplugin/dist/cjs/names.js": {{
		"function allNames(file) {",
		`var allNamesMemo;
function allNames(file) {
    allNamesMemo ??= new WeakMap();
    let n = allNamesMemo.get(file);
    if (n === undefined) allNamesMemo.set(file, (n = allNamesUncached(file)));
    return n;
}
function allNamesUncached(file) {`,
	}},
	"@bufbuild/protoplugin/dist/cjs/source-code-info.js": {{
		"    for (const location of sourceCodeInfo.location) {",
		"    for (const location of locationsAt(sourceCodeInfo, sourcePath)) {",
	}, {
		"function findComments(sourceCodeInfo, sourcePath) {",
		`var locationIndex;
// [first location whose path equals sourcePath], or [].
function locationsAt(sourceCodeInfo, sourcePath) {
    locationIndex ??= new WeakMap();
    let m = locationIndex.get(sourceCodeInfo);
    if (m === undefined) {
        m = new Map();
        for (const l of sourceCodeInfo.location) {
            const k = l.path.join(",");
            if (!m.has(k)) m.set(k, l);
        }
        locationIndex.set(sourceCodeInfo, m);
    }
    const l = m.get(sourcePath.join(","));
    return l === undefined ? [] : [l];
}
function findComments(sourceCodeInfo, sourcePath) {`,
	}},
}

// memoPlugin applies patches; unapplied reports the suffixes no loaded file matched. The
// filter is built from the suffixes, separator-agnostic.
func memoPlugin(patches map[string][][2]string) (plugin api.Plugin, unapplied func() []string) {
	var mu sync.Mutex
	pending := map[string]bool{}
	var alts []string
	for suffix := range patches {
		pending[suffix] = true
		alts = append(alts, strings.ReplaceAll(regexp.QuoteMeta(suffix), "/", `[/\\]`))
	}
	slices.Sort(alts)
	filter := `(?:^|[/\\])(?:` + strings.Join(alts, "|") + `)$`
	plugin = api.Plugin{
		Name: "grpcview-codegen-memo",
		Setup: func(build api.PluginBuild) {
			build.OnLoad(api.OnLoadOptions{Filter: filter},
				func(args api.OnLoadArgs) (api.OnLoadResult, error) {
					path := strings.ReplaceAll(args.Path, `\`, "/")
					var suffix string
					for s := range patches {
						if strings.HasSuffix(path, "/"+s) || path == s {
							suffix = s
						}
					}
					if suffix == "" {
						return api.OnLoadResult{}, nil
					}
					b, err := os.ReadFile(args.Path)
					if err != nil {
						return api.OnLoadResult{}, err
					}
					src := string(b)
					for _, p := range patches[suffix] {
						if n := strings.Count(src, p[0]); n != 1 {
							return api.OnLoadResult{}, fmt.Errorf("codegen memo: %s: %q occurs %d times, want 1", args.Path, p[0], n)
						}
						src = strings.Replace(src, p[0], p[1], 1)
					}
					mu.Lock()
					delete(pending, suffix)
					mu.Unlock()
					return api.OnLoadResult{Contents: &src, Loader: api.LoaderJS}, nil
				})
		},
	}
	return plugin, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Sorted(maps.Keys(pending))
	}
}
