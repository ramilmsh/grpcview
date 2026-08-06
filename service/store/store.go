// Package store persists a collection as a git-versionable tree of protojson files.
package store

import (
	"context"
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
	Name   *string
	Source *string
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
