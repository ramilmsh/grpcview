package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"codeberg.org/ramilmsh/grpcview/service/wsroot"
)

// Resolves p to a file inside root and returns (the path to READ, the root-relative path to RECORD).
// Every path that reads a user-supplied file goes through it.
//
// The first return is SYMLINK-RESOLVED (wsroot.Confine) deliberately: confinement is proved about the
// resolved path, so returning the unresolved spelling would leave a check-then-read window in which a
// link inside root is re-pointed out of it. The second is the recipe a manifest records — never a path
// to read, because it is re-resolved and re-checked every time it is used: a RECORDED path is wire
// input again at refresh time, not only at add time.
func resolveWorkspaceFile(root, p string) (real string, rel string, err error) {
	real, rel, isDir, err := resolveWorkspacePath(root, p)
	if err != nil {
		return "", "", err
	}
	if isDir {
		return "", "", fmt.Errorf("%q is a directory, not a descriptor set", p)
	}
	return real, rel, nil
}

// resolveWorkspaceFile, for a file or a directory.
func resolveWorkspacePath(root, p string) (real string, rel string, isDir bool, err error) {
	if strings.TrimSpace(p) == "" {
		return "", "", false, fmt.Errorf("empty path")
	}
	real, err = wsroot.Confine(root, p)
	if errors.Is(err, wsroot.ErrEscapes) {
		return "", "", false, fmt.Errorf("%q is outside the workspace: sources have to live in the workspace", p)
	}
	if err != nil {
		return "", "", false, fmt.Errorf("cannot read %q: %w", p, err)
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", "", false, fmt.Errorf("cannot read %q: no such file or directory", p)
	}

	// Confine proved lexical containment, so the recorded recipe is root-relative.
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", "", false, fmt.Errorf("cannot read %q: %w", p, err)
	}
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(absRoot, abs)
	}
	if rel, err = filepath.Rel(absRoot, abs); err != nil {
		return "", "", false, fmt.Errorf("cannot read %q: %w", p, err)
	}
	return real, filepath.ToSlash(rel), info.IsDir(), nil
}

// The weaker cousin of resolveWorkspaceFile, for the one input that legitimately sits ABOVE root
// (bazel.root) and so cannot be confined to it, but must still not point sideways into an unrelated
// tree the workspace's trust decision says nothing about. Symlinks are deliberately not resolved: the
// trust list compares unresolved paths too (wsroot.trustKey).
func relatedToRoot(root, dir string) error {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("failed to resolve workspace root %q: %w", root, err)
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("failed to resolve %q: %w", dir, err)
	}
	if wsroot.Within(absRoot, absDir) || wsroot.Within(absDir, absRoot) {
		return nil
	}
	return fmt.Errorf("%s is neither inside %s nor an ancestor of it", absDir, absRoot)
}
