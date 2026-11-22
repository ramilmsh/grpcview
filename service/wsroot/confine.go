package wsroot

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrEscapes: the path leaves its root, lexically or through a symlink.
var ErrEscapes = errors.New("path escapes its root")

// errUnresolvable: generic on purpose — the underlying *PathError would name a host path.
var errUnresolvable = errors.New("path cannot be resolved")

// Confine resolves p (absolute, or relative to root) to a symlink-free path inside root.
// Lexical containment first, then EvalSymlinks of the deepest EXISTING ancestor (so a path
// not created yet resolves), then containment again against the resolved root. Callers use
// the returned path, never p: a link re-pointed after the check then cannot redirect them.
// Errors name no host path.
func Confine(root, p string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", errUnresolvable
	}
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(absRoot, abs)
	}
	abs = filepath.Clean(abs)
	if !Within(absRoot, abs) {
		return "", ErrEscapes
	}
	realRoot, err := resolveExisting(absRoot)
	if err != nil {
		return "", err
	}
	real, err := resolveExisting(abs)
	if err != nil {
		return "", err
	}
	if !Within(realRoot, real) {
		return "", ErrEscapes
	}
	return real, nil
}

// Within reports whether p is dir or lexically below it. No symlinks resolved.
func Within(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolveExisting symlink-resolves the deepest ancestor of abs that exists and re-appends
// the missing tail. A dangling link is refused: its target could be created later.
func resolveExisting(abs string) (string, error) {
	existing, tail := abs, ""
	for {
		_, err := os.Lstat(existing)
		if err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if !errors.Is(err, fs.ErrNotExist) || parent == existing {
			return "", errUnresolvable
		}
		tail = filepath.Join(filepath.Base(existing), tail)
		existing = parent
	}
	real, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", errUnresolvable
	}
	return filepath.Join(real, tail), nil
}
