package store

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

var ErrInvalidPath = errors.New("invalid workspace path")

// Hidden reports whether a directory entry is invisible: dot-entries (`.env`, `.tmp-*` atomic-write
// temps) except MountDirName, node_modules and bazel-*. The one prune predicate for discovery, the
// module scan and the served FS.
func Hidden(name string) bool {
	if name == MountDirName {
		return false
	}
	return strings.HasPrefix(name, ".") || name == nodeModulesDirName || strings.HasPrefix(name, bazelSymlinkPrefix)
}

type PathKind int

const (
	PathOther PathKind = iota
	PathManifest
	PathRequestBody
	PathRequestMetadata
	PathFolderMetadata
	PathScript
	PathMount
)

func (k PathKind) String() string {
	switch k {
	case PathManifest:
		return "manifest"
	case PathRequestBody:
		return "request-body"
	case PathRequestMetadata:
		return "request-metadata"
	case PathFolderMetadata:
		return "folder-metadata"
	case PathScript:
		return "script"
	case PathMount:
		return "mount"
	}
	return "other"
}

type ResolvedPath struct {
	// Collection is the id of the collection holding the path ("." for the root), "" outside every one.
	Collection string
	Kind       PathKind
	// SlugPath addresses, below tree/, the item the path is or sits directly in; nil otherwise.
	SlugPath []string
	// File is POSIX, relative to the collection root, or to the workspace root outside every collection.
	File string
}

// ResolvePath classifies a workspace-rooted POSIX path ("/" is the workspace root). Lexical over slugs,
// plus the stats that tell a request from a folder; the target itself need not exist.
func (s *Store) ResolvePath(wsPath string) (ResolvedPath, error) {
	segs, err := splitWorkspacePath(wsPath)
	if err != nil {
		return ResolvedPath{}, err
	}
	ids, err := s.collectionIDs()
	if err != nil {
		return ResolvedPath{}, err
	}
	id, rest := s.matchCollection(ids, segs)

	r := ResolvedPath{Collection: id, File: strings.Join(rest, "/")}
	switch {
	case len(segs) > 0 && segs[0] == MountDirName:
		r.Kind = PathMount
	case len(segs) == 1 && segs[0] == WorkspaceFileName:
		r.Kind = PathManifest
	case id != "":
		r.Kind, r.SlugPath = classifyInCollection(filepath.Join(s.root, filepath.FromSlash(id)), rest)
	}
	return r, nil
}

func splitWorkspacePath(p string) ([]string, error) {
	if !strings.HasPrefix(p, "/") {
		return nil, fmt.Errorf("%w: %q is not rooted at /", ErrInvalidPath, p)
	}
	if slices.Contains(strings.Split(p, "/"), "..") {
		return nil, fmt.Errorf("%w: %q", ErrInvalidPath, p)
	}
	clean := path.Clean(p)
	if clean == "/" {
		return nil, nil
	}
	return strings.Split(clean[1:], "/"), nil
}

// Longest prefix wins, so a "." collection only claims what no other collection does. A pinned id
// whose manifest is missing is not a collection.
func (s *Store) matchCollection(ids, segs []string) (id string, rest []string) {
	best := -1
	for _, cand := range ids {
		cleaned, err := cleanCollectionID(cand)
		if err != nil {
			continue
		}
		var prefix []string
		if cleaned != "." {
			prefix = strings.Split(filepath.ToSlash(cleaned), "/")
		}
		if len(prefix) <= best || len(prefix) > len(segs) || !slices.Equal(prefix, segs[:len(prefix)]) {
			continue
		}
		if !fileExists(filepath.Join(s.root, cleaned, CollectionFileName)) {
			continue
		}
		best, id = len(prefix), filepath.ToSlash(cleaned)
	}
	if best < 0 {
		return "", segs
	}
	return id, segs[best:]
}

func classifyInCollection(collRoot string, rest []string) (PathKind, []string) {
	if len(rest) == 0 {
		return PathOther, nil
	}
	switch rest[0] {
	case CollectionFileName:
		if len(rest) == 1 {
			return PathManifest, nil
		}
	case MountDirName:
		return PathMount, nil
	case scriptsDir:
		if isScript(rest[1:]) {
			return PathScript, nil
		}
	case treeDir:
		return classifyInTree(filepath.Join(collRoot, treeDir), rest[1:])
	}
	return PathOther, nil
}

