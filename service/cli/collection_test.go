package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/store"
)

func mustCollectionDir(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", dir, err)
	}
	manifest := filepath.Join(dir, store.CollectionFileName)
	if err := os.WriteFile(manifest, []byte(`{"schemaVersion":1}`), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", manifest, err)
	}
	return dir
}

func summaries(ids ...string) []*grpcviewv1.CollectionSummary {
	out := make([]*grpcviewv1.CollectionSummary, 0, len(ids))
	for _, id := range ids {
		out = append(out, &grpcviewv1.CollectionSummary{Id: id, Name: filepath.Base(id)})
	}
	return out
}

func TestResolveCollectionFromTheCwd(t *testing.T) {
	root := t.TempDir()
	collection := mustCollectionDir(t, filepath.Join(root, "services", "payments", "requests"))
	mustCollectionDir(t, filepath.Join(root, "tools", "loadgen", "requests"))

	inside := filepath.Join(collection, "tree", "Auth")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", inside, err)
	}
	t.Chdir(inside)

	fc := newFake()
	fc.listRoot = root
	fc.listing = summaries("services/payments/requests", "tools/loadgen/requests")

	out, errOut, code := runCLI(fc, "", "get")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stdout=%q stderr=%q)", code, out, errOut)
	}
	if len(fc.gotGet) != 1 || fc.gotGet[0].GetCollection() != "services/payments/requests" {
		t.Errorf("gotGet = %+v, want one Get for the collection holding the cwd", fc.gotGet)
	}
	if len(fc.gotList) != 1 {
		t.Errorf("ListCollections called %d time(s), want exactly 1 — the answer is memoized", len(fc.gotList))
	}
}

func TestResolveCollectionAtTheWorkspaceRoot(t *testing.T) {
	root := mustCollectionDir(t, t.TempDir())
	t.Chdir(root)

	fc := newFake()
	fc.listRoot = root
	fc.listing = summaries(".", "other/requests")

	out, errOut, code := runCLI(fc, "", "get")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stdout=%q stderr=%q)", code, out, errOut)
	}
	if len(fc.gotGet) != 1 || fc.gotGet[0].GetCollection() != "." {
		t.Errorf("gotGet = %+v, want one Get for %q — the root's own id", fc.gotGet, ".")
	}
}

func TestResolveCollectionFallsBackToTheOnlyOne(t *testing.T) {
	root := t.TempDir()
	mustCollectionDir(t, filepath.Join(root, "requests"))
	t.Chdir(t.TempDir())

	fc := newFake()
	fc.listRoot = root
	fc.listing = summaries("requests")

	out, errOut, code := runCLI(fc, "", "get")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stdout=%q stderr=%q)", code, out, errOut)
	}
	if len(fc.gotGet) != 1 || fc.gotGet[0].GetCollection() != "requests" {
		t.Errorf("gotGet = %+v, want one Get for the workspace's only collection", fc.gotGet)
	}
}

func TestResolveCollectionWithNoneIsExitTwo(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	fc := newFake()
	fc.listRoot = root
	fc.listing = nil

	out, errOut, code := runCLI(fc, "", "get")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (stdout=%q stderr=%q)", code, out, errOut)
	}
	if out != "" {
		t.Errorf("stdout = %q, want empty", out)
	}
	if !strings.Contains(errOut, "grpcview init") || !strings.Contains(errOut, root) {
		t.Errorf("stderr = %q, want it to name the workspace root and point at `grpcview init`", errOut)
	}
	if len(fc.gotGet) != 0 {
		t.Errorf("gotGet = %+v, want nothing read: the address was never decided", fc.gotGet)
	}
}

func TestResolveCollectionWithSeveralListsTheCandidates(t *testing.T) {
	root := t.TempDir()
	mustCollectionDir(t, filepath.Join(root, "a", "requests"))
	mustCollectionDir(t, filepath.Join(root, "b", "requests"))
	t.Chdir(root)

	fc := newFake()
	fc.listRoot = root
	fc.listing = summaries("a/requests", "b/requests")

	out, errOut, code := runCLI(fc, "", "ls")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (stdout=%q stderr=%q)", code, out, errOut)
	}
	if out != "" {
		t.Errorf("stdout = %q, want empty", out)
	}
	for _, want := range []string{"--collection", "\n  a/requests", "\n  b/requests"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr = %q, want it to contain %q", errOut, want)
		}
	}
}

func TestCollectionFlagWinsOverTheCwd(t *testing.T) {
	root := t.TempDir()
	t.Chdir(mustCollectionDir(t, filepath.Join(root, "requests")))

	fc := newFake()
	fc.listRoot = root
	fc.listing = summaries("requests")

	out, errOut, code := runCLI(fc, "", "get", "--collection", "other")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stdout=%q stderr=%q)", code, out, errOut)
	}
	if len(fc.gotGet) != 1 || fc.gotGet[0].GetCollection() != "other" {
		t.Errorf("gotGet = %+v, want the explicit --collection", fc.gotGet)
	}
	if len(fc.gotList) != 0 {
		t.Errorf("ListCollections called %d time(s), want none: an explicit flag needs no listing", len(fc.gotList))
	}
}

