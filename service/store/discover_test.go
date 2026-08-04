package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	grpcviewstorev1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/store/v1"
)

// writeCollectionAt materializes a collection manifest at a workspace-relative path, with one
// reflection source per address so SourceCount has something to count.
func writeCollectionAt(t *testing.T, root, rel, name string, addresses ...string) {
	t.Helper()
	dir := filepath.Join(root, rel)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	col := &grpcviewstorev1.Collection{SchemaVersion: schemaVersion, Name: name}
	for _, addr := range addresses {
		col.Sources = append(col.Sources, &grpcviewstorev1.DescriptorSource{
			Source: &grpcviewstorev1.DescriptorSource_Reflection{
				Reflection: &grpcviewstorev1.Reflection{Address: addr},
			},
		})
	}
	if err := writeMessage(filepath.Join(dir, CollectionFileName), col); err != nil {
		t.Fatalf("write manifest %s: %v", rel, err)
	}
}

func writeWorkspaceManifest(t *testing.T, root string, collections ...string) {
	t.Helper()
	ws := &grpcviewstorev1.Workspace{SchemaVersion: schemaVersion, Name: "acme", Collections: collections}
	if err := writeMessage(filepath.Join(root, workspaceFileName), ws); err != nil {
		t.Fatalf("write workspace manifest: %v", err)
	}
}

func mustList(t *testing.T, s *Store, refresh bool) []CollectionInfo {
	t.Helper()
	infos, err := s.List(context.Background(), refresh)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return infos
}

func listIDs(infos []CollectionInfo) []string {
	ids := make([]string, len(infos))
	for i, info := range infos {
		ids[i] = info.ID
	}
	return ids
}

func assertIDs(t *testing.T, infos []CollectionInfo, want ...string) {
	t.Helper()
	got := listIDs(infos)
	if len(got) != len(want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ids = %v, want %v", got, want)
		}
	}
}

func TestListScansAtEveryDepth(t *testing.T) {
	s := newTestStore(t)
	writeCollectionAt(t, s.Root(), "services/payments/requests", "Payments", "localhost:1", "localhost:2")
	writeCollectionAt(t, s.Root(), "tools/loadgen", "")

	infos := mustList(t, s, false)
	assertIDs(t, infos, "services/payments/requests", "tools/loadgen")

	if infos[0].Name != "Payments" || infos[0].SourceCount != 2 {
		t.Fatalf("payments = %+v, want name Payments with 2 sources", infos[0])
	}
	// An unnamed manifest displays as its own directory's base name, never as the id.
	if infos[1].Name != "loadgen" || infos[1].SourceCount != 0 {
		t.Fatalf("loadgen = %+v, want name loadgen with 0 sources", infos[1])
	}
	for _, info := range infos {
		if info.Err != "" {
			t.Fatalf("%s carries an error: %s", info.ID, info.Err)
		}
	}
}

func TestListPrunesAtACollection(t *testing.T) {
	s := newTestStore(t)
	writeCollectionAt(t, s.Root(), "requests", "Outer")
	// Nested collections are not a supported shape, and the non-nesting invariant is what
	// makes "<collection id>/<slug path>" an unambiguous key.
	writeCollectionAt(t, s.Root(), "requests/tree/nested", "Inner")

	assertIDs(t, mustList(t, s, false), "requests")
}