// isScript: rel (segments below scripts/) names a *.ts with no dot-named segment — the rule
// readScripts lists by.
func isScript(rel []string) bool {
	if len(rel) == 0 || path.Ext(rel[len(rel)-1]) != ".ts" {
		return false
	}
	for _, seg := range rel {
		if strings.HasPrefix(seg, ".") {
			return false
		}
	}
	return true
}

func classifyInTree(treeRoot string, slugs []string) (PathKind, []string) {
	_, kind, n := walkSlugs(treeRoot, slugs)
	switch {
	case n == 0:
		return PathOther, nil
	case n == len(slugs):
		return PathOther, slugs
	case n < len(slugs)-1:
		return PathOther, nil
	}
	parent, name := slugs[:n], slugs[n]
	switch {
	case kind == kindRequest && name == requestFileName, kind == kindFolder && name == folderFileName:
		return PathManifest, parent
	case kind == kindRequest && name == bodyFileName:
		return PathRequestBody, parent
	case kind == kindRequest && name == metadataFileName:
		return PathRequestMetadata, parent
	case kind == kindFolder && name == metadataFileName:
		return PathFolderMetadata, parent
	}
	return PathOther, parent
}

// Walks slugs below treeRoot as readChildren sees them, every ancestor a folder; returns the deepest
// item reached and how many slugs led to it.
func walkSlugs(treeRoot string, slugs []string) (dir string, kind itemKind, n int) {
	dir = treeRoot
	for _, slug := range slugs {
		if (n > 0 && kind != kindFolder) || slug == "" || strings.ContainsRune(slug, filepath.Separator) {
			break
		}
		k, ok := itemKindAt(dir, slug)
		if !ok {
			break
		}
		dir, kind, n = filepath.Join(dir, slug), k, n+1
	}
	return dir, kind, n
}

// slugItem is the item slugs addresses: every ancestor a folder, the last any item.
func slugItem(treeRoot string, slugs []string) (dir string, kind itemKind, ok bool) {
	dir, kind, n := walkSlugs(treeRoot, slugs)
	return dir, kind, n > 0 && n == len(slugs)
}

// itemKindAt is the item parent/slug holds: none for a dot-named or reserved slug; folder.json wins
// over request.json. The one predicate readChildren and ResolvePath share.
func itemKindAt(parent, slug string) (itemKind, bool) {
	if strings.HasPrefix(slug, ".") || isReserved(slug) {
		return 0, false
	}
	dir := filepath.Join(parent, slug)
	switch {
	case fileExists(filepath.Join(dir, folderFileName)):
		return kindFolder, true
	case fileExists(filepath.Join(dir, requestFileName)):
		return kindRequest, true
	}
	return 0, false
}

// IsItemScript reports whether name in dir is an item's own script, not a module: body.ts or
// metadata.ts beside request.json, metadata.ts beside folder.json.
func IsItemScript(dir, name string) bool {
	switch name {
	case bodyFileName:
		return fileExists(filepath.Join(dir, requestFileName))
	case metadataFileName:
		return fileExists(filepath.Join(dir, requestFileName)) || fileExists(filepath.Join(dir, folderFileName))
	}
	return false
}

var errNotItemFile = errors.New("not a writable item file")

// writeItemFile writes name (body.ts or metadata.ts) in the item slugPath addresses. body.ts needs a
// request; metadata.ts either kind. Callers must hold c.lockWrite.
func (c *Collection) writeItemFile(slugPath []string, name, src string) error {
	dir, kind, ok := slugItem(c.treeRoot(), slugPath)
	if !ok {
		return fmt.Errorf("%w: %q", ErrItemNotFound, strings.Join(slugPath, "/"))
	}
	if name != metadataFileName && (name != bodyFileName || kind != kindRequest) {
		return fmt.Errorf("%w: %q", errNotItemFile, name)
	}
	return c.tx.writeSource(filepath.Join(dir, name), src)
}
