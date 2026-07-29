// Package store persists a grpcview collection as a git-versionable directory
// tree of protojson files instead of a single opaque binary blob.
//
// On-disk layout of a collection rooted at some directory:
//
//	<root>/
//	  grpcview.json          manifest: schemaVersion, name, root ordering, sources
//	  .gitignore             generated; ignores .grpcview/
//	  tree/                  the request tree
//	    <slug>/              a folder
//	      folder.json          {meta:{name}, items:[child slugs]}
//	      <slug>/ ...
//	    <slug>/              a request
//	      request.json         {meta:{name}, service, method, draftBody?, draftMetadataScript?}
//	  .grpcview/             gitignored local state
//	    cache/services.json    merged resolved-schema cache (derived)
//	    cache/sources/<f>.binpb  one descriptor source's last resolve
//
// A directory is named by a stable slug; the display name lives in the config's
// meta.name. Ordering is an explicit items[] slug list in the parent's config,
// reconciled against disk on load. All writes are atomic (temp file + rename)
// and mutations on a collection are serialized by a per-collection mutex.
package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

// Sentinel errors. Callers (the RPC handlers) map these to transport error
// codes; the store itself stays transport-agnostic.
var (
	// ErrNotFound means the collection has not been created yet.
	ErrNotFound = errors.New("collection not found")
	// ErrItemNotFound means a path segment or item does not exist.
	ErrItemNotFound = errors.New("item not found")
	// ErrNotAFolder means a path segment that must be a folder is not one.
	ErrNotAFolder = errors.New("item is not a folder")
	// ErrNotARequest means an operation expected a request but found a folder.
	ErrNotARequest = errors.New("item is not a request")
	// ErrAlreadyExists means an item with that display name already exists in
	// the parent folder.
	ErrAlreadyExists = errors.New("item already exists")
)

// RequestPatch is a partial update to a request. A nil field is left unchanged;
// a non-nil field is applied — matching the optional RPC field semantics, so an
// empty-but-present DraftBody ("") clears the body. Name renames the request's
// display name; because the store keys items by a stable slug (not the name),
// the rename only rewrites meta.name and the on-disk directory stays put.
//
// Middleware (the ordered attached-middleware names) is a repeated field, which
// can't be a pointer-nil "unset", so SetMiddleware is its explicit set-flag: when
// true the list is replaced by Middleware (nil/empty clears it); when false the
// list is left unchanged.
type RequestPatch struct {
	Name      *string
	Service   *string
	Method    *string
	DraftBody *string
	// DraftMetadataScript patches the request's metadata source (the TypeScript
	// module evaluated on invoke). Like DraftBody it is a plain *string: nil
	// leaves it unchanged, an empty-but-present value clears it.
	DraftMetadataScript *string
	Middleware          []string
	SetMiddleware       bool
	// Target patches the per-request invoke destination. Like Middleware it is a
	// message with no pointer-nil "unset", so SetTarget is its set-flag: when true
	// the target is replaced by Target (nil clears it); when false it is unchanged.
	Target    *grpcviewv1.Server
	SetTarget bool
}

// ScriptPatch is a partial update to a script. A nil field is left unchanged; a
// non-nil field is applied. Name renames the script's display name (the slug/dir
// stays stable, like RequestPatch.Name); Source replaces the authored source.
type ScriptPatch struct {
	Name   *string
	Source *string
}

// FolderPatch is a partial update to a folder. Like RequestPatch, a nil field is
// left unchanged and a non-nil field is applied, so an empty-but-present
// DraftMetadataScript ("") clears the script.
type FolderPatch struct {
	// DraftMetadataScript patches the folder's metadata source (the TypeScript
	// module folded into gv.metadata.inherit()'s ancestor chain). nil leaves it
	// unchanged; a non-nil value (including "") replaces it.
	DraftMetadataScript *string
}

// Store manages filesystem-backed collections rooted under a common base
// directory. Phase 1 uses name-based addressing: a workspace name maps to
// <base>/<name>. It hands out per-name Collection handles that serialize their
// own mutations, and caches them so repeated access shares the same lock.
type Store struct {
	base   string
	logger *slog.Logger

	mu    sync.Mutex
	colls map[string]*Collection
}

// New returns a Store rooting collections under base. A nil logger falls back to
// slog.Default().
func New(base string, logger *slog.Logger) *Store {
	if logger == nil {
		logger = slog.Default()
	}
	return &Store{
		base:   base,
		logger: logger,
		colls:  make(map[string]*Collection),
	}
}

// Open returns the handle for the named collection. It does not create a new
// collection; callers that must (Get) use Collection.EnsureCreated.
func (s *Store) Open(_ context.Context, name string) (*Collection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	coll, ok := s.colls[name]
	if !ok {
		coll = &Collection{
			root:   filepath.Join(s.base, name),
			name:   name,
			logger: s.logger.With("collection", name),
		}
		s.colls[name] = coll
	}
	return coll, nil
}

// Collection is a handle to a single collection on disk. Its mutex serializes
// mutations so concurrent RPCs for the same workspace can't interleave writes.
type Collection struct {
	root   string
	name   string
	logger *slog.Logger

	mu sync.Mutex
}

// Root returns the collection's on-disk directory.
func (c *Collection) Root() string { return c.root }

func (c *Collection) collectionFilePath() string { return filepath.Join(c.root, collectionFileName) }
func (c *Collection) treeRoot() string           { return filepath.Join(c.root, treeDir) }
func (c *Collection) scriptsRoot() string        { return filepath.Join(c.root, scriptsDir) }
func (c *Collection) servicesCachePath() string {
	return filepath.Join(c.root, stateDir, cacheSubdir, servicesCacheFileName)
}

// sourcesCacheRoot holds one file per descriptor source's last resolve.
func (c *Collection) sourcesCacheRoot() string {
	return filepath.Join(c.root, stateDir, cacheSubdir, sourcesCacheSubdir)
}

// sourceCachePath is a source's resolve-cache file. Source ids are opaque
// user-derived strings (a dial address, an upload's file name), so the file name
// is the id's slug plus a hash of the full id — the slug keeps the directory
// legible while the hash keeps two ids that slugify alike (localhost:8080 vs
// localhost.8080) on distinct files.
func (c *Collection) sourceCachePath(id string) string {
	sum := sha256.Sum256([]byte(id))
	name := fmt.Sprintf("%s-%s%s", slugify(id), hex.EncodeToString(sum[:6]), sourceCacheFileExt)
	return filepath.Join(c.sourcesCacheRoot(), name)
}
func (c *Collection) historyRoot() string { return filepath.Join(c.root, stateDir, historyDir) }
