package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolveWorkspaceFile resolves p — absolute, or relative to the workspace root — to a file inside
// root, and returns (the real path to READ, the root-relative path to RECORD). It is the ONE
// confinement helper: every path that reads a user-supplied file goes through it, so there is a
// single place to be right.
//
// The first return is the SYMLINK-RESOLVED path, and that is deliberately the one to hand to
// os.ReadFile: confinement is proved about the resolved path, so returning the unresolved spelling
// would leave a check-then-read window in which a link inside root is re-pointed out of it between
// the proof and the read. The second is root-relative and is the recipe a manifest records — never
// a path to read, because it is re-resolved (and re-checked) the next time it is used.
//
// The confinement is not paranoia about a hostile local user, it is about where the path comes
// from. Every one of them is WIRE input: a browser, the CLI, or a grpcview.json a colleague pushed
// — and a path RECORDED in that manifest is wire input again at refresh time, not only at add time,
// which is why the check runs on both paths and not just on the add. Unconfined this is an
// arbitrary-file-read primitive: whatever bytes parse as a FileDescriptorSet surface in the UI's
// merged descriptor set, and with commit_descriptors on they are written into a protojson sidecar
// the repo then carries.
//
// So three ways out of root are refused:
//
//   - an absolute path that is not under root;
//   - any ".." escape, checked after Clean so "a/../../etc" is caught;
//   - a SYMLINK escape — the two above are textual, and a link inside root pointing at /etc/passwd
//     passes both. root itself is resolved first, so a workspace reached through a symlink (the
//     macOS /var -> /private/var case, or a checkout under a symlinked home) is fine; it is only
//     the part of the path BELOW root that may not lead out.
//
// A directory is an error rather than a read: the caller wants a descriptor set's bytes.
//
// The returned errors are plain, deliberately: this is a path helper, and every caller wraps them
// as connect.CodeInvalidArgument at its own RPC boundary.
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

	// The symlink pass. Resolving root first is what keeps a symlinked root itself legal: both
	// sides are then compared in the same, fully resolved namespace.
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

	// realPath, not abs, is returned: it is the path confinement was PROVEN about, so reading it
	// cannot traverse a link that changed since. rel is slash-separated so the recipe a manifest
	// carries reads the same in every checkout.
	return realPath, filepath.ToSlash(rel), nil
}

// relatedToRoot reports whether dir is on the same line of descent as the workspace root: root
// itself, a descendant of it, or an ancestor of it. It is the weaker cousin of resolveWorkspaceFile,
// for the one input that legitimately sits ABOVE root (bazel.root — see Workspace.bazelBuilder) and
// so cannot be confined to it, but must still not be allowed to point sideways into an unrelated
// tree that the workspace's trust decision says nothing about.
//
// Symlinks are deliberately not resolved: this is a directory a process will be started IN, not a
// file whose bytes are read, and the trust list itself compares unresolved paths (see
// wsroot.trustKey), so resolving here would disagree with the answer trust just gave.
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
		return nil // root itself, or below it
	}
	if _, err := containedIn(absDir, absRoot); err == nil {
		return nil // above it
	}
	return fmt.Errorf("%s is neither inside %s nor an ancestor of it", absDir, absRoot)
}

// containedIn returns path's root-relative form, erroring when path is root's sibling or ancestor
// rather than its descendant. root == path is "." and counts as contained; a caller that cares (a
// file read does) rejects it on the directory check instead.
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
