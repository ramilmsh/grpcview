package scripting

// The embedded npm registry: a curated set of npm packages compiled into the binary and
// extracted per Engine, so bare imports resolve offline while the host FS stays closed.

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// BUILD.bazel's embedsrcs must glob the same tree (npm/**) for Bazel to embed these files.
//
//go:embed npm
var npmRegistry embed.FS

const npmRegistryRoot = "npm"

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
		_ = os.RemoveAll(root)
		return "", fmt.Errorf("scripting: extract npm registry: %w", err)
	}
	// Canonicalize: the containment check compares against the realpath esbuild reports,
	// and on macOS os.TempDir() is /var/…, a symlink to /private/var/… .
	if canon, err := filepath.EvalSymlinks(root); err == nil {
		return canon, nil
	}
	return root, nil
}

func (e *Engine) provisionNpmRegistry() error {
	dir, err := materializeNpmRegistry()
	if err != nil {
		return err
	}
	e.npmDir = dir
	return nil
}

func (e *Engine) removeNpmRegistry() error {
	if e.npmDir == "" {
		return nil
	}
	return os.RemoveAll(e.npmDir)
}