func TestListPrunesUninterestingDirectories(t *testing.T) {
	s := newTestStore(t)
	if err := os.WriteFile(filepath.Join(s.Root(), gitignoreFileName), []byte("# build output\ndist/\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	writeCollectionAt(t, s.Root(), "requests", "Real")
	for _, rel := range []string{
		".git/modules/x/requests",
		".hidden/requests",
		"node_modules/pkg/requests",
		"bazel-out/darwin/requests",
		"dist/requests",
	} {
		writeCollectionAt(t, s.Root(), rel, "Bogus")
	}

	assertIDs(t, mustList(t, s, false), "requests")
}

func TestListHonorsNestedGitignore(t *testing.T) {
	s := newTestStore(t)
	if err := os.MkdirAll(filepath.Join(s.Root(), "app"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(s.Root(), "app", gitignoreFileName), []byte("target/\n"), 0o644); err != nil {
		t.Fatalf("write nested .gitignore: %v", err)
	}
	writeCollectionAt(t, s.Root(), "app/requests", "Real")
	writeCollectionAt(t, s.Root(), "app/target/requests", "Copied")
	// The same name outside the .gitignore's own directory is NOT ignored: a pattern's
	// domain is the directory that declared it.
	writeCollectionAt(t, s.Root(), "target/requests", "Elsewhere")

	assertIDs(t, mustList(t, s, false), "app/requests", "target/requests")
}

func TestListCollectionAtWorkspaceRoot(t *testing.T) {
	s := newTestStore(t)
	writeCollectionAt(t, s.Root(), ".", "Root Collection")
	writeCollectionAt(t, s.Root(), "tree/nested", "Nested")

	infos := mustList(t, s, false)
	assertIDs(t, infos, ".")
	if infos[0].Name != "Root Collection" {
		t.Fatalf("name = %q, want %q", infos[0].Name, "Root Collection")
	}
}

func TestListDeclaredWinsOverScanning(t *testing.T) {
	s := newTestStore(t)
	writeCollectionAt(t, s.Root(), "services/payments/requests", "Payments")
	writeCollectionAt(t, s.Root(), "services/ledger/requests", "Ledger")
	writeCollectionAt(t, s.Root(), "other/requests", "Undeclared")
	writeWorkspaceManifest(t, s.Root(),
		"services/*/requests",    // a glob: matches what is there
		"tools/*/requests",       // a glob matching nothing is not an error
		"tools/loadgen/requests", // a literal typo, which must be visible
	)

	infos := mustList(t, s, false)
	assertIDs(t, infos, "services/ledger/requests", "services/payments/requests", "tools/loadgen/requests")
	for _, info := range infos[:2] {
		if info.Err != "" {
			t.Fatalf("%s carries an error: %s", info.ID, info.Err)
		}
	}
	if infos[2].Err == "" {
		t.Fatal("a declared literal with no grpcview.json reported no error")
	}
}

func TestListDeclaredRejectsEscapingEntry(t *testing.T) {
	s := newTestStore(t)
	writeWorkspaceManifest(t, s.Root(), "../outside")

	infos := mustList(t, s, false)
	assertIDs(t, infos, "../outside")
	if infos[0].Err == "" {
		t.Fatal("an entry naming a path outside the root reported no error")
	}
}

func TestListServesTheCachedIndexUntilRefresh(t *testing.T) {
	s := newTestStore(t)
	writeCollectionAt(t, s.Root(), "requests", "Real")

	assertIDs(t, mustList(t, s, false), "requests")

	// Removing a manifest below the root leaves the ROOT's own mtime untouched, so a
	// second List can only still see the collection if it came from the index.
	if err := os.Remove(filepath.Join(s.Root(), "requests", CollectionFileName)); err != nil {
		t.Fatalf("remove manifest: %v", err)
	}
	assertIDs(t, mustList(t, s, false), "requests")

	assertIDs(t, mustList(t, s, true))
	// The refreshed answer replaces the index rather than leaving the stale one behind.
	assertIDs(t, mustList(t, s, false))
}

func TestInvalidateListDropsTheIndex(t *testing.T) {
	s := newTestStore(t)
	writeCollectionAt(t, s.Root(), "requests", "Real")
	assertIDs(t, mustList(t, s, false), "requests")

	if err := os.Remove(filepath.Join(s.Root(), "requests", CollectionFileName)); err != nil {
		t.Fatalf("remove manifest: %v", err)
	}
	s.InvalidateList()
	assertIDs(t, mustList(t, s, false))

	// Dropping an index that is not there is not an error — nothing to undo.
	s.InvalidateList()
}

func TestListRejectsAWorkspaceTooLargeToScan(t *testing.T) {
	s := newTestStore(t)
	if err := os.MkdirAll(filepath.Join(s.Root(), "a", "b", "c"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	restore := maxScanDirs
	t.Cleanup(func() { maxScanDirs = restore })
	maxScanDirs = 2

	if _, err := s.List(context.Background(), false); !errors.Is(err, ErrWorkspaceTooLarge) {
		t.Fatalf("List error = %v, want ErrWorkspaceTooLarge", err)
	}
}
