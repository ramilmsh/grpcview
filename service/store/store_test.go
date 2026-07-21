package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/proto"

	grpcviewstorev1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/store/v1"
	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

func newTestCollection(t *testing.T) (*Collection, context.Context) {
	t.Helper()
	base := t.TempDir()
	s := New(base, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	coll, err := s.Open(ctx, "test")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := coll.EnsureCreated(ctx); err != nil {
		t.Fatalf("EnsureCreated: %v", err)
	}
	return coll, ctx
}

// childByName returns the child Item with the given display name.
func childByName(items []*grpcviewv1.Item, name string) *grpcviewv1.Item {
	for _, it := range items {
		if it.GetName() == name {
			return it
		}
	}
	return nil
}

// rootItems loads the collection and returns the root folder's ordered children.
func rootItems(t *testing.T, coll *Collection, ctx context.Context) []*grpcviewv1.Item {
	t.Helper()
	ws, err := coll.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return ws.GetItem().GetFolder().GetItems()
}

func names(items []*grpcviewv1.Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.GetName()
	}
	return out
}

// mustRead decodes a managed protojson file into m, failing the test on error.
func mustRead(t *testing.T, path string, m proto.Message) {
	t.Helper()
	if err := readMessage(path, m); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
}

func TestCreateAndLoadRoundTrip(t *testing.T) {
	coll, ctx := newTestCollection(t)

	if err := coll.CreateFolder(ctx, nil, "Users"); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	if err := coll.CreateRequest(ctx, []string{"Users"}, "Get User", "acme.v1.UserService", "GetUser"); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	body := `{"id":"42"}`
	if err := coll.UpdateRequest(ctx, []string{"Users"}, "Get User", RequestPatch{DraftBody: &body}); err != nil {
		t.Fatalf("UpdateRequest: %v", err)
	}

	// In-memory tree.
	root := rootItems(t, coll, ctx)
	users := childByName(root, "Users")
	if users == nil || users.GetFolder() == nil {
		t.Fatalf("expected Users folder, got %v", names(root))
	}
	req := childByName(users.GetFolder().GetItems(), "Get User")
	if req == nil || req.GetRequest() == nil {
		t.Fatalf("expected Get User request, got %v", names(users.GetFolder().GetItems()))
	}
	if got := req.GetRequest(); got.GetService() != "acme.v1.UserService" || got.GetMethod() != "GetUser" {
		t.Errorf("service/method = %q/%q", got.GetService(), got.GetMethod())
	}
	if got := req.GetRequest().GetDraftBody(); got != body {
		t.Errorf("draft body = %q, want %q", got, body)
	}

	// On-disk shape: slug dirs, meta.name, ordered items[].
	tree := filepath.Join(coll.Root(), treeDir)
	rf := &grpcviewstorev1.Request{}
	mustRead(t, filepath.Join(tree, "users", "get-user", requestFileName), rf)
	if rf.GetMeta().GetName() != "Get User" {
		t.Errorf("request.json meta.name = %q, want %q", rf.GetMeta().GetName(), "Get User")
	}
	if rf.GetDraftBody() != body {
		t.Errorf("request.json draftBody = %q, want %q", rf.GetDraftBody(), body)
	}
	col := &grpcviewstorev1.Collection{}
	mustRead(t, coll.collectionFilePath(), col)
	if len(col.GetItems()) != 1 || col.GetItems()[0] != "users" {
		t.Errorf("root items = %v, want [users]", col.GetItems())
	}
	ff := &grpcviewstorev1.Folder{}
	mustRead(t, filepath.Join(tree, "users", folderFileName), ff)
	if len(ff.GetItems()) != 1 || ff.GetItems()[0] != "get-user" {
		t.Errorf("Users folder items = %v, want [get-user]", ff.GetItems())
	}
}

