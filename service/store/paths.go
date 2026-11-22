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

// Mirrors readScripts: *.ts, no dot-named segment below scripts/.
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
	if len(slugs) == 0 {
		return PathOther, nil
	}
	if _, _, ok := slugItem(treeRoot, slugs); ok {
		return PathOther, slugs
	}
	parent, name := slugs[:len(slugs)-1], slugs[len(slugs)-1]
	_, kind, ok := slugItem(treeRoot, parent)
	if !ok {
		return PathOther, nil
	}
	switch {
	case kind == kindRequest && name == RequestFileName, kind == kindFolder && name == folderFileName:
		return PathManifest, parent
	case kind == kindRequest && name == BodyFileName:
		return PathRequestBody, parent
	case kind == kindRequest && name == MetadataFileName:
		return PathRequestMetadata, parent
	case kind == kindFolder && name == MetadataFileName:
		return PathFolderMetadata, parent
	}
	return PathOther, parent
}

// Walks slugs below treeRoot as readChildren sees them: every ancestor a folder, the last any item.
func slugItem(treeRoot string, slugs []string) (dir string, kind itemKind, ok bool) {
	if len(slugs) == 0 {
		return "", 0, false
	}
	dir = treeRoot
	for i, slug := range slugs {
		if slug == "" || strings.HasPrefix(slug, ".") || isReserved(slug) || strings.ContainsRune(slug, filepath.Separator) {
			return "", 0, false
		}
		dir = filepath.Join(dir, slug)
		switch {
		case fileExists(filepath.Join(dir, folderFileName)):
			kind = kindFolder
		case fileExists(filepath.Join(dir, RequestFileName)):
			kind = kindRequest
		default:
			return "", 0, false
		}
		if i < len(slugs)-1 && kind != kindFolder {
			return "", 0, false
		}
	}
	return dir, kind, true
}

var errNotItemFile = errors.New("not a writable item file")

// writeItemFile writes name (body.ts or metadata.ts) in the item slugPath addresses. body.ts needs a
// request; metadata.ts either kind. Callers must hold c.mu.
func (c *Collection) writeItemFile(slugPath []string, name, src string) error {
	dir, kind, ok := slugItem(c.treeRoot(), slugPath)
	if !ok {
		return fmt.Errorf("%w: %q", ErrItemNotFound, strings.Join(slugPath, "/"))
	}
	if name != MetadataFileName && (name != BodyFileName || kind != kindRequest) {
		return fmt.Errorf("%w: %q", errNotItemFile, name)
	}
	return writeSourceFile(filepath.Join(dir, name), src)
}
