package store

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"

	grpcviewstorev1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/store/v1"
)

var ErrWorkspaceTooLarge = errors.New(`too large to scan — pin "collections" in grpcview.work.json`)

var maxScanDirs = 20000

type CollectionInfo struct {
	ID          string
	Name        string
	SourceCount int
	Err         string
}

func (s *Store) List(ctx context.Context, refresh bool) ([]CollectionInfo, error) {
	mtime, err := s.rootMtime()
	if err != nil {
		return nil, err
	}
	if !refresh {
		if cached, ok := s.readIndex(mtime); ok {
			return cached, nil
		}
	}

	ids, err := s.collectionIDs()
	if err != nil {
		return nil, err
	}
	infos := make([]CollectionInfo, 0, len(ids))
	for _, id := range ids {
		infos = append(infos, s.summarize(ctx, id))
	}
	slices.SortFunc(infos, func(a, b CollectionInfo) int { return strings.Compare(a.ID, b.ID) })

	s.writeIndex(mtime, infos)
	return infos, nil
}

// Creating a collection deeper than the root does not change the ROOT's mtime the index is keyed by,
// so the one writer that can create one has to say so explicitly.
func (s *Store) InvalidateList() {
	if err := os.Remove(s.indexPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.logger.Warn("drop collection index", "error", err)
	}
}

func (s *Store) collectionIDs() ([]string, error) {
	declared, err := s.declaredIDs()
	if err != nil {
		return nil, err
	}
	if declared != nil {
		return declared, nil
	}
	return s.scan()
}

func (s *Store) summarize(ctx context.Context, id string) CollectionInfo {
	coll, err := s.Open(ctx, id)
	if err != nil {
		return CollectionInfo{ID: id, Err: err.Error()}
	}
	name, count, err := coll.Summary(ctx)
	if err != nil {
		return CollectionInfo{ID: id, Name: coll.defaultName(), Err: err.Error()}
	}
	return CollectionInfo{ID: id, Name: name, SourceCount: count}
}

func (s *Store) declaredIDs() ([]string, error) {
	ws, err := s.readWorkspaceManifest()
	if err != nil {
		return nil, err
	}
	if len(ws.GetCollections()) == 0 {
		return nil, nil
	}

	ids := make([]string, 0, len(ws.GetCollections()))
	seen := make(map[string]bool, len(ws.GetCollections()))
	add := func(id string) {
		if key := strings.ToLower(id); !seen[key] {
			seen[key] = true
			ids = append(ids, id)
		}
	}
	for _, entry := range ws.GetCollections() {
		cleaned, err := cleanCollectionID(entry)
		if err != nil {
			add(entry)
			continue
		}
		if !isGlob(cleaned) {
			add(cleaned)
			continue
		}
		matches, err := filepath.Glob(filepath.Join(s.root, cleaned))
		if err != nil {
			return nil, fmt.Errorf("collections pattern %q: %w", entry, err)
		}
		for _, match := range matches {
			if !fileExists(filepath.Join(match, CollectionFileName)) {
				continue
			}
			id, err := s.relativeID(match)
			if err != nil {
				continue
			}
			add(id)
		}
	}
	return ids, nil
}

func isGlob(entry string) bool { return strings.ContainsAny(entry, `*?[`) }

// Walks the workspace for grpcview.json. The rules, in the order they matter:
//
//   - Prune dot-directories, node_modules, bazel-* and anything gitignored, so a build step that
//     copied a collection into dist/ does not produce a second, real-looking one.
//   - Never follow a symlink. fs.DirEntry.IsDir is false for one, so descending is not reachable —
//     that is the reason to walk DirEntries rather than stat results.
//   - Prune AT a hit: a directory holding grpcview.json is a collection and a LEAF. That invariant is
//     load-bearing well beyond the scan — because no collection id can be a path prefix of another,
//     "<collection id>/<slug path>" is an unambiguous key with a plain "/".
func (s *Store) scan() ([]string, error) {
	// The ROOT's own symlinks, resolved once: WalkDir Lstats the path it is handed, so a symlinked
	// workspace root would otherwise present as a non-directory and yield an empty workspace with no error
	// at all. The entries BELOW it are still never followed.
	base, err := filepath.EvalSymlinks(s.root)
	if err != nil {
		return nil, err
	}

	scopes := map[string]dirIgnore{}
	parentScope := func(dir string) dirIgnore {
		if dir == base {
			return newDirIgnore(nil, readIgnoreFile(filepath.Join(base, gitExcludeRelPath), nil))
		}
		return scopes[filepath.Dir(dir)]
	}

	var ids []string
	visited := 0
	err = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == base {
				return err
			}
			s.logger.Debug("skip directory during collection scan", "path", path, "error", err)
			return nil
		}
		if !d.IsDir() {
			return nil
		}

		segments := relSegments(base, path)
		if len(segments) > 0 {
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == nodeModulesDirName || strings.HasPrefix(name, bazelSymlinkPrefix) {
				return fs.SkipDir
			}
			if scope := parentScope(path); scope.matcher != nil && scope.matcher.Match(segments, true) {
				return fs.SkipDir
			}
		}

		visited++
		if visited > maxScanDirs {
			return ErrWorkspaceTooLarge
		}

		if fileExists(filepath.Join(path, CollectionFileName)) {
			ids = append(ids, idFromSegments(segments))
			return fs.SkipDir
		}

		scopes[path] = newDirIgnore(parentScope(path).patterns, readIgnoreFile(filepath.Join(path, gitignoreFileName), segments))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ids, nil
}

