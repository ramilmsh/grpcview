package scripting

// npm.go — the EMBEDDED npm registry that makes bare npm imports resolve in PRODUCTION.
//
// The esbuild front end (bundler.go) resolves a bare import (`import dayjs from "dayjs"`).
// In production that failed: esbuild's NodePaths — the NODE_PATH-style roots bare imports
// resolve against — were empty (see WithNodePaths), so no bare import assembled. Only the
// tests wired a root at a MATERIALIZED vendored tree, so real npm libraries bundled under
// test but not in the shipped binary.
//
// This file closes that gap without a call-site change: a small, curated registry of npm
// packages is compiled INTO the binary via //go:embed (the same single-static-offline-
// binary discipline as quickjs.wasm and the UI's index.html — nothing is fetched at run
// time). Each Engine extracts the tree once, in its constructor, to a private temp dir it
// hands to the bundler as the registry root (registryResolverPlugin, bundler.go), so any
// caller — including workspace.go's default NewEngine(ctx, maxPages) with no options — can
// import a vendored package offline.
//
// Why a plugin and not NodePaths (which the field is named for)? esbuild only consults
// NodePaths for an entry that HAS a filesystem home, i.e. a non-empty Stdin.ResolveDir —
// and any non-empty ResolveDir also lets an UNTRUSTED script read arbitrary host files at
// bundle time (`import x from "/etc/passwd"` or `"../../.."`). Production deliberately keeps
// ResolveDir empty for exactly that reason, which makes NodePaths inert. The resolver
// plugin sidesteps the whole ResolveDir gate: it maps a bare specifier to a file inside the
// extracted registry (and refuses anything that escapes it), so a vendored package resolves
// while the host FS stays closed.
//
// The registry is a CLOSED ALLOWLIST: only what lives here resolves (plus the grant-gated
// node:* shims and, if a caller sets one, its own roots). A non-vendored package still
// fails to resolve — the host's real node_modules is never consulted. Growing the registry
// is a matter of vendoring another package directory under npm/ and adding it to embedsrcs
// in BUILD.bazel; for now only dayjs (1.11.21) is vendored.

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// npmRegistry is the vendored npm tree, laid out as <pkg>/package.json + sources so
// esbuild's node resolver finds each package by <root>/<pkg>. embedsrcs in BUILD.bazel
// must glob the same tree (npm/**) for the Bazel build to make these files embeddable.
//
//go:embed npm
var npmRegistry embed.FS

// npmRegistryRoot is the directory the embed is rooted at: paths in npmRegistry are
// "npm/<pkg>/...", so the walk and the Rel base below both start here.
const npmRegistryRoot = "npm"

// materializeNpmRegistry writes the embedded registry to a fresh temp dir and returns its
// CANONICAL ABSOLUTE path. os.MkdirTemp("") roots under os.TempDir() (absolute), and the
// path is run through EvalSymlinks so it matches the realpath esbuild reports for a resolved
// file — the resolver plugin's containment check (bundler.go) compares the two, and on
// macOS os.TempDir() is /var/… , a symlink to /private/var/… . This mirrors the test's
// materializeNpm, but writes to os.MkdirTemp (an Engine-owned dir removed in Close) rather
// than a test-scoped t.TempDir.
func materializeNpmRegistry() (string, error) {
	root, err := os.MkdirTemp("", "grpcview-npm-")
	if err != nil {
		return "", fmt.Errorf("scripting: create npm registry dir: %w", err)
	}
	err = fs.WalkDir(npmRegistry, npmRegistryRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(npmRegistryRoot, p)
		if err != nil {
			return err
		}
		dst := filepath.Join(root, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, err := npmRegistry.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
	if err != nil {
		_ = os.RemoveAll(root) // never leak the temp dir on a partial extraction
		return "", fmt.Errorf("scripting: extract npm registry: %w", err)
	}
	if canon, err := filepath.EvalSymlinks(root); err == nil {
		return canon, nil
	}
	return root, nil // fall back to the raw path if the FS has no symlinks to resolve
}

// provisionNpmRegistry extracts the embedded registry to a temp dir this Engine owns and
// records it as the bundler's registry root (npmDir). It must run after options are applied
// and before initBundlerAndPool, so npmDir is part of the bundler's resolver AND its cache
// salt (a run bundled against a different registry must not hit a stale cache entry). It
// composes with WithNodePaths/WithResolveDir rather than replacing them: those configure
// esbuild's own resolver for caller-supplied roots, while npmDir drives the plugin — a
// caller's tree and the embedded registry both resolve. A failure is returned to the
// constructor rather than swallowed — an Engine that silently ran without its vendored
// packages would fail bare imports in a way no caller expects.
func (e *Engine) provisionNpmRegistry() error {
	dir, err := materializeNpmRegistry()
	if err != nil {
		return err
	}
	e.npmDir = dir
	return nil
}

// removeNpmRegistry deletes the temp dir provisionNpmRegistry created (best-effort; a
// no-op if none was provisioned). Called from Close so the Engine leaves no temp state.
func (e *Engine) removeNpmRegistry() error {
	if e.npmDir == "" {
		return nil
	}
	return os.RemoveAll(e.npmDir)
}
