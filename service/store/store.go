// Package store persists a grpcview collection as a git-versionable directory tree of
// protojson files: a grpcview.json manifest plus tree/ and scripts/. A collection's local
// state (resolved-schema cache, run history) is kept OUTSIDE that tree entirely, under a
// separate state root the caller supplies — see Store.New and Collection.state.
package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

var (
	ErrNotFound            = errors.New("collection not found")
	ErrItemNotFound        = errors.New("item not found")
	ErrNotAFolder          = errors.New("item is not a folder")
	ErrNotARequest         = errors.New("item is not a request")
	ErrAlreadyExists       = errors.New("item already exists")
	ErrCollectionExists    = errors.New("collection already exists")
	ErrMoveIntoDescendant  = errors.New("cannot move an item into itself or its own descendant")
	ErrInvalidCollectionID = errors.New("invalid collection id")
)

// RequestPatch is a partial update to a request; a nil field is left unchanged.
// Middleware and Target have no nil "unset", so SetMiddleware/SetTarget gate them.
type RequestPatch struct {
	Name                *string
	Service             *string
	Method              *string
	DraftBody           *string
	DraftMetadataScript *string
	Middleware          []string
	SetMiddleware       bool
	Target              *grpcviewv1.Server
	SetTarget           bool
}

// ScriptPatch is a partial update to a script; a nil field is left unchanged.
type ScriptPatch struct {
	Name   *string
	Source *string
}

// FolderPatch is a partial update to a folder; a nil field is left unchanged.
type FolderPatch struct {
	Name                *string
	DraftMetadataScript *string
}

// Store manages filesystem-backed collections rooted under a workspace: root is the
// workspace's own directory (a collection at id "foo/bar" lives at root/foo/bar), and
// stateRoot is where each collection's local state (caches, history) is kept instead —
// see Collection.state for why that can't simply be a subdirectory of the collection.
type Store struct {
	root      string
	stateRoot string
	logger    *slog.Logger

	mu    sync.Mutex
	colls map[string]*Collection
}

// New returns a Store rooting collections under root (the workspace root) and keeping
// their local state under stateRoot; a nil logger uses slog.Default().
func New(root, stateRoot string, logger *slog.Logger) *Store {
	if logger == nil {
		logger = slog.Default()
	}
	return &Store{
		root:      root,
		stateRoot: stateRoot,
		logger:    logger,
		colls:     make(map[string]*Collection),
	}
}

// Root is the workspace root — the directory collection ids are relative to. It is exposed
// so a client with a cwd of its own (the CLI) can resolve that cwd against the workspace it
// is talking to, which it cannot do from a relative id alone.
func (s *Store) Root() string { return s.root }

// Open returns the collection handle addressed by id, a workspace-relative path ("." for
// the workspace root itself, "services/payments/requests" for a subdirectory); it does not
// create the collection on disk. id comes off the wire, so it is cleaned and checked against
// traversal before anything else: an absolute path or a ".." that would resolve outside root
// is rejected with ErrInvalidCollectionID.
func (s *Store) Open(_ context.Context, id string) (*Collection, error) {
	cleaned, err := cleanCollectionID(id)
	if err != nil {
		return nil, err
	}
	// Fold case before using id as a map key: on macOS's (default) case-insensitive
	// filesystem, "Requests" and "requests" name the SAME directory, and Collection.mu is
	// the only thing serializing writes to it — two *Collection handles on one directory
	// would let two writers race. layout.go's uniqueSlug folds case for the identical reason.
	key := strings.ToLower(cleaned)
	s.mu.Lock()
	defer s.mu.Unlock()
	coll, ok := s.colls[key]
	if !ok {
		coll = &Collection{
			root:   filepath.Join(s.root, cleaned),
			state:  collectionStateDir(s.stateRoot, key),
			id:     cleaned,
			logger: s.logger.With("collection", cleaned),
		}
		s.colls[key] = coll
	}
	return coll, nil
}

// cleanCollectionID cleans id and rejects anything that could name a path outside the
// workspace root: an absolute path, or a ".." that survives Clean because it walks above
// the root. "." (the workspace root itself) is legal.
func cleanCollectionID(id string) (string, error) {
	cleaned := filepath.Clean(id)
	if filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("%w: %q", ErrInvalidCollectionID, id)
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q", ErrInvalidCollectionID, id)
	}
	return cleaned, nil
}

// collectionStateDir computes a collection's local-state directory as a FLAT child of
// stateRoot, keyed by a hash of the whole (case-folded) id — never by nesting the id's own
// path segments under stateRoot.
//
// Nesting would collide: a collection's id is a workspace-relative path, and id "."
// addresses the workspace root itself, so "<stateRoot>/collections/<id>/cache" would put
// that collection's cache at "<stateRoot>/collections/cache" — exactly where a sibling
// collection literally named "cache" would ALSO land. Any id that happens to be a prefix or
// suffix of another id has the same problem one level up. Hashing the whole id into one
// path segment sidesteps it regardless of depth or naming.
//
// foldedID must already be case-folded (strings.ToLower) by the caller: this dir is keyed
// off the same identity as the s.colls handle map, because two different-case ids that name
// one directory on a case-insensitive filesystem must not also get two different state dirs.
func collectionStateDir(stateRoot, foldedID string) string {
	sum := sha256.Sum256([]byte(foldedID))
	key := slugify(foldedID) + "-" + hex.EncodeToString(sum[:6])
	return filepath.Join(stateRoot, "collections", key)
}

// Collection serializes its mutations, so concurrent RPCs for one workspace can't interleave writes.
type Collection struct {
	root   string // committed content: manifest, tree/, scripts/
	state  string // local state: resolved-schema cache, run history — see collectionStateDir
	id     string // the workspace-relative address Store.Open was called with; never a display name
	logger *slog.Logger

	mu sync.Mutex
}

func (c *Collection) Root() string  { return c.root }
func (c *Collection) State() string { return c.state }

func (c *Collection) collectionFilePath() string { return filepath.Join(c.root, CollectionFileName) }
func (c *Collection) treeRoot() string           { return filepath.Join(c.root, treeDir) }
func (c *Collection) scriptsRoot() string        { return filepath.Join(c.root, scriptsDir) }
func (c *Collection) servicesCachePath() string {
	return filepath.Join(c.state, cacheSubdir, servicesCacheFileName)
}

func (c *Collection) sourcesCacheRoot() string {
	return filepath.Join(c.state, cacheSubdir, sourcesCacheSubdir)
}

// sourceCachePath is slug + id hash: two ids that slugify alike (localhost:8080 vs
// localhost.8080) must not share a file.
func (c *Collection) sourceCachePath(id string) string {
	sum := sha256.Sum256([]byte(id))
	name := fmt.Sprintf("%s-%s%s", slugify(id), hex.EncodeToString(sum[:6]), sourceCacheFileExt)
	return filepath.Join(c.sourcesCacheRoot(), name)
}
func (c *Collection) historyRoot() string { return filepath.Join(c.state, historyDir) }

// defaultName is the display name a collection gets when its manifest doesn't specify one:
// the base name of its own directory. This holds even for id ".", since c.root for that
// collection IS the workspace root directory — its base name is the right default too.
func (c *Collection) defaultName() string { return filepath.Base(c.root) }
