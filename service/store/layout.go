package store

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"unicode"

	grpcviewstorev1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/store/v1"
)

const (
	WorkspaceFileName  = "grpcview.work.json"
	CollectionFileName = "grpcview.json"
	folderFileName     = "folder.json"
	requestFileName    = "request.json"
	historyFileName    = "history.json"

	treeDir    = "tree"
	scriptsDir = "scripts"
	historyDir = "history"

	descriptorsDir       = "descriptors"
	descriptorSidecarExt = ".json"

	descriptorIndexFileName = "descriptors.json"

	blobsDir    = "blobs"
	blobFileExt = ".binpb"

	collectionsStateSubdir = "collections"

	gitignoreFileName = ".gitignore"
	gitExcludeRelPath = ".git/info/exclude"

	// NodeModulesDirName and BazelSymlinkPrefix are exported for service/workspace's
	// ListWorkspaceModules walk, which skips the same directories this scan does.
	NodeModulesDirName = "node_modules"
	BazelSymlinkPrefix = "bazel-"
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

	folder  *grpcviewstorev1.Folder
	request *grpcviewstorev1.Request
}

func (c childEntry) orderSlug() string { return c.slug }
func (c childEntry) orderName() string { return c.name }

type slugNamed interface {
	orderSlug() string
	orderName() string
}

var reservedSlugs = map[string]bool{
	CollectionFileName: true,
	folderFileName:     true,
	requestFileName:    true,
}

func isReserved(slug string) bool {
	return reservedSlugs[strings.ToLower(slug)]
}

// Slug plus a hash of the whole key, so keys that slugify alike ("localhost:8080" and
// "localhost.8080") can never collide on one name.
func hashedName(key string) string {
	sum := sha256.Sum256([]byte(key))
	return slugify(key) + "-" + hex.EncodeToString(sum[:6])
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

// used holds LOWERCASED slugs: the comparison must stay case-insensitive for macOS.
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
