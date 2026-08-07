// Package wsroot resolves the workspace root and its local-state directory.
package wsroot

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Set by `bazel run`, which replaces cwd with the runfiles tree.
const BuildWorkspaceEnv = "BUILD_WORKSPACE_DIRECTORY"

// InvocationDir is where the user stands, which under `bazel run` is not cwd.
func InvocationDir() (string, error) {
	if dir := os.Getenv(BuildWorkspaceEnv); dir != "" {
		return dir, nil
	}
	return os.Getwd()
}

// Discover resolves the workspace root, in order: override, else $BUILD_WORKSPACE_DIRECTORY,
// else the nearest ancestor of cwd holding .git, else cwd with warn set. .git may be a regular
// FILE (a git worktree or submodule), which counts. Symlinks are deliberately not resolved: a
// user who cd's through one means the path they typed.
func Discover(override, cwd string) (root string, warn string, err error) {
	if override == "" {
		override = os.Getenv(BuildWorkspaceEnv)
	}
	if override != "" {
		abs := override
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(cwd, abs)
		}
		abs, err = filepath.Abs(abs)
		if err != nil {
			return "", "", fmt.Errorf("failed to resolve workspace %q: %w", override, err)
		}
		abs = filepath.Clean(abs)

		info, statErr := os.Stat(abs)
		if statErr != nil || !info.IsDir() {
			return "", "", fmt.Errorf("workspace %q is not an existing directory", abs)
		}
		return abs, "", nil
	}

	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve cwd %q: %w", cwd, err)
	}
	absCwd = filepath.Clean(absCwd)

	for dir := absCwd; ; {
		if _, statErr := os.Stat(filepath.Join(dir, ".git")); statErr == nil {
			return dir, "", nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	warn = fmt.Sprintf(
		"no repository (.git) found above %q; treating it as the workspace root — pass --workspace to name one explicitly",
		absCwd,
	)
	return absCwd, warn, nil
}

// ConfigDirEnv redirects every durable thing grpcview owns — the per-workspace state dirs and
// the trust list — somewhere else. It exists for throwaway runs (CI, `//example:up --isolated`),
// which must not write into the developer's real history. Overriding HOME instead would work
// too, and would also relocate the output base of the `bazel build` a bazel source shells out
// to, which is not the same request at all.
const ConfigDirEnv = "GRPCVIEW_CONFIG_DIR"

func configRoot() (string, error) {
	if dir := os.Getenv(ConfigDirEnv); dir != "" {
		return dir, nil
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user config dir: %w", err)
	}
	return filepath.Join(configDir, "grpcview"), nil
}

// os.UserConfigDir, not os.UserCacheDir: run history and other local state are user data, not
// disposable cache — a cache directory is fair game for the OS or the user to reclaim, and losing
// history silently on the next reboot is not acceptable. Uniqueness comes from the hash suffix, not
// from the slug, which is there so a human can tell workspaces apart by eye.
func StateDir(root string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("failed to resolve workspace root %q: %w", root, err)
	}
	absRoot = filepath.Clean(absRoot)

	configDir, err := configRoot()
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256([]byte(absRoot))
	hash := hex.EncodeToString(sum[:])[:12]
	key := slugify(filepath.Base(absRoot)) + "-" + hash

	return filepath.Join(configDir, "workspaces", key), nil
}

func slugify(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		case !prevDash:
			b.WriteByte('-')
			prevDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "root"
	}
	return slug
}
