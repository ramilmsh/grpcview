// Package store persists a grpcview collection as a git-versionable directory tree
// of protojson files: a grpcview.json manifest plus tree/, scripts/ and .grpcview/.
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

var (
	ErrNotFound           = errors.New("collection not found")
	ErrItemNotFound       = errors.New("item not found")
	ErrNotAFolder         = errors.New("item is not a folder")
	ErrNotARequest        = errors.New("item is not a request")
	ErrAlreadyExists      = errors.New("item already exists")
	ErrMoveIntoDescendant = errors.New("cannot move an item into itself or its own descendant")
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

// Store manages filesystem-backed collections rooted under a common base directory.
type Store struct {
	base   string
	logger *slog.Logger

	mu    sync.Mutex
	colls map[string]*Collection
}

// New returns a Store rooting collections under base; a nil logger uses slog.Default().
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

// Open returns the named collection's handle; it does not create the collection on disk.
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

// Collection serializes its mutations, so concurrent RPCs for one workspace can't interleave writes.
type Collection struct {
	root   string
	name   string
	logger *slog.Logger

	mu sync.Mutex
}

func (c *Collection) Root() string { return c.root }

func (c *Collection) collectionFilePath() string { return filepath.Join(c.root, collectionFileName) }
func (c *Collection) treeRoot() string           { return filepath.Join(c.root, treeDir) }
func (c *Collection) scriptsRoot() string        { return filepath.Join(c.root, scriptsDir) }
func (c *Collection) servicesCachePath() string {
	return filepath.Join(c.root, stateDir, cacheSubdir, servicesCacheFileName)
}

func (c *Collection) sourcesCacheRoot() string {
	return filepath.Join(c.root, stateDir, cacheSubdir, sourcesCacheSubdir)
}

// sourceCachePath is slug + id hash: two ids that slugify alike (localhost:8080 vs
// localhost.8080) must not share a file.
func (c *Collection) sourceCachePath(id string) string {
	sum := sha256.Sum256([]byte(id))
	name := fmt.Sprintf("%s-%s%s", slugify(id), hex.EncodeToString(sum[:6]), sourceCacheFileExt)
	return filepath.Join(c.sourcesCacheRoot(), name)
}
func (c *Collection) historyRoot() string { return filepath.Join(c.root, stateDir, historyDir) }
