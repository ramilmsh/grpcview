// Package wsroot resolves the workspace root — the repository grpcview was opened
// in — and the durable local-state directory belonging to it.
package wsroot

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Discover resolves the workspace root, first hit winning:
//  1. override, which must already exist and be a directory;
//  2. the nearest ancestor of cwd holding .git;
//  3. cwd, with warn set.
//
// The returned path is absolute and cleaned. warn is a sentence to log, empty
// unless rule 3 applied.
func Discover(override, cwd string) (root string, warn string, err error) {
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

	// Rule 2: walk up from cwd looking for a .git entry. It may be a directory (an
	// ordinary repo) or a regular file (a git worktree or submodule, whose .git
	// file points elsewhere) — either is enough to call dir the workspace root.
	// We deliberately do not resolve symlinks: a user who cd's through a symlink
	// means the path they typed.
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

	// Rule 3: nothing found. Fall back to cwd itself and say so.
	warn = fmt.Sprintf(
		"no repository (.git) found above %q; treating it as the workspace root — pass --workspace to name one explicitly",
		absCwd,
	)
	return absCwd, warn, nil
}

// StateDir returns the durable local-state directory for the workspace at root:
// one directory per workspace, keyed by root's absolute path.
func StateDir(root string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("failed to resolve workspace root %q: %w", root, err)
	}
	absRoot = filepath.Clean(absRoot)

	// os.UserConfigDir, not os.UserCacheDir: run history and other local state are
	// user data, not disposable cache — ~/Library/Caches (and its XDG_CACHE_HOME
	// equivalent) is fair game for the OS or the user to reclaim at any time, and
	// losing history silently on the next reboot is not acceptable.
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user config dir: %w", err)
	}

	sum := sha256.Sum256([]byte(absRoot))
	hash := hex.EncodeToString(sum[:])[:12]
	key := slugify(filepath.Base(absRoot)) + "-" + hash

	return filepath.Join(configDir, "grpcview", "workspaces", key), nil
}

// slugify lowercases s and collapses every run of characters outside [a-z0-9] into
// a single '-', trimming any leading or trailing '-'. It exists purely so a human
// browsing the state directory can tell workspaces apart by eye; uniqueness comes
// from the hash suffix in StateDir, not from this.
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