func TestSlugUniquenessAndReserved(t *testing.T) {
	coll, ctx := newTestCollection(t)

	// Two distinct display names that slugify to the same base.
	if err := coll.CreateRequest(ctx, nil, "Get User", "s", "m"); err != nil {
		t.Fatal(err)
	}
	if err := coll.CreateRequest(ctx, nil, "get user", "s", "m"); err != nil {
		t.Fatal(err)
	}
	tree := filepath.Join(coll.Root(), treeDir)
	for _, slug := range []string{"get-user", "get-user-2"} {
		if _, err := os.Stat(filepath.Join(tree, slug)); err != nil {
			t.Errorf("expected slug dir %q: %v", slug, err)
		}
	}

	// A reserved name must not become its literal directory.
	if err := coll.CreateFolder(ctx, nil, "con"); err != nil {
		t.Fatal(err)
	}
	col := &grpcviewstorev1.Collection{}
	mustRead(t, coll.collectionFilePath(), col)
	for _, slug := range col.GetItems() {
		if slug == "con" {
			t.Errorf("reserved name leaked as slug %q", slug)
		}
	}

	// A duplicate display name in the same parent is rejected.
	if err := coll.CreateRequest(ctx, nil, "Get User", "s", "m"); err == nil {
		t.Error("expected duplicate display name to be rejected")
	}
}

func TestOrderedListReconciliation(t *testing.T) {
	coll, ctx := newTestCollection(t)
	for _, n := range []string{"Alpha", "Bravo", "Charlie"} {
		if err := coll.CreateFolder(ctx, nil, n); err != nil {
			t.Fatal(err)
		}
	}

	// Rewrite the root ordering: reorder, drop "bravo" from the list, and add a
	// bogus slug that has no directory on disk.
	col := &grpcviewstorev1.Collection{}
	mustRead(t, coll.collectionFilePath(), col)
	col.Items = []string{"charlie", "does-not-exist", "alpha"}
	if err := writeMessage(coll.collectionFilePath(), col); err != nil {
		t.Fatal(err)
	}

	// Load reconciles: listed+present first (charlie, alpha), bogus dropped,
	// unlisted-on-disk (bravo) appended in name order.
	got := names(rootItems(t, coll, ctx))
	want := []string{"Charlie", "Alpha", "Bravo"}
	if len(got) != len(want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}

	// A subsequent mutation should heal the on-disk list (drop the bogus slug).
	if err := coll.CreateFolder(ctx, nil, "Delta"); err != nil {
		t.Fatal(err)
	}
	mustRead(t, coll.collectionFilePath(), col)
	for _, slug := range col.GetItems() {
		if slug == "does-not-exist" {
			t.Errorf("bogus slug survived healing: %v", col.GetItems())
		}
	}
}

func TestRenameSameParent(t *testing.T) {
	coll, ctx := newTestCollection(t)
	if err := coll.CreateFolder(ctx, nil, "Folder"); err != nil {
		t.Fatal(err)
	}
	if err := coll.CreateRequest(ctx, []string{"Folder"}, "Req", "s", "m"); err != nil {
		t.Fatal(err)
	}

	// Rename the request; slug/dir must stay stable, only meta.name changes.
	if err := coll.Move(ctx, []string{"Folder", "Req"}, []string{"Folder", "Renamed"}); err != nil {
		t.Fatalf("Move(rename): %v", err)
	}
	tree := filepath.Join(coll.Root(), treeDir)
	if _, err := os.Stat(filepath.Join(tree, "folder", "req")); err != nil {
		t.Errorf("slug dir should be stable across rename: %v", err)
	}
	rf := &grpcviewstorev1.Request{}
	mustRead(t, filepath.Join(tree, "folder", "req", requestFileName), rf)
	if rf.GetMeta().GetName() != "Renamed" {
		t.Errorf("meta.name = %q, want Renamed", rf.GetMeta().GetName())
	}
	folder := childByName(rootItems(t, coll, ctx), "Folder")
	if childByName(folder.GetFolder().GetItems(), "Renamed") == nil {
		t.Errorf("renamed request not found: %v", names(folder.GetFolder().GetItems()))
	}
}

