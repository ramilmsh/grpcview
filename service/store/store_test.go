package store

import (
	"bytes"
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

func TestDescriptorStatePersistence(t *testing.T) {
	coll, ctx := newTestCollection(t)

	sources := []*grpcviewv1.DescriptorSource{
		{
			Id:     "reflection:localhost:50051",
			Source: &grpcviewv1.DescriptorSource_Reflection{Reflection: &grpcviewv1.Server{Address: "localhost:50051"}},
		},
	}
	services := []*grpcviewv1.Service{
		{Package: "acme.v1", Name: "UserService", Methods: []*grpcviewv1.Method{{Name: "GetUser"}}},
	}
	descriptorSet := []byte{0x01, 0x02, 0x03}
	if err := coll.PutDescriptorState(ctx, DescriptorState{
		Sources:       sources,
		Services:      services,
		DescriptorSet: descriptorSet,
		Resolves: map[string]*grpcviewstorev1.ResolvedSource{
			"reflection:localhost:50051": {Id: "reflection:localhost:50051", ServiceNames: []string{"acme.v1.UserService"}},
		},
	}); err != nil {
		t.Fatalf("PutDescriptorState: %v", err)
	}

	ws, err := coll.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws.GetSources()) != 1 || ws.GetSources()[0].GetReflection().GetAddress() != "localhost:50051" {
		t.Errorf("sources not round-tripped: %v", ws.GetSources())
	}
	if len(ws.GetServices()) != 1 || ws.GetServices()[0].GetName() != "UserService" {
		t.Errorf("services not round-tripped: %v", ws.GetServices())
	}
	if !bytes.Equal(ws.GetDescriptorSet(), descriptorSet) {
		t.Errorf("descriptor set not round-tripped: got %v want %v", ws.GetDescriptorSet(), descriptorSet)
	}

	// The derived merged cache and the per-source resolve cache both live under the
	// gitignored state dir — never in the committed manifest.
	if _, err := os.Stat(coll.servicesCachePath()); err != nil {
		t.Errorf("services cache missing: %v", err)
	}
	if _, err := os.Stat(coll.sourceCachePath("reflection:localhost:50051")); err != nil {
		t.Errorf("per-source resolve cache missing: %v", err)
	}
	// A source that is no longer configured loses its cache entry, so a removed
	// source's descriptors can't come back.
	if err := coll.PutDescriptorState(ctx, DescriptorState{}); err != nil {
		t.Fatalf("PutDescriptorState (empty): %v", err)
	}
	if _, err := os.Stat(coll.sourceCachePath("reflection:localhost:50051")); !os.IsNotExist(err) {
		t.Errorf("per-source resolve cache not pruned: %v", err)
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

// TestUpdateRequestMiddleware covers the attached-middleware patch (§S3): the set-flag
// distinguishes "set" from "leave unchanged", an empty set clears the list, the list
// persists in request.json and round-trips through a fresh Store, and RequestMiddleware
// reads it back (with ErrItemNotFound for an absent request).
func TestUpdateRequestMiddleware(t *testing.T) {
	base := t.TempDir()
	ctx := context.Background()
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	coll, err := New(base, discard).Open(ctx, "test")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := coll.EnsureCreated(ctx); err != nil {
		t.Fatalf("EnsureCreated: %v", err)
	}
	if err := coll.CreateRequest(ctx, nil, "Echo", "echo.v1.EchoService", "Unary"); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	middlewareOf := func(t *testing.T, c *Collection, name string) []string {
		t.Helper()
		req := childByName(rootItems(t, c, ctx), name)
		if req == nil || req.GetRequest() == nil {
			t.Fatalf("request %q not found", name)
		}
		return req.GetRequest().GetMiddleware()
	}

	// Set the list.
	if err := coll.UpdateRequest(ctx, nil, "Echo", RequestPatch{SetMiddleware: true, Middleware: []string{"sign", "trace"}}); err != nil {
		t.Fatalf("UpdateRequest set middleware: %v", err)
	}
	if got := middlewareOf(t, coll, "Echo"); len(got) != 2 || got[0] != "sign" || got[1] != "trace" {
		t.Fatalf("middleware after set = %v, want [sign trace]", got)
	}
	// On-disk shape: the ordered list lands in request.json.
	rf := &grpcviewstorev1.Request{}
	mustRead(t, filepath.Join(coll.Root(), treeDir, "echo", requestFileName), rf)
	if len(rf.GetMiddleware()) != 2 || rf.GetMiddleware()[0] != "sign" {
		t.Fatalf("request.json middleware = %v, want [sign trace]", rf.GetMiddleware())
	}

	// RequestMiddleware reads the list back without loading the whole tree.
	if got, err := coll.RequestMiddleware(ctx, nil, "Echo"); err != nil || len(got) != 2 || got[1] != "trace" {
		t.Fatalf("RequestMiddleware = %v (err %v), want [sign trace]", got, err)
	}
	if _, err := coll.RequestMiddleware(ctx, nil, "Ghost"); !errors.Is(err, ErrItemNotFound) {
		t.Fatalf("RequestMiddleware of absent request = %v, want ErrItemNotFound", err)
	}

	// An unrelated patch (set-flag off) leaves the list untouched.
	body := `{"m":1}`
	if err := coll.UpdateRequest(ctx, nil, "Echo", RequestPatch{DraftBody: &body}); err != nil {
		t.Fatalf("UpdateRequest body only: %v", err)
	}
	if got := middlewareOf(t, coll, "Echo"); len(got) != 2 {
		t.Fatalf("middleware after unrelated patch = %v, want [sign trace] unchanged", got)
	}

	// The list survives a fresh reload from a brand-new Store over the same directory.
	reloaded, err := New(base, discard).Open(ctx, "test")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := middlewareOf(t, reloaded, "Echo"); len(got) != 2 || got[0] != "sign" {
		t.Fatalf("reloaded middleware = %v, want [sign trace]", got)
	}

	// Setting an empty list clears it.
	if err := reloaded.UpdateRequest(ctx, nil, "Echo", RequestPatch{SetMiddleware: true, Middleware: nil}); err != nil {
		t.Fatalf("UpdateRequest clear middleware: %v", err)
	}
	if got := middlewareOf(t, reloaded, "Echo"); len(got) != 0 {
		t.Fatalf("middleware after clear = %v, want empty", got)
	}
}

// TestUpdateRequestTarget covers the per-request target patch, mirroring
// TestUpdateRequestMiddleware: the set-flag distinguishes "set" from "leave
// unchanged", the target persists in request.json (with the tls bool) and
// round-trips through a fresh Store, an unrelated patch leaves it intact, and
// SetTarget with a nil Target clears it (reverting to the reflection default).
func TestUpdateRequestTarget(t *testing.T) {
	base := t.TempDir()
	ctx := context.Background()
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	coll, err := New(base, discard).Open(ctx, "test")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := coll.EnsureCreated(ctx); err != nil {
		t.Fatalf("EnsureCreated: %v", err)
	}
	if err := coll.CreateRequest(ctx, nil, "Echo", "echo.v1.EchoService", "Unary"); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	targetOf := func(t *testing.T, c *Collection) *grpcviewv1.Server {
		t.Helper()
		req := childByName(rootItems(t, c, ctx), "Echo")
		if req == nil || req.GetRequest() == nil {
			t.Fatalf("request Echo not found")
		}
		return req.GetRequest().GetTarget()
	}

	// Set the target (TLS on).
	if err := coll.UpdateRequest(ctx, nil, "Echo", RequestPatch{SetTarget: true, Target: serverFromAddressTLS("api.example.com:8443", true)}); err != nil {
		t.Fatalf("UpdateRequest set target: %v", err)
	}
	if got := targetOf(t, coll); got == nil || got.GetAddress() != "api.example.com:8443" || got.GetTls() == nil {
		t.Fatalf("target after set = %+v, want api.example.com:8443 tls", got)
	}
	// On-disk shape: the target (with the tls bool) lands in request.json.
	rf := &grpcviewstorev1.Request{}
	mustRead(t, filepath.Join(coll.Root(), treeDir, "echo", requestFileName), rf)
	if rf.GetTarget().GetAddress() != "api.example.com:8443" || !rf.GetTarget().GetTls() {
		t.Fatalf("request.json target = %+v, want api.example.com:8443 tls", rf.GetTarget())
	}

	// An unrelated patch (set-flag off) leaves the target untouched.
	body := `{"m":1}`
	if err := coll.UpdateRequest(ctx, nil, "Echo", RequestPatch{DraftBody: &body}); err != nil {
		t.Fatalf("UpdateRequest body only: %v", err)
	}
	if got := targetOf(t, coll); got == nil || got.GetAddress() != "api.example.com:8443" {
		t.Fatalf("target after unrelated patch = %+v, want unchanged", got)
	}

	// The target survives a fresh reload from a brand-new Store over the same directory.
	reloaded, err := New(base, discard).Open(ctx, "test")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := targetOf(t, reloaded); got == nil || got.GetAddress() != "api.example.com:8443" {
		t.Fatalf("reloaded target = %+v, want api.example.com:8443", got)
	}

	// SetTarget with a nil Target clears it (reverting to the reflection default).
	if err := reloaded.UpdateRequest(ctx, nil, "Echo", RequestPatch{SetTarget: true, Target: nil}); err != nil {
		t.Fatalf("UpdateRequest clear target: %v", err)
	}
	if got := targetOf(t, reloaded); got != nil {
		t.Fatalf("target after clear = %+v, want nil", got)
	}
}

// TestFolderDraftMetadataScriptRoundTrip covers gv-features-plan.md Feature 1
// Phase 1: a folder's draft_metadata_script round-trips through UpdateFolder ->
// disk (folder.json) -> Load, FolderMetadataChain returns the ordered ancestor
// scripts (root->leaf) for a node nested under those folders, a root-level node
// gets an empty chain, and a missing/non-folder path segment propagates
// ErrItemNotFound/ErrNotAFolder — the store does not swallow either (matching
// RequestMiddleware's own contract, whose ErrItemNotFound is tolerated by its
// *caller*, not by the store).
func TestFolderDraftMetadataScriptRoundTrip(t *testing.T) {
	coll, ctx := newTestCollection(t)
	if err := coll.CreateFolder(ctx, nil, "a"); err != nil {
		t.Fatalf("CreateFolder a: %v", err)
	}
	if err := coll.CreateFolder(ctx, []string{"a"}, "b"); err != nil {
		t.Fatalf("CreateFolder a/b: %v", err)
	}

	scriptA := "export default () => ({ ...gv.metadata.inherit(), fromA: ['1'] })"
	scriptB := "export default () => ({ ...gv.metadata.inherit(), fromB: ['2'] })"
	if err := coll.UpdateFolder(ctx, nil, "a", FolderPatch{DraftMetadataScript: &scriptA}); err != nil {
		t.Fatalf("UpdateFolder a: %v", err)
	}
	if err := coll.UpdateFolder(ctx, []string{"a"}, "b", FolderPatch{DraftMetadataScript: &scriptB}); err != nil {
		t.Fatalf("UpdateFolder a/b: %v", err)
	}

	// In-memory tree (Load) carries the script on both folders.
	root := rootItems(t, coll, ctx)
	aItem := childByName(root, "a")
	if aItem == nil || aItem.GetFolder() == nil {
		t.Fatalf("expected folder a, got %v", names(root))
	}
	if got := aItem.GetFolder().GetDraftMetadataScript(); got != scriptA {
		t.Errorf("folder a script = %q, want %q", got, scriptA)
	}
	bItem := childByName(aItem.GetFolder().GetItems(), "b")
	if bItem == nil || bItem.GetFolder() == nil {
		t.Fatalf("expected folder a/b, got %v", names(aItem.GetFolder().GetItems()))
	}
	if got := bItem.GetFolder().GetDraftMetadataScript(); got != scriptB {
		t.Errorf("folder a/b script = %q, want %q", got, scriptB)
	}

	// On-disk shape: folder.json carries draftMetadataScript, like a request's.
	tree := filepath.Join(coll.Root(), treeDir)
	ff := &grpcviewstorev1.Folder{}
	mustRead(t, filepath.Join(tree, "a", folderFileName), ff)
	if ff.GetDraftMetadataScript() != scriptA {
		t.Errorf("a/folder.json draftMetadataScript = %q, want %q", ff.GetDraftMetadataScript(), scriptA)
	}
	mustRead(t, filepath.Join(tree, "a", "b", folderFileName), ff)
	if ff.GetDraftMetadataScript() != scriptB {
		t.Errorf("a/b/folder.json draftMetadataScript = %q, want %q", ff.GetDraftMetadataScript(), scriptB)
	}

	// FolderMetadataChain(['a','b']) is the ancestor chain for a request that
	// LIVES inside a/b (parent path ['a','b']): both folders' scripts, root->leaf.
	chain, err := coll.FolderMetadataChain(ctx, []string{"a", "b"})
	if err != nil {
		t.Fatalf("FolderMetadataChain: %v", err)
	}
	if len(chain) != 2 || chain[0] != scriptA || chain[1] != scriptB {
		t.Fatalf("FolderMetadataChain(a,b) = %v, want [%q %q]", chain, scriptA, scriptB)
	}

	// A root-level node (no ancestor folders) gets an empty chain, no error.
	rootChain, err := coll.FolderMetadataChain(ctx, nil)
	if err != nil || len(rootChain) != 0 {
		t.Fatalf("FolderMetadataChain(root) = %v (err %v), want empty", rootChain, err)
	}

	// A missing path segment propagates ErrItemNotFound: FolderMetadataChain
	// itself does not tolerate it (a future workspace-layer caller decides to).
	if _, err := coll.FolderMetadataChain(ctx, []string{"a", "ghost"}); !errors.Is(err, ErrItemNotFound) {
		t.Fatalf("FolderMetadataChain missing path = %v, want ErrItemNotFound", err)
	}

	// A path segment that resolves to a request (not a folder) is ErrNotAFolder.
	if err := coll.CreateRequest(ctx, []string{"a", "b"}, "Leaf", "s", "m"); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	if _, err := coll.FolderMetadataChain(ctx, []string{"a", "b", "Leaf"}); !errors.Is(err, ErrNotAFolder) {
		t.Fatalf("FolderMetadataChain through a request = %v, want ErrNotAFolder", err)
	}

	// Clearing (empty-but-present) removes the script.
	empty := ""
	if err := coll.UpdateFolder(ctx, nil, "a", FolderPatch{DraftMetadataScript: &empty}); err != nil {
		t.Fatalf("UpdateFolder clear: %v", err)
	}
	root = rootItems(t, coll, ctx)
	if got := childByName(root, "a").GetFolder().GetDraftMetadataScript(); got != "" {
		t.Errorf("folder a script after clear = %q, want empty", got)
	}

	// An unset (nil) patch leaves the script unchanged.
	if err := coll.UpdateFolder(ctx, []string{"a"}, "b", FolderPatch{}); err != nil {
		t.Fatalf("UpdateFolder no-op: %v", err)
	}
	root = rootItems(t, coll, ctx)
	bItem = childByName(childByName(root, "a").GetFolder().GetItems(), "b")
	if got := bItem.GetFolder().GetDraftMetadataScript(); got != scriptB {
		t.Errorf("folder a/b script after no-op patch = %q, want unchanged %q", got, scriptB)
	}

	// UpdateFolder surfaces the same sentinels as FolderMetadataChain/UpdateRequest
	// for a missing folder / an item that isn't a folder.
	if err := coll.UpdateFolder(ctx, nil, "ghost", FolderPatch{DraftMetadataScript: &scriptA}); !errors.Is(err, ErrItemNotFound) {
		t.Fatalf("UpdateFolder missing = %v, want ErrItemNotFound", err)
	}
	if err := coll.UpdateFolder(ctx, []string{"a", "b"}, "Leaf", FolderPatch{DraftMetadataScript: &scriptA}); !errors.Is(err, ErrNotAFolder) {
		t.Fatalf("UpdateFolder on a request = %v, want ErrNotAFolder", err)
	}
}

// TestUpdateFolderRename covers tree-rewrite-plan.md T4a: a folder rename follows
// the same slug-identity model as a request's, which matters more for a folder
// because its directory is also every descendant's prefix — so the children must
// stay reachable and the folder's recorded child order must survive. It also
// covers colliding with a sibling of either kind, a no-op rename, and a rename
// combined with a DraftMetadataScript patch in one call.
func TestUpdateFolderRename(t *testing.T) {
	coll, ctx := newTestCollection(t)
	if err := coll.CreateFolder(ctx, nil, "Users"); err != nil {
		t.Fatal(err)
	}
	if err := coll.CreateFolder(ctx, nil, "Admin"); err != nil {
		t.Fatal(err)
	}
	// A sibling *request* of the folder, to prove the collision check spans kinds.
	if err := coll.CreateRequest(ctx, nil, "Ping", "s", "m"); err != nil {
		t.Fatal(err)
	}
	// Two children so the folder's Items ordering is observable across the rename.
	if err := coll.CreateRequest(ctx, []string{"Users"}, "Get User", "s.S", "GetUser"); err != nil {
		t.Fatal(err)
	}
	if err := coll.CreateRequest(ctx, []string{"Users"}, "List Users", "s.S", "ListUsers"); err != nil {
		t.Fatal(err)
	}

	// Happy path: only meta.name changes; the slug dir (and its children) stay put.
	newName := "People"
	if err := coll.UpdateFolder(ctx, nil, "Users", FolderPatch{Name: &newName}); err != nil {
		t.Fatalf("UpdateFolder rename: %v", err)
	}
	tree := filepath.Join(coll.Root(), treeDir)
	if _, err := os.Stat(filepath.Join(tree, "users")); err != nil {
		t.Errorf("slug dir should be stable across rename: %v", err)
	}
	ff := &grpcviewstorev1.Folder{}
	mustRead(t, filepath.Join(tree, "users", folderFileName), ff)
	if ff.GetMeta().GetName() != newName {
		t.Errorf("folder.json meta.name = %q, want %q", ff.GetMeta().GetName(), newName)
	}
	if got := ff.GetItems(); len(got) != 2 || got[0] != "get-user" || got[1] != "list-users" {
		t.Errorf("child slug order = %v, want [get-user list-users]", got)
	}
	// The parent's recorded order still names the *slug*, which the rename didn't touch.
	col := &grpcviewstorev1.Collection{}
	mustRead(t, coll.collectionFilePath(), col)
	if len(col.GetItems()) != 3 || col.GetItems()[0] != "users" {
		t.Errorf("root items = %v, want users first", col.GetItems())
	}
	root := rootItems(t, coll, ctx)
	renamed := childByName(root, newName)
	if renamed == nil || renamed.GetFolder() == nil || childByName(root, "Users") != nil {
		t.Fatalf("rename not reflected in tree: %v", names(root))
	}
	// Children are still there, in order, and still reachable by the NEW path.
	if got := names(renamed.GetFolder().GetItems()); len(got) != 2 || got[0] != "Get User" || got[1] != "List Users" {
		t.Errorf("children after rename = %v, want [Get User, List Users]", got)
	}
	body := `{"id":"1"}`
	if err := coll.UpdateRequest(ctx, []string{newName}, "Get User", RequestPatch{DraftBody: &body}); err != nil {
		t.Errorf("child unreachable under renamed folder: %v", err)
	}

	// Collision with a sibling folder, and with a sibling request: both ErrAlreadyExists,
	// and neither mutates meta.name.
	for _, collide := range []string{"Admin", "Ping"} {
		if err := coll.UpdateFolder(ctx, nil, newName, FolderPatch{Name: &collide}); !errors.Is(err, ErrAlreadyExists) {
			t.Errorf("rename onto %q = %v, want ErrAlreadyExists", collide, err)
		}
	}
	mustRead(t, filepath.Join(tree, "users", folderFileName), ff)
	if ff.GetMeta().GetName() != newName {
		t.Errorf("failed rename must not mutate meta.name, got %q", ff.GetMeta().GetName())
	}

	// A no-op rename (name == current) succeeds without self-colliding, and applies
	// a DraftMetadataScript patched in the same call.
	script := "export default () => ({ team: ['people'] })"
	if err := coll.UpdateFolder(ctx, nil, newName, FolderPatch{Name: &newName, DraftMetadataScript: &script}); err != nil {
		t.Fatalf("no-op rename + script patch: %v", err)
	}
	mustRead(t, filepath.Join(tree, "users", folderFileName), ff)
	if ff.GetMeta().GetName() != newName || ff.GetDraftMetadataScript() != script {
		t.Errorf("after no-op rename + script: name=%q script=%q", ff.GetMeta().GetName(), ff.GetDraftMetadataScript())
	}

	// A real rename and a script patch in one call apply both.
	final := "Humans"
	script2 := "export default () => ({ team: ['humans'] })"
	if err := coll.UpdateFolder(ctx, nil, newName, FolderPatch{Name: &final, DraftMetadataScript: &script2}); err != nil {
		t.Fatalf("rename + script patch: %v", err)
	}
	mustRead(t, filepath.Join(tree, "users", folderFileName), ff)
	if ff.GetMeta().GetName() != final || ff.GetDraftMetadataScript() != script2 {
		t.Errorf("after rename + script: name=%q script=%q", ff.GetMeta().GetName(), ff.GetDraftMetadataScript())
	}
}

// TestResolveRequest covers the store.ResolveRequest refactor (gv-features-plan.md
// Feature 3's ResolveRequest bullet): resolving a saved request by display-name
// path to its wire shape, and propagating ErrItemNotFound / ErrNotARequest
// exactly like RequestMiddleware does for a missing item / a folder.
func TestResolveRequest(t *testing.T) {
	coll, ctx := newTestCollection(t)
	if err := coll.CreateFolder(ctx, nil, "Users"); err != nil {
		t.Fatal(err)
	}
	if err := coll.CreateRequest(ctx, []string{"Users"}, "Get User", "acme.v1.UserService", "GetUser"); err != nil {
		t.Fatal(err)
	}
	body := `{"id":"42"}`
	if err := coll.UpdateRequest(ctx, []string{"Users"}, "Get User", RequestPatch{DraftBody: &body}); err != nil {
		t.Fatal(err)
	}

	req, err := coll.ResolveRequest(ctx, []string{"Users"}, "Get User")
	if err != nil {
		t.Fatalf("ResolveRequest: %v", err)
	}
	if req.GetName() != "Get User" || req.GetService() != "acme.v1.UserService" || req.GetMethod() != "GetUser" || req.GetDraftBody() != body {
		t.Errorf("ResolveRequest = %+v, want name/service/method/draftBody to match", req)
	}

	if _, err := coll.ResolveRequest(ctx, nil, "Ghost"); !errors.Is(err, ErrItemNotFound) {
		t.Errorf("ResolveRequest missing = %v, want ErrItemNotFound", err)
	}
	if _, err := coll.ResolveRequest(ctx, nil, "Users"); !errors.Is(err, ErrNotARequest) {
		t.Errorf("ResolveRequest on a folder = %v, want ErrNotARequest", err)
	}
}