func TestCollectionsLsText(t *testing.T) {
	root := t.TempDir()
	t.Chdir(mustCollectionDir(t, filepath.Join(root, "svc", "requests")))

	fc := newFake()
	fc.listRoot = root
	fc.listing = []*grpcviewv1.CollectionSummary{
		{Id: "svc/requests", Name: "payments", SourceCount: 2},
		{Id: "tools/requests", Name: "loadgen", SourceCount: 1},
		{Id: "broken", Error: "grpcview.json:\n  unexpected end of JSON input"},
	}

	out, errOut, code := runCLI(fc, "", "collections", "ls")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stdout=%q stderr=%q)", code, out, errOut)
	}
	if errOut != "" {
		t.Errorf("stderr = %q, want empty", errOut)
	}

	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("stdout = %q, want one line per collection", out)
	}
	if !strings.HasPrefix(lines[0], "* svc/requests") {
		t.Errorf("line 0 = %q, want the cwd's collection marked with a leading *", lines[0])
	}
	if !strings.Contains(lines[0], "payments") || !strings.Contains(lines[0], "2 sources") {
		t.Errorf("line 0 = %q, want the display name and the source count", lines[0])
	}
	if !strings.HasPrefix(lines[1], "  tools/requests") || !strings.Contains(lines[1], "1 source") {
		t.Errorf("line 1 = %q, want an unmarked row with a singular source count", lines[1])
	}
	if !strings.Contains(lines[2], "error: grpcview.json: unexpected end of JSON input") {
		t.Errorf("line 2 = %q, want the errored row to carry its reason on one line", lines[2])
	}
	if strings.HasPrefix(lines[2], "*") {
		t.Errorf("line 2 = %q, want only one marked row", lines[2])
	}
}

func TestCollectionsLsSaysNothingAboutTrust(t *testing.T) {
	for _, shape := range [][]string{
		{"collections", "ls"},
		{"collections", "ls", "-o", "json"},
	} {
		t.Run(strings.Join(shape, " "), func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(mustCollectionDir(t, filepath.Join(root, "svc", "requests")))

			fc := newFake()
			fc.listRoot = root
			fc.listTrusted = false
			fc.listing = summaries("svc/requests")

			out, errOut, code := runCLI(fc, "", shape...)
			if code != 0 {
				t.Fatalf("exit code = %d, want 0 (stdout=%q stderr=%q)", code, out, errOut)
			}
			if errOut != "" {
				t.Errorf("stderr = %q, want empty", errOut)
			}
			if lines := strings.Count(out, "\n"); lines != 1 {
				t.Errorf("stdout = %q, want exactly one line: the row alone, with no trust note", out)
			}
			if strings.Contains(out, "grpcview trust") || strings.Contains(out, "not trusted") {
				t.Errorf("stdout = %q, want no trust nag: this listing cannot know a bazel source exists", out)
			}
		})
	}
}

func TestCollectionsLsJSONAndRefresh(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	fc := newFake()
	fc.listRoot = root
	fc.listing = summaries("requests")

	out, errOut, code := runCLI(fc, "", "collections", "ls", "-o", "json", "--refresh")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stdout=%q stderr=%q)", code, out, errOut)
	}
	if lines := strings.Count(out, "\n"); lines != 1 {
		t.Errorf("stdout = %q, want exactly one line of JSON", out)
	}
	if !strings.Contains(out, `"root":`) || !strings.Contains(out, `"requests"`) {
		t.Errorf("stdout = %q, want the whole ListCollectionsResponse as protojson", out)
	}
	if len(fc.gotList) != 1 || !fc.gotList[0].GetRefresh() {
		t.Errorf("gotList = %+v, want one listing with refresh set", fc.gotList)
	}
}

func TestInitNeedsNoCollectionToResolve(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	fc := newFake()
	fc.listRoot = root
	fc.listing = nil

	out, errOut, code := runCLI(fc, "", "init", "requests")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stdout=%q stderr=%q)", code, out, errOut)
	}
	if len(fc.writes.createCollection) != 1 || fc.writes.createCollection[0].GetCollection() != "requests" {
		t.Errorf("createCollection = %+v, want one create at %q", fc.writes.createCollection, "requests")
	}
	if len(fc.gotList) != 0 {
		t.Errorf("ListCollections called %d time(s), want none: init decides its own address", len(fc.gotList))
	}
}

func TestNearestCollectionStopsAtTheRoot(t *testing.T) {
	outer := t.TempDir()
	root := filepath.Join(outer, "repo")
	mustCollectionDir(t, outer)
	cwd := filepath.Join(root, "deep", "dir")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", cwd, err)
	}

	if id, ok := nearestCollection(root, cwd); ok {
		t.Errorf("nearestCollection = %q, true; a collection ABOVE the workspace root is not addressable", id)
	}
	if id, ok := nearestCollection(root, outer); ok {
		t.Errorf("nearestCollection = %q, true; a cwd outside the root has nothing to walk", id)
	}
}
