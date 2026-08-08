// Package store persists a collection as a git-versionable tree of protojson files.
package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
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

type ScriptPatch struct {
	NewPath *string
	Source  *string
}

type FolderPatch struct {
	Name                *string
	DraftMetadataScript *string
}

type Store struct {
	root      string
	stateRoot string
	logger    *slog.Logger

	mu    sync.Mutex
	colls map[string]*Collection

	// blobMu serializes the whole descriptor-store critical section — write the blobs, rewrite the writing
	// collection's index, collect garbage — across every collection in this workspace. Collection.mu
	// cannot do that job: it serializes ONE collection, while the blob store and its GC are
	// workspace-wide, so a GC could delete a blob a writer in another collection had written but not yet
	// indexed.
	blobMu sync.Mutex
}

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

func (s *Store) Root() string { return s.root }

func (s *Store) Open(_ context.Context, id string) (*Collection, error) {
	cleaned, err := cleanCollectionID(id)
	if err != nil {
		return nil, err
	}
	// Fold case before using id as a map key: on macOS's case-insensitive filesystem "Requests" and
	// "requests" name the SAME directory, and Collection.mu is the only thing serializing writes to it.
	key := strings.ToLower(cleaned)
	s.mu.Lock()
	defer s.mu.Unlock()
	coll, ok := s.colls[key]
	if !ok {
		coll = &Collection{
			store:  s,
			root:   filepath.Join(s.root, cleaned),
			state:  collectionStateDir(s.stateRoot, key),
			id:     cleaned,
			key:    key,
			logger: s.logger.With("collection", cleaned),
		}
		s.colls[key] = coll
	}
	return coll, nil
}

// Rename MOVES the collection's directory inside the workspace and returns a handle on its new address.
// The returned handle replaces the caller's: every handle on either id is dropped from the cache, so any
// the caller still holds addresses the old path.
func (s *Store) Rename(ctx context.Context, id, newID string) (*Collection, error) {
	oldCleaned, err := cleanCollectionID(id)
	if err != nil {
		return nil, err
	}
	newCleaned, err := cleanCollectionID(newID)
	if err != nil {
		return nil, err
	}
	if oldCleaned == newCleaned {
		return s.Open(ctx, newCleaned)
	}
	// A collection at "." IS the workspace root directory, so moving it would move the workspace itself.
	if oldCleaned == "." || newCleaned == "." {
		return nil, fmt.Errorf("%w: the collection at %q is the workspace root and cannot be moved", ErrInvalidCollectionID, ".")
	}

	srcAbs := filepath.Join(s.root, oldCleaned)
	destAbs := filepath.Join(s.root, newCleaned)
	if !fileExists(filepath.Join(srcAbs, CollectionFileName)) {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, oldCleaned)
	}
	if _, err := os.Lstat(destAbs); err == nil {
		return nil, fmt.Errorf("%w: %q", ErrCollectionExists, newCleaned)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	rel, err := filepath.Rel(srcAbs, destAbs)
	if err != nil {
		return nil, err
	}
	if !(rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		return nil, fmt.Errorf("%w: %q is inside %q", ErrInvalidCollectionID, newCleaned, oldCleaned)
	}
	if err := s.rejectNestedDestination(destAbs); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(destAbs), 0o755); err != nil {
		return nil, err
	}
	if err := os.Rename(srcAbs, destAbs); err != nil {
		return nil, err
	}

	oldKey, newKey := strings.ToLower(oldCleaned), strings.ToLower(newCleaned)
	s.moveState(oldKey, newKey)

	// A cached handle holds root/state/id resolved at Open time, so a stale entry would keep addressing the
	// old path. Its own critical section: s.Open takes s.mu itself.
	s.mu.Lock()
	delete(s.colls, oldKey)
	delete(s.colls, newKey)
	s.mu.Unlock()

	return s.Open(ctx, newCleaned)
}

// A collection is a leaf: the scan prunes at the first grpcview.json it finds, so a collection nested
// inside another one would be invisible.
func (s *Store) rejectNestedDestination(destAbs string) error {
	root := filepath.Clean(s.root)
	for dir := filepath.Dir(destAbs); dir != root && dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if !fileExists(filepath.Join(dir, CollectionFileName)) {
			continue
		}
		enclosing, err := s.relativeID(dir)
		if err != nil {
			enclosing = dir
		}
		return fmt.Errorf("%w: the destination is inside collection %q", ErrInvalidCollectionID, enclosing)
	}
	return nil
}

// Run history and the resolved-descriptor index live here, so they follow the directory. The directory
// move already happened and cannot be taken back, so a failure here is logged, never returned.
func (s *Store) moveState(oldKey, newKey string) {
	oldDir := collectionStateDir(s.stateRoot, oldKey)
	newDir := collectionStateDir(s.stateRoot, newKey)
	if _, err := os.Stat(oldDir); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			s.logger.Warn("stat the local state of a renamed collection", "dir", oldDir, "err", err)
		}
		return
	}
	// Whatever sits at the destination belongs to a collection that no longer exists.
	if err := os.RemoveAll(newDir); err != nil {
		s.logger.Warn("clear the local state at a rename destination", "dir", newDir, "err", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(newDir), 0o755); err != nil {
		s.logger.Warn("create the local state parent of a renamed collection", "dir", newDir, "err", err)
		return
	}
	if err := os.Rename(oldDir, newDir); err != nil {
		s.logger.Warn("move the local state of a renamed collection", "from", oldDir, "to", newDir, "err", err)
	}
}

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

// A FLAT child of stateRoot keyed by a hash of the whole id, never the id's own path segments nested:
// id "." is the workspace root, so "<stateRoot>/collections/<id>/cache" would land exactly where a
// sibling collection literally named "cache" does, and any id that is a prefix of another collides one
// level up. foldedID must already be case-folded — this is keyed off the same identity as s.colls.
func collectionStateDir(stateRoot, foldedID string) string {
	return filepath.Join(stateRoot, collectionsStateSubdir, hashedName(foldedID))
}

type Collection struct {
	store  *Store
	root   string
	state  string
	id     string
	key    string
	logger *slog.Logger

	mu sync.Mutex
}

func (c *Collection) Root() string  { return c.root }
func (c *Collection) State() string { return c.state }

// Key is the cleaned, case-folded id. Anything memoizing per collection must key on THIS rather than
// on the id it passed to Open: two memo entries for one directory would let an invalidation miss the
// entry a reader is about to use.
func (c *Collection) Key() string { return c.key }

func (c *Collection) collectionFilePath() string { return filepath.Join(c.root, CollectionFileName) }
func (c *Collection) treeRoot() string           { return filepath.Join(c.root, treeDir) }
func (c *Collection) scriptsRoot() string        { return filepath.Join(c.root, scriptsDir) }
func (c *Collection) historyRoot() string        { return filepath.Join(c.state, historyDir) }

func (c *Collection) defaultName() string { return filepath.Base(c.root) }
