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

// ErrWorkspaceTooLarge stops discovery from becoming a hang. A workspace root is whatever
// directory the user pointed at, so $HOME is legal (if unhelpful); it must fail loudly and
// cheaply rather than walk a home directory to the leaves.
var ErrWorkspaceTooLarge = errors.New(`too large to scan — pin "collections" in grpcview.work.json`)

// maxScanDirs bounds the directories a scan descends into. A var, not a const, so a test can
// lower it instead of materializing twenty thousand directories.
var maxScanDirs = 20000

// CollectionInfo is a cheap summary of one collection: its address, its display name and
// how many descriptor sources it declares. No tree is ever read to produce it.
type CollectionInfo struct {
	ID          string // workspace-relative slash path; "." is the workspace root itself
	Name        string
	SourceCount int
	Err         string // non-empty when this collection's own manifest could not be read
}

// List returns every collection in the workspace, sorted by ID. refresh bypasses the
// cached index and rescans.
//
// A collection that cannot be summarized becomes an entry with Err set rather than a failed
// List: one unparseable grpcview.json a colleague pushed must not hide every other
// collection in the repo.
func (s *Store) List(ctx context.Context, refresh bool) ([]CollectionInfo, error) {
	// The index is keyed by the root directory's own mtime, so stat it before anything
	// else; a root that isn't there is a real error, not an empty workspace.
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

// InvalidateList drops the cached index. Creating a collection deeper than the root does not
// change the ROOT's mtime, so the one writer that can create one has to say so explicitly —
// the mtime key cannot notice it.
func (s *Store) InvalidateList() {
	if err := os.Remove(s.indexPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.logger.Warn("drop collection index", "error", err)
	}
}

// collectionIDs answers "which collections are in this workspace" — declared if declared,
// bounded scan otherwise.
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

// summarize reads one collection's own manifest and nothing else. It goes through Open so a
// listing shares the store's handle for a directory rather than minting a second one, which
// is what keeps Collection.mu the single write serializer.
func (s *Store) summarize(ctx context.Context, id string) CollectionInfo {
	coll, err := s.Open(ctx, id)
	if err != nil {
		return CollectionInfo{ID: id, Err: err.Error()}
	}
	name, count, err := coll.Summary(ctx)
	if err != nil {
		// Still name it: a row the user can see is the whole point of carrying the error.
		return CollectionInfo{ID: id, Name: coll.defaultName(), Err: err.Error()}
	}
	return CollectionInfo{ID: id, Name: name, SourceCount: count}
}

// declaredIDs expands grpcview.work.json's collections list, or returns nil when the file is
// absent or pins nothing — nil meaning "scan", distinct from an empty non-nil slice meaning
// "the workspace declares no collections".
func (s *Store) declaredIDs() ([]string, error) {
	ws := &grpcviewstorev1.Workspace{}
	err := readMessage(filepath.Join(s.root, workspaceFileName), ws)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
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
			// Keep it, so the row carries the error. Silently dropping an entry that names
			// something outside the root would hide a manifest a reviewer should see, and
			// Open rejects the id again if anything tries to use it.
			add(entry)
			continue
		}
		if !isGlob(cleaned) {
			// A literal is a claim that a collection is there; report a broken claim so a
			// typo is visible. summarize turns the missing manifest into the Err text.
			add(cleaned)
			continue
		}
		// A glob is a filter, not a claim: matching nothing is the correct answer for
		// "services/*/requests" in a repo that has no services yet.
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

// isGlob reports whether an entry is a pattern rather than a path. filepath.Match's
// metacharacters, so the two readings of an entry can never disagree.
func isGlob(entry string) bool { return strings.ContainsAny(entry, `*?[`) }

// scan walks the workspace for grpcview.json. The rules it enforces, in the order they
// matter:
//
//   - Prune dot-directories (so .git), node_modules, bazel-* (convenience symlinks into an
//     unbounded output base) and anything gitignored. A build step that copied a collection
//     into dist/ or target/ must not produce a second, real-looking collection.
//   - Never follow a symlink. fs.DirEntry.IsDir is false for one, so descending is not even
//     reachable from here — that is the reason to walk DirEntries rather than stat results.
//   - Prune AT a hit: a directory holding grpcview.json is a collection and a LEAF. This
//     invariant is load-bearing well beyond the scan — because no collection id can be a path
//     prefix of another, "<collection id>/<slug path>" is an unambiguous key with a plain "/".
//     It also means a collection at the root (id ".") is the whole workspace.
func (s *Store) scan() ([]string, error) {
	// Resolve the ROOT's own symlinks once. filepath.WalkDir Lstats the path it is handed, so
	// a symlinked workspace root ("--workspace ~/link-to-repo") would otherwise present as a
	// non-directory and yield an empty workspace with no error at all. This is not "following
	// symlinks": the root is where the user pointed, and it is the entries BELOW it that must
	// not be followed.
	base, err := filepath.EvalSymlinks(s.root)
	if err != nil {
		return nil, err
	}

	// One entry per directory we descended into, holding the ignore patterns in scope for
	// its children. Bounded by maxScanDirs, which is what makes holding it all affordable.
	scopes := map[string]dirIgnore{}
	parentScope := func(dir string) dirIgnore {
		if dir == base {
			// git reads .git/info/exclude before any .gitignore, and a repo can hide a
			// directory there instead of in a committed file.
			return newDirIgnore(nil, readIgnoreFile(filepath.Join(base, gitExcludeRelPath), nil))
		}
		return scopes[filepath.Dir(dir)]
	}

	var ids []string
	visited := 0
	err = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable directory hides its own contents and nothing else; a listing
			// that fails outright because of one would be worse than an incomplete one.
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
		if len(segments) > 0 { // never prune the root itself, whatever it is named
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

// dirIgnore is one directory's accumulated gitignore patterns plus the matcher built from
// them, so a matcher is built once per directory rather than once per child.
type dirIgnore struct {
	patterns []gitignore.Pattern
	matcher  gitignore.Matcher // nil when nothing in scope ignores anything
}

// newDirIgnore extends a parent scope with a directory's own patterns. It copies rather than
// appending in place: two siblings extending one parent slice would otherwise write over each
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

// readIgnoreFile parses one ignore file, with domain the path segments of the directory that
// owns it (which is how a pattern knows what it is relative to).
//
// go-git's own gitignore.ReadPatterns is deliberately NOT used: it collects nested
// .gitignore files by recursively ReadDir-ing the entire tree itself, pruning only .git and
// already-ignored directories. That is precisely the unbounded walk maxScanDirs exists to
// prevent — it would descend node_modules and the bazel-* symlinks before our own walk got
// a say. Accumulating patterns per directory as the scan enters it costs one Open per
// directory that has a .gitignore and nothing for the rest.
//
// A file that will not open or read is treated as absent: an ignore file is a hint about
// what to skip, and failing a whole listing over one is the wrong trade.
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

// relSegments splits a path below base into the segments the gitignore matcher and the
// collection id are both built from; nil means the path IS base.
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

// idFromSegments is the collection id for a directory: "." for the root, since that is the
// id Open uses for the workspace root itself.
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

// readIndex returns the cached listing when it was taken at this root mtime. Everything that
// can change the answer — a collection added or removed at any depth, an edit to
// grpcview.work.json — is a write to a directory, and only writes directly to the ROOT change
// the root's mtime, which is why Store.InvalidateList also exists.
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

// writeIndex caches a listing. Every failure here is logged and swallowed: this is disposable
// local state, so a workspace whose state directory is read-only must still list.
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
