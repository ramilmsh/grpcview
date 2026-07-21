package store

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"unicode"

	grpcviewstorev1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/store/v1"
)

// Managed file and directory names within a collection.
const (
	collectionFileName = "grpcview.json" // root manifest + root folder ordering + sources
	folderFileName     = "folder.json"   // per-folder config (meta + child ordering)
	requestFileName    = "request.json"  // per-request config (meta + service/method/body/metadata)
	historyFileName    = "history.json"  // per-request run history (gitignored; under stateDir)
	gitignoreFileName  = ".gitignore"

	treeDir               = "tree"      // committed request tree
	stateDir              = ".grpcview" // gitignored local state
	cacheSubdir           = "cache"     // resolved-schema cache under stateDir
	historyDir            = "history"   // run history under stateDir, keyed by request slug path
	servicesCacheFileName = "services.json"
)

// itemKind distinguishes the two kinds of tree items.
type itemKind int

const (
	kindFolder itemKind = iota
	kindRequest
)

// childEntry is a classified on-disk item directory: its stable slug (dir name),
// its display name (from the config's meta), and its kind.
type childEntry struct {
	slug string
	name string
	kind itemKind

	// The decoded on-disk config, populated by readChildren so a later read of
	// the same item (e.g. Load's readItem) reuses it instead of re-opening and
	// re-decoding the file. Exactly one is non-nil, matching kind.
	folder  *grpcviewstorev1.Folder
	request *grpcviewstorev1.Request
}

// reservedSlugs cannot be used as item directory names: they would collide with
// managed files, the local-state dir, or (case-insensitively) Windows device
// names that are illegal as directory names on that platform.
var reservedSlugs = func() map[string]bool {
	m := map[string]bool{
		collectionFileName: true,
		folderFileName:     true,
		requestFileName:    true,
		gitignoreFileName:  true,
		stateDir:           true,
		"con":              true,
		"prn":              true,
		"aux":              true,
		"nul":              true,
	}
	for i := 1; i <= 9; i++ {
		m[fmt.Sprintf("com%d", i)] = true
		m[fmt.Sprintf("lpt%d", i)] = true
	}
	return m
}()

func isReserved(slug string) bool {
	return reservedSlugs[strings.ToLower(slug)]
}

// slugify turns a display name into a lowercase, filesystem-safe slug. Runs of
// non-alphanumeric characters collapse to a single hyphen; letters and digits
// (including non-ASCII) are kept, lowercased.
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

// uniqueSlug derives a slug from name that is neither reserved nor already used
// by a sibling. Comparison is case-insensitive so the result is safe on
// case-insensitive filesystems (macOS/Windows). used holds lowercased slugs.
func uniqueSlug(name string, used map[string]bool) string {
	base := slugify(name)
	candidate := base
	for i := 2; used[strings.ToLower(candidate)] || isReserved(candidate); i++ {
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
	return candidate
}

// slugSet returns the lowercased slugs of the given children.
func slugSet(children []childEntry) map[string]bool {
	set := make(map[string]bool, len(children))
	for _, c := range children {
		set[strings.ToLower(c.slug)] = true
	}
	return set
}

// findByName returns the child with the given display name (first match).
func findByName(children []childEntry, name string) (childEntry, bool) {
	for _, c := range children {
		if c.name == name {
			return c, true
		}
	}
	return childEntry{}, false
}

// reconcileOrder orders children by the parent's recorded slug list, then
// self-heals drift: slugs listed but absent on disk are dropped (returned in
// dropped for the caller to log), and on-disk items missing from the list are
// appended in display-name order (tie-broken by slug for determinism).
func reconcileOrder(listed []string, present []childEntry) (ordered []childEntry, dropped []string) {
	bySlug := make(map[string]childEntry, len(present))
	for _, c := range present {
		bySlug[c.slug] = c
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

	var rest []childEntry
	for _, c := range present {
		if !seen[c.slug] {
			rest = append(rest, c)
		}
	}
	slices.SortFunc(rest, func(a, b childEntry) int {
		if d := cmp.Compare(strings.ToLower(a.name), strings.ToLower(b.name)); d != 0 {
			return d
		}
		return cmp.Compare(a.slug, b.slug)
	})
	ordered = append(ordered, rest...)
	return ordered, dropped
}