func TestUpdateRequestRename(t *testing.T) {
	coll, ctx := newTestCollection(t)
	if err := coll.CreateRequest(ctx, nil, "Get User", "s.S", "GetUser"); err != nil {
		t.Fatal(err)
	}
	if err := coll.CreateRequest(ctx, nil, "List Users", "s.S", "ListUsers"); err != nil {
		t.Fatal(err)
	}

	// Happy path: rename "Get User" -> "Fetch User". Per the slug-identity model
	// the slug/dir stays stable and only meta.name changes; a name-only patch must
	// not touch service/method.
	newName := "Fetch User"
	if err := coll.UpdateRequest(ctx, nil, "Get User", RequestPatch{Name: &newName}); err != nil {
		t.Fatalf("UpdateRequest rename: %v", err)
	}
	tree := filepath.Join(coll.Root(), treeDir)
	if _, err := os.Stat(filepath.Join(tree, "get-user")); err != nil {
		t.Errorf("slug dir should be stable across rename: %v", err)
	}
	rf := &grpcviewstorev1.Request{}
	mustRead(t, filepath.Join(tree, "get-user", requestFileName), rf)
	if rf.GetMeta().GetName() != newName {
		t.Errorf("meta.name = %q, want %q", rf.GetMeta().GetName(), newName)
	}
	if rf.GetService() != "s.S" || rf.GetMethod() != "GetUser" {
		t.Errorf("name-only patch mutated service/method: %q/%q", rf.GetService(), rf.GetMethod())
	}
	root := rootItems(t, coll, ctx)
	if childByName(root, newName) == nil || childByName(root, "Get User") != nil {
		t.Errorf("rename not reflected in tree: %v", names(root))
	}

	// Collision: renaming onto an existing sibling name fails with ErrAlreadyExists
	// and leaves the request untouched.
	collide := "List Users"
	if err := coll.UpdateRequest(ctx, nil, newName, RequestPatch{Name: &collide}); !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("rename collision = %v, want ErrAlreadyExists", err)
	}
	mustRead(t, filepath.Join(tree, "get-user", requestFileName), rf)
	if rf.GetMeta().GetName() != newName {
		t.Errorf("failed rename must not mutate meta.name, got %q", rf.GetMeta().GetName())
	}

	// A no-op rename (name == current) combined with another field still applies
	// that field without self-colliding.
	body := `{"x":1}`
	if err := coll.UpdateRequest(ctx, nil, newName, RequestPatch{Name: &newName, DraftBody: &body}); err != nil {
		t.Fatalf("no-op rename + body patch: %v", err)
	}
	mustRead(t, filepath.Join(tree, "get-user", requestFileName), rf)
	if rf.GetMeta().GetName() != newName || rf.GetDraftBody() != body {
		t.Errorf("after no-op rename + body: name=%q body=%q", rf.GetMeta().GetName(), rf.GetDraftBody())
	}
}

