package store

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"unicode"

	grpcviewstorev1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/store/v1"
)

const (
	// workspaceFileName and CollectionFileName differ by five characters in a listing,
	// which is accepted (after go.work) — but only because both names live HERE, so a
	// product rename edits one place rather than every use site.
	//
	// CollectionFileName alone is exported: it is also what marks a directory as a
	// collection to a caller OUTSIDE the store, since the CLI walks up from the cwd looking
	// for one (service/cli/collection.go).
	workspaceFileName  = "grpcview.work.json"
	CollectionFileName = "grpcview.json"
	folderFileName     = "folder.json"
	requestFileName    = "request.json"
	scriptFileName     = "script.json"
	historyFileName    = "history.json"

	treeDir    = "tree"
	scriptsDir = "scripts"
	historyDir = "history"

	// descriptorIndexFileName is a collection's `source id -> blob digest` index; the blobs
	// it points at are shared, so it sits in the collection's state dir while they do not.
	descriptorIndexFileName = "descriptors.json"

	// collectionIndexFileName and blobsDir sit directly under the workspace STATE root, not
	// under collections/: the collection index is about the workspace and no collection owns
	// it, and a blob is deliberately shared by every collection whose sources resolve to the
	// same bytes.
	collectionIndexFileName = "collections.json"
	blobsDir                = "blobs"
	blobFileExt             = ".binpb"

	// collectionsStateSubdir holds one state directory per collection — see collectionStateDir
	// for why it is a flat, hashed layer rather than the collection's own path.
	collectionsStateSubdir = "collections"

	gitignoreFileName = ".gitignore"
	// gitExcludeRelPath is the repo-local ignore file git itself reads before any
	// .gitignore; discovery honors it for the same reason it honors .gitignore.
	gitExcludeRelPath = ".git/info/exclude"

	// nodeModulesDirName and bazelSymlinkPrefix are the two prune names discovery knows
	// by hand. Everything else it prunes it learns from a .gitignore — see Store.scan.
	nodeModulesDirName = "node_modules"
	bazelSymlinkPrefix = "bazel-"
)

type itemKind int

const (
	kindFolder itemKind = iota
	kindRequest
)

type childEntry struct {
	slug string
	name string
	kind itemKind

	// Exactly one is non-nil, matching kind; readChildren fills it so later reads need no re-decode.
	folder  *grpcviewstorev1.Folder
	request *grpcviewstorev1.Request
}

func (c childEntry) orderSlug() string { return c.slug }
func (c childEntry) orderName() string { return c.name }

type slugNamed interface {
	orderSlug() string
	orderName() string
}

// reservedSlugs are names a child item must not take, because a managed file already owns
// them. Local state no longer lives inside a collection directory (see Collection.state), so
// there is no local-state dirname to reserve here any more.
var reservedSlugs = map[string]bool{
	CollectionFileName: true,
	folderFileName:     true,
	requestFileName:    true,
}

func isReserved(slug string) bool {
	return reservedSlugs[strings.ToLower(slug)]
}

func slugify(name string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range name {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "item"
	}
	return slug
}

// uniqueSlug derives a slug that is neither reserved nor in used, which holds LOWERCASED
// slugs: the comparison must stay case-insensitive for macOS's case-insensitive filesystem.
func uniqueSlug(name string, used map[string]bool) string {
	base := slugify(name)
	candidate := base
	for i := 2; used[strings.ToLower(candidate)] || isReserved(candidate); i++ {
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
	return candidate
}

func slugSet(children []childEntry) map[string]bool {
	set := make(map[string]bool, len(children))
	for _, c := range children {
		set[strings.ToLower(c.slug)] = true
	}
	return set
}

func findByName(children []childEntry, name string) (childEntry, bool) {
	for _, c := range children {
		if c.name == name {
			return c, true
		}
	}
	return childEntry{}, false
}

// reconcileOrder orders present by listed, returning slugs absent on disk in dropped and
// appending unlisted items in display-name order (tie-broken by slug for determinism).
func reconcileOrder[T slugNamed](listed []string, present []T) (ordered []T, dropped []string) {
	bySlug := make(map[string]T, len(present))
	for _, c := range present {
		bySlug[c.orderSlug()] = c
	}

	seen := make(map[string]bool, len(listed))
	for _, slug := range listed {
		if seen[slug] {
			continue
		}
		c, ok := bySlug[slug]
		if !ok {
			dropped = append(dropped, slug)
			continue
		}
		seen[slug] = true
		ordered = append(ordered, c)
	}

	var rest []T
	for _, c := range present {
		if !seen[c.orderSlug()] {
			rest = append(rest, c)
		}
	}
	slices.SortFunc(rest, func(a, b T) int {
		if d := cmp.Compare(strings.ToLower(a.orderName()), strings.ToLower(b.orderName())); d != 0 {
			return d
		}
		return cmp.Compare(a.orderSlug(), b.orderSlug())
	})
	ordered = append(ordered, rest...)
	return ordered, dropped
}