type dirIgnore struct {
	patterns []gitignore.Pattern
	matcher  gitignore.Matcher
}

// Copies rather than appending in place: two siblings extending one parent slice would write over each
// other's patterns through the shared backing array.
func newDirIgnore(parent, own []gitignore.Pattern) dirIgnore {
	if len(own) == 0 {
		return dirIgnore{patterns: parent, matcher: matcherOf(parent)}
	}
	merged := make([]gitignore.Pattern, 0, len(parent)+len(own))
	merged = append(merged, parent...)
	merged = append(merged, own...)
	return dirIgnore{patterns: merged, matcher: matcherOf(merged)}
}

func matcherOf(ps []gitignore.Pattern) gitignore.Matcher {
	if len(ps) == 0 {
		return nil
	}
	return gitignore.NewMatcher(ps)
}

// go-git's gitignore.ReadPatterns is deliberately NOT used: it recursively ReadDirs the whole tree
// itself, which is the unbounded walk maxScanDirs exists to prevent. A file that will not open or read
// is treated as absent.
func readIgnoreFile(path string, domain []string) []gitignore.Pattern {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var ps []gitignore.Pattern
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		ps = append(ps, gitignore.ParsePattern(line, domain))
	}
	return ps
}

func relSegments(base, path string) []string {
	if path == base {
		return nil
	}
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return nil
	}
	return strings.Split(filepath.ToSlash(rel), "/")
}

func idFromSegments(segments []string) string {
	if len(segments) == 0 {
		return "."
	}
	return strings.Join(segments, "/")
}

func (s *Store) relativeID(path string) (string, error) {
	rel, err := filepath.Rel(s.root, path)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

func (s *Store) indexPath() string { return filepath.Join(s.stateRoot, collectionIndexFileName) }

func (s *Store) rootMtime() (int64, error) {
	info, err := os.Stat(s.root)
	if err != nil {
		return 0, err
	}
	return info.ModTime().UnixNano(), nil
}

func (s *Store) readIndex(mtime int64) ([]CollectionInfo, bool) {
	idx := &grpcviewstorev1.CollectionIndex{}
	if err := readMessage(s.indexPath(), idx); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			s.logger.Debug("read collection index", "error", err)
		}
		return nil, false
	}
	if idx.GetSchemaVersion() != schemaVersion || idx.GetRootMtimeUnixNano() != mtime {
		return nil, false
	}
	infos := make([]CollectionInfo, 0, len(idx.GetEntries()))
	for _, e := range idx.GetEntries() {
		infos = append(infos, CollectionInfo{
			ID:          e.GetId(),
			Name:        e.GetName(),
			SourceCount: int(e.GetSourceCount()),
			Err:         e.GetError(),
		})
	}
	return infos, true
}

func (s *Store) writeIndex(mtime int64, infos []CollectionInfo) {
	idx := &grpcviewstorev1.CollectionIndex{
		SchemaVersion:     schemaVersion,
		RootMtimeUnixNano: mtime,
		Entries:           make([]*grpcviewstorev1.CollectionIndexEntry, 0, len(infos)),
	}
	for _, info := range infos {
		idx.Entries = append(idx.Entries, &grpcviewstorev1.CollectionIndexEntry{
			Id:          info.ID,
			Name:        info.Name,
			SourceCount: int32(info.SourceCount),
			Error:       info.Err,
		})
	}
	if err := os.MkdirAll(s.stateRoot, 0o755); err != nil {
		s.logger.Warn("cache collection index", "error", err)
		return
	}
	if err := writeMessage(s.indexPath(), idx); err != nil {
		s.logger.Warn("cache collection index", "error", err)
	}
}
