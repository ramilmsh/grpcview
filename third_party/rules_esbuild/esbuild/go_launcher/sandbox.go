package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

// Port of esbuild/private/plugins/bazel-sandbox.js, almost line-for-line, onto esbuild's Go
// plugin API instead of its JS one. See that file's header comment for the problem this solves:
// under Bazel's sandbox, esbuild follows a symlink (e.g. a pnpm-style symlinked node_modules
// entry) out of the sandbox root, resolving to a real absolute path outside it. This plugin
// detects that and maps the path back into the sandbox.
//
// Go's PluginBuild.Resolve is a synchronous, in-process call (no IPC round trip), so unlike the
// JS original this needs no `async`/`await`.

// bazelOutBindirRE matches the bazel-out/<config>/bin segment of an absolute path. Used to detect
// and strip a bindir prefix instead of matching bindir exactly, because under Bazel's
// path-mapping feature the BAZEL_BINDIR env var may hold a generic mapped placeholder (e.g.
// "bazel-out/cfg/bin") for cache-sharing purposes rather than the real per-config value (e.g.
// "bazel-out/k8-fastbuild/bin") -- but once esbuild follows a symlink out of the sandbox and Go
// resolves it to a real absolute path, that path always contains the *real* bindir segment, never
// the mapped one. The path is then reconstructed using bindir (see correctImportPath), since
// that's the name the mapped sandbox's own directory tree actually uses on disk for this action.
//
// Matches both `/` and `\` as separators for the same reason the JS original does: BAZEL_BINDIR
// (from Bazel's Starlark-internal path representation) is always forward-slash, but a real
// resolved path on native Windows uses backslashes.
var bazelOutBindirRE = regexp.MustCompile(`bazel-out[\\/][^\\/]+[\\/]bin[\\/]`)

// sandboxGuard is the Go analog of the JS plugin's `pluginData.executedSandboxPlugin` flag: it
// prevents infinite recursion when resolveInExecroot calls build.Resolve() from inside its own
// OnResolve handler, since the handler's filter (".") matches every resolution, including the
// nested one it just triggered itself.
type sandboxGuard struct{}

// bazelSandboxPlugin returns the Go equivalent of bazelSandboxPlugin() in bazel-sandbox.js.
//
// execroot is this action's execution root (the sandbox root when sandboxing is enabled) -- the
// Go analog of the JS launcher's JS_BINARY__EXECROOT env var. bindir is this action's bazel-out
// bin directory, forwarded from the BAZEL_BINDIR env var esbuild.bzl sets explicitly for
// non-js_binary launchers.
func bazelSandboxPlugin(execroot, bindir string) api.Plugin {
	return api.Plugin{
		Name: "bazel-sandbox",
		Setup: func(build api.PluginBuild) {
			build.OnResolve(api.OnResolveOptions{Filter: `.`},
				func(args api.OnResolveArgs) (api.OnResolveResult, error) {
					// NB: mirrors the JS guard against infinite recursion when we call
					// build.Resolve below.
					if _, guarded := args.PluginData.(sandboxGuard); guarded {
						return api.OnResolveResult{}, nil
					}

					result := build.Resolve(args.Path, api.ResolveOptions{
						PluginName: "bazel-sandbox",
						Importer:   args.Importer,
						Namespace:  args.Namespace,
						ResolveDir: args.ResolveDir,
						Kind:       args.Kind,
						PluginData: sandboxGuard{},
						With:       args.With,
					})

					return resolveInExecroot(execroot, bindir, args.Importer, result)
				})
		},
	}
}

func resolveInExecroot(execroot, bindir, importer string, result api.ResolveResult) (api.OnResolveResult, error) {
	if len(result.Errors) > 0 {
		// There was an error resolving, just return the error as-is.
		return api.OnResolveResult{Errors: result.Errors, Warnings: result.Warnings}, nil
	}

	// External modules are intentionally outside the bundle and don't need path validation.
	if result.External {
		return toOnResolveResult(result), nil
	}

	if !strings.HasPrefix(result.Path, ".") &&
		!strings.HasPrefix(result.Path, "/") &&
		!strings.HasPrefix(result.Path, `\`) {
		// Not a relative or absolute path. Likely a module resolution that is marked "external".
		return toOnResolveResult(result), nil
	}

	corrected, err := correctImportPath(execroot, bindir, result.Path, importer)
	if err != nil {
		return api.OnResolveResult{}, err
	}
	result.Path = corrected
	return toOnResolveResult(result), nil
}

// correctImportPath is the Go port of correctImportPath in bazel-sandbox.js. The `firstEntry`
// parameter of the JS original is unused there too (dead parameter, kept for fidelity of the
// port -- omitted here since Go has no use for tracking it either).
func correctImportPath(execroot, bindir, resultPath, importer string) (string, error) {
	// If esbuild attempts to leave the execroot, map the path back into the execroot.
	if strings.HasPrefix(resultPath, execroot) {
		return resultPath, nil
	}

	// A relative path that is marked as external. If it was not marked as external, it would
	// error in the build.Resolve call above. We need to make it an absolute path from its
	// importer and then re-attempt correcting it to be within the execroot.
	//
	// filepath.Join(importer, resultPath) mirrors the JS original's path.resolve(importer,
	// result.path): both treat the importer's own basename as if it were a directory segment, so
	// a single ".." lands back in the importer's directory (dirname(importer) + everything after
	// the leading ".."), while "../.." consumes both the basename and dirname components. This is
	// lexical normalization only, matching Node's path.resolve/path.join semantics exactly.
	if strings.HasPrefix(resultPath, "..") {
		absPath := filepath.Join(importer, resultPath)
		return correctImportPath(execroot, bindir, absPath, importer)
	}

	// If it tried to leave bazel-bin, error out completely.
	loc := bazelOutBindirRE.FindStringIndex(resultPath)
	if loc == nil {
		return "", fmt.Errorf("esbuild resolved a path outside of bazel-out/*/bin: %s", resultPath)
	}

	// Otherwise remap the bindir-relative path, reconstructed under this action's actual
	// (possibly path-mapped) bindir rather than the real one baked into resultPath.
	relativeToBindir := resultPath[loc[1]:]
	return filepath.Join(execroot, bindir, relativeToBindir), nil
}

// toOnResolveResult carries a ResolveResult (the Go API's onResolve-callback-facing shape) over
// to an OnResolveResult (what this handler must return). The two struct shapes diverge on
// SideEffects: ResolveResult reports it as a bool, OnResolveResult wants the tri-state
// SideEffects enum, so it's translated explicitly here rather than by a direct field copy.
func toOnResolveResult(r api.ResolveResult) api.OnResolveResult {
	sideEffects := api.SideEffectsTrue
	if !r.SideEffects {
		sideEffects = api.SideEffectsFalse
	}
	return api.OnResolveResult{
		Errors:      r.Errors,
		Warnings:    r.Warnings,
		Path:        r.Path,
		External:    r.External,
		SideEffects: sideEffects,
		Namespace:   r.Namespace,
		Suffix:      r.Suffix,
		PluginData:  r.PluginData,
	}
}
