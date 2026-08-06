package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Resolves p to a file inside root and returns (the path to READ, the root-relative path to RECORD).
// Every path that reads a user-supplied file goes through it.
//
// The first return is SYMLINK-RESOLVED deliberately: confinement is proved about the resolved path, so
// returning the unresolved spelling would leave a check-then-read window in which a link inside root
// is re-pointed out of it. The second is the recipe a manifest records — never a path to read, because
// it is re-resolved and re-checked every time it is used: a RECORDED path is wire input again at
// refresh time, not only at add time.
//
// Three ways out of root are refused: an absolute path not under root; any ".." surviving Clean; and a
// SYMLINK escape, which the two textual checks miss. root itself is resolved first, so a workspace
// reached through a symlink is legal.
func resolveWorkspaceFile(root, p string) (real string, rel string, err error) {
	if strings.TrimSpace(p) == "" {
		return "", "", fmt.Errorf("empty path")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve workspace root %q: %w", root, err)
	}
	absRoot = filepath.Clean(absRoot)

	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(absRoot, abs)
	}
	abs = filepath.Clean(abs)

	rel, err = containedIn(absRoot, abs)
	if err != nil {
		return "", "", fmt.Errorf("%q is outside the workspace %s: a descriptor set has to live in the workspace", p, absRoot)
	}

	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve workspace root %q: %w", absRoot, err)
	}
	realPath, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", "", fmt.Errorf("cannot read %q: %w", p, err)
	}
	if _, err := containedIn(realRoot, realPath); err != nil {
		return "", "", fmt.Errorf("%q resolves through a symlink to %s, outside the workspace %s", p, realPath, absRoot)
	}

	info, err := os.Stat(realPath)
	if err != nil {
		return "", "", fmt.Errorf("cannot read %q: %w", p, err)
	}
	if info.IsDir() {
		return "", "", fmt.Errorf("%q is a directory, not a descriptor set", p)
	}

	return realPath, filepath.ToSlash(rel), nil
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
	absRoot, absDir = filepath.Clean(absRoot), filepath.Clean(absDir)
	if _, err := containedIn(absRoot, absDir); err == nil {
		return nil
	}
	if _, err := containedIn(absDir, absRoot); err == nil {
		return nil
	}
	return fmt.Errorf("%s is neither inside %s nor an ancestor of it", absDir, absRoot)
}

func containedIn(root, path string) (string, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s escapes %s", path, root)
	}
	return rel, nil
}