func TestMoveCrossFolder(t *testing.T) {
	coll, ctx := newTestCollection(t)
	for _, n := range []string{"A", "B"} {
		if err := coll.CreateFolder(ctx, nil, n); err != nil {
			t.Fatal(err)
		}
	}
	if err := coll.CreateRequest(ctx, []string{"A"}, "Req", "s", "m"); err != nil {
		t.Fatal(err)
	}

	// Move A/Req -> B/Req (slug kept since free in B).
	if err := coll.Move(ctx, []string{"A", "Req"}, []string{"B", "Req"}); err != nil {
		t.Fatalf("Move: %v", err)
	}
	tree := filepath.Join(coll.Root(), treeDir)
	if _, err := os.Stat(filepath.Join(tree, "b", "req")); err != nil {
		t.Errorf("moved dir missing in B: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tree, "a", "req")); !os.IsNotExist(err) {
		t.Errorf("source dir should be gone, stat err = %v", err)
	}
	root := rootItems(t, coll, ctx)
	if a := childByName(root, "A"); len(a.GetFolder().GetItems()) != 0 {
		t.Errorf("A should be empty, got %v", names(a.GetFolder().GetItems()))
	}
	if b := childByName(root, "B"); childByName(b.GetFolder().GetItems(), "Req") == nil {
		t.Errorf("Req not in B")
	}

	// Moving a folder into its own descendant is rejected.
	if err := coll.CreateFolder(ctx, []string{"B"}, "Inner"); err != nil {
		t.Fatal(err)
	}
	if err := coll.Move(ctx, []string{"B"}, []string{"B", "Inner", "B"}); err == nil {
		t.Error("expected move-into-descendant to be rejected")
	}
}

func TestDelete(t *testing.T) {
	coll, ctx := newTestCollection(t)
	if err := coll.CreateFolder(ctx, nil, "Folder"); err != nil {
		t.Fatal(err)
	}
	if err := coll.CreateRequest(ctx, []string{"Folder"}, "Req", "s", "m"); err != nil {
		t.Fatal(err)
	}

	// Delete a request.
	if err := coll.Delete(ctx, []string{"Folder"}, "Req"); err != nil {
		t.Fatalf("Delete request: %v", err)
	}
	folder := childByName(rootItems(t, coll, ctx), "Folder")
	if len(folder.GetFolder().GetItems()) != 0 {
		t.Errorf("request not deleted: %v", names(folder.GetFolder().GetItems()))
	}
	if _, err := os.Stat(filepath.Join(coll.Root(), treeDir, "folder", "req")); !os.IsNotExist(err) {
		t.Errorf("request dir should be gone, stat err = %v", err)
	}

	// Deleting a missing item is a no-op.
	if err := coll.Delete(ctx, []string{"Folder"}, "Ghost"); err != nil {
		t.Errorf("deleting missing item should be a no-op, got %v", err)
	}

	// Delete a folder (recursive).
	if err := coll.Delete(ctx, nil, "Folder"); err != nil {
		t.Fatalf("Delete folder: %v", err)
	}
	if len(rootItems(t, coll, ctx)) != 0 {
		t.Error("folder not deleted from root")
	}
}

func TestMigrationFromLegacyBlob(t *testing.T) {
	base := t.TempDir()
	ctx := context.Background()

	ws := &grpcviewv1.Workspace{
		Name: "legacy",
		Item: &grpcviewv1.Item{
			Name: "legacy",
			Content: &grpcviewv1.Item_Folder{Folder: &grpcviewv1.Folder{Items: []*grpcviewv1.Item{
				{Name: "Folder", Content: &grpcviewv1.Item_Folder{Folder: &grpcviewv1.Folder{Items: []*grpcviewv1.Item{
					{Name: "Req", Content: &grpcviewv1.Item_Request{Request: &grpcviewv1.Request{
						Name: "Req", Service: "s.S", Method: "M", DraftBody: `{"x":1}`,
					}}},
				}}}},
			}}},
		},
	}
	blob, err := proto.Marshal(ws)
	if err != nil {
		t.Fatal(err)
	}
	blobPath := filepath.Join(base, "legacy")
	if err := os.WriteFile(blobPath, blob, 0o644); err != nil {
		t.Fatal(err)
	}

	s := New(base, slog.New(slog.NewTextHandler(io.Discard, nil)))
	coll, err := s.Open(ctx, "legacy") // triggers migration
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if info, err := os.Stat(blobPath); err != nil || !info.IsDir() {
		t.Errorf("collection path should now be a directory (err %v)", err)
	}
	if _, err := os.Stat(blobPath + ".blob.bak"); err != nil {
		t.Errorf("legacy blob backup missing: %v", err)
	}

	root := rootItems(t, coll, ctx)
	folder := childByName(root, "Folder")
	if folder == nil || folder.GetFolder() == nil {
		t.Fatalf("migrated tree missing Folder: %v", names(root))
	}
	req := childByName(folder.GetFolder().GetItems(), "Req")
	if req == nil || req.GetRequest().GetDraftBody() != `{"x":1}` {
		t.Fatalf("migrated request wrong: %+v", req)
	}

	// Idempotent: opening again does not error or double-migrate.
	if _, err := s.Open(ctx, "legacy"); err != nil {
		t.Errorf("second Open: %v", err)
	}
}

func TestDescriptorStatePersistence(t *testing.T) {
	coll, ctx := newTestCollection(t)

	sources := []*grpcviewv1.DescriptorSource{
		{Source: &grpcviewv1.DescriptorSource_Reflection{Reflection: &grpcviewv1.Server{Host: "localhost", Port: 50051}}},
	}
	services := []*grpcviewv1.Service{
		{Package: "acme.v1", Name: "UserService", Methods: []*grpcviewv1.Method{{Name: "GetUser"}}},
	}
	if err := coll.PutDescriptorState(ctx, sources, services); err != nil {
		t.Fatalf("PutDescriptorState: %v", err)
	}

	ws, err := coll.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws.GetSources()) != 1 || ws.GetSources()[0].GetReflection().GetPort() != 50051 {
		t.Errorf("sources not round-tripped: %v", ws.GetSources())
	}
	if len(ws.GetServices()) != 1 || ws.GetServices()[0].GetName() != "UserService" {
		t.Errorf("services not round-tripped: %v", ws.GetServices())
	}

	// The resolved-schema cache lives under the gitignored state dir.
	if _, err := os.Stat(coll.servicesCachePath()); err != nil {
		t.Errorf("services cache missing: %v", err)
	}
	gi, err := os.ReadFile(filepath.Join(coll.Root(), gitignoreFileName))
	if err != nil || string(gi) == "" {
		t.Fatalf(".gitignore missing: %v", err)
	}
}

// TestAppendHistoryCapAndReload appends more runs than the cap allows and asserts
// the newest are kept (oldest dropped) in order, that history lands under the
// gitignored .grpcview/history/ sidecar (never request.json), and that it rides
// back along Request.history on a fresh reload.
func TestAppendHistoryCapAndReload(t *testing.T) {
	base := t.TempDir()
	s := New(base, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	coll, err := s.Open(ctx, "test")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := coll.EnsureCreated(ctx); err != nil {
		t.Fatalf("EnsureCreated: %v", err)
	}
	if err := coll.CreateRequest(ctx, nil, "Get User", "acme.v1.UserService", "GetUser"); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	histEntry := func(i int) *grpcviewv1.History {
		return &grpcviewv1.History{
			Request: &grpcviewv1.History_Request{Service: "acme.v1.UserService", Method: "GetUser", Body: []byte(fmt.Sprintf(`{"n":%d}`, i))},
			Response: &grpcviewv1.History_Response{
				Status:   &grpcviewv1.Status{Code: int32(i % 3)}, // mix OK and non-OK codes
				Response: []byte(fmt.Sprintf(`{"r":%d}`, i)),
			},
		}
	}

	// Append 6 runs with a cap of 3: the last three (3,4,5) survive, in order.
	const limit = 3
	for i := 0; i < 6; i++ {
		if err := coll.AppendHistory(ctx, nil, "Get User", histEntry(i), limit); err != nil {
			t.Fatalf("AppendHistory %d: %v", i, err)
		}
	}

	// History is gitignored local state: the sidecar exists, and request.json is
	// untouched by run history.
	histFile := filepath.Join(base, "test", stateDir, historyDir, "get-user", historyFileName)
	if _, err := os.Stat(histFile); err != nil {
		t.Fatalf("history sidecar missing at %s: %v", histFile, err)
	}
	// request.json is intact and carries no run history (the on-disk Request schema
	// has no history field — the sidecar is the only place run history lives).
	rf := &grpcviewstorev1.Request{}
	mustRead(t, filepath.Join(base, "test", treeDir, "get-user", requestFileName), rf)
	if rf.GetMeta().GetName() != "Get User" {
		t.Errorf("request.json meta.name = %q, want %q", rf.GetMeta().GetName(), "Get User")
	}

	assertHistory := func(t *testing.T, hist []*grpcviewv1.History) {
		t.Helper()
		if len(hist) != limit {
			t.Fatalf("history len = %d, want %d (capped)", len(hist), limit)
		}
		for j, want := range []int{3, 4, 5} {
			gotBody := string(hist[j].GetRequest().GetBody())
			if wantBody := fmt.Sprintf(`{"n":%d}`, want); gotBody != wantBody {
				t.Errorf("entry %d body = %s, want %s (order/drop-oldest)", j, gotBody, wantBody)
			}
			if got := hist[j].GetResponse().GetStatus().GetCode(); got != int32(want%3) {
				t.Errorf("entry %d status code = %d, want %d", j, got, want%3)
			}
		}
	}

	// Rides along on this collection's Load.
	assertHistory(t, historyOf(t, coll, ctx, "Get User"))

	// And survives a fresh reload from a brand-new Store over the same directory
	// (no in-memory caches shared).
	reloaded, err := New(base, slog.New(slog.NewTextHandler(io.Discard, nil))).Open(ctx, "test")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	assertHistory(t, historyOf(t, reloaded, ctx, "Get User"))
}

// historyOf loads the collection and returns the named root request's history.
func historyOf(t *testing.T, coll *Collection, ctx context.Context, name string) []*grpcviewv1.History {
	t.Helper()
	req := childByName(rootItems(t, coll, ctx), name)
	if req == nil || req.GetRequest() == nil {
		t.Fatalf("request %q not found", name)
	}
	return req.GetRequest().GetHistory()
}

func TestLoadMissingCollection(t *testing.T) {
	base := t.TempDir()
	ctx := context.Background()
	s := New(base, slog.New(slog.NewTextHandler(io.Discard, nil)))
	coll, err := s.Open(ctx, "absent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coll.Load(ctx); err != ErrNotFound {
		t.Errorf("Load of missing collection = %v, want ErrNotFound", err)
	}
}
