package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	grpcviewstorev1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/store/v1"
	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

// newTestStore returns a Store rooted at two independent temp dirs — a workspace root and
// a state root — mirroring how a real Store never keeps state inside the workspace.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	return New(t.TempDir(), t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func newTestCollection(t *testing.T) (*Collection, context.Context) {
	t.Helper()
	s := newTestStore(t)
	ctx := context.Background()
	coll, err := s.Open(ctx, "test")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := coll.Create(ctx, ""); err != nil {
		t.Fatalf("Create: %v", err)
	}
	return coll, ctx
}

func childByName(items []*grpcviewv1.Item, name string) *grpcviewv1.Item {
	for _, it := range items {
		if it.GetName() == name {
			return it
		}
	}
	return nil
}

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

func TestSlugUniqueness(t *testing.T) {
	coll, ctx := newTestCollection(t)

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

	if err := coll.CreateRequest(ctx, nil, "Get User", "s", "m"); err == nil {
		t.Error("expected duplicate display name to be rejected")
	}
}

// The wire Item carries the on-disk slug, and a rename never changes it — the
// invariant the UI's slug-keyed tree state depends on.
func TestWireItemCarriesStableSlug(t *testing.T) {
	coll, ctx := newTestCollection(t)
	if err := coll.CreateFolder(ctx, nil, "Users"); err != nil {
		t.Fatal(err)
	}
	if err := coll.CreateRequest(ctx, []string{"Users"}, "Get User", "s.S", "GetUser"); err != nil {
		t.Fatal(err)
	}

	folder := childByName(rootItems(t, coll, ctx), "Users")
	if folder.GetSlug() != "users" {
		t.Errorf("folder slug = %q, want %q", folder.GetSlug(), "users")
	}
	if got := childByName(folder.GetFolder().GetItems(), "Get User").GetSlug(); got != "get-user" {
		t.Errorf("request slug = %q, want %q", got, "get-user")
	}

	newName := "Fetch User"
	if err := coll.UpdateRequest(ctx, []string{"Users"}, "Get User", RequestPatch{Name: &newName}); err != nil {
		t.Fatalf("UpdateRequest rename: %v", err)
	}
	folder = childByName(rootItems(t, coll, ctx), "Users")
	if got := childByName(folder.GetFolder().GetItems(), newName).GetSlug(); got != "get-user" {
		t.Errorf("slug after rename = %q, want %q", got, "get-user")
	}
}

func TestOrderedListReconciliation(t *testing.T) {
	coll, ctx := newTestCollection(t)
	for _, n := range []string{"Alpha", "Bravo", "Charlie"} {
		if err := coll.CreateFolder(ctx, nil, n); err != nil {
			t.Fatal(err)
		}
	}

	col := &grpcviewstorev1.Collection{}
	mustRead(t, coll.collectionFilePath(), col)
	col.Items = []string{"charlie", "does-not-exist", "alpha"}
	if err := writeMessage(coll.collectionFilePath(), col); err != nil {
		t.Fatal(err)
	}

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

	collide := "List Users"
	if err := coll.UpdateRequest(ctx, nil, newName, RequestPatch{Name: &collide}); !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("rename collision = %v, want ErrAlreadyExists", err)
	}
	mustRead(t, filepath.Join(tree, "get-user", requestFileName), rf)
	if rf.GetMeta().GetName() != newName {
		t.Errorf("failed rename must not mutate meta.name, got %q", rf.GetMeta().GetName())
	}

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

	if err := coll.Delete(ctx, []string{"Folder"}, "Ghost"); err != nil {
		t.Errorf("deleting missing item should be a no-op, got %v", err)
	}

	if err := coll.Delete(ctx, nil, "Folder"); err != nil {
		t.Fatalf("Delete folder: %v", err)
	}
	if len(rootItems(t, coll, ctx)) != 0 {
		t.Error("folder not deleted from root")
	}
}

func before(name string) *string { return &name }

func folderItems(t *testing.T, coll *Collection, ctx context.Context, path ...string) []*grpcviewv1.Item {
	t.Helper()
	items := rootItems(t, coll, ctx)
	for _, name := range path {
		it := childByName(items, name)
		if it == nil || it.GetFolder() == nil {
			t.Fatalf("folder %q not found under path %v", name, path)
		}
		items = it.GetFolder().GetItems()
	}
	return items
}

func mustDir(t *testing.T, coll *Collection, slugs ...string) {
	t.Helper()
	p := filepath.Join(append([]string{coll.Root(), treeDir}, slugs...)...)
	if _, err := os.Stat(p); err != nil {
		t.Errorf("expected dir %s: %v", p, err)
	}
}

func mustNotDir(t *testing.T, coll *Collection, slugs ...string) {
	t.Helper()
	p := filepath.Join(append([]string{coll.Root(), treeDir}, slugs...)...)
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("dir %s should be gone, stat err = %v", p, err)
	}
}

func TestMoveReparent(t *testing.T) {
	coll, ctx := newTestCollection(t)
	if err := coll.CreateFolder(ctx, nil, "Folder"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"A", "B"} {
		if err := coll.CreateRequest(ctx, []string{"Folder"}, name, "s", "m"); err != nil {
			t.Fatal(err)
		}
	}
	if err := coll.CreateRequest(ctx, nil, "R1", "s", "m"); err != nil {
		t.Fatal(err)
	}

	if err := coll.Move(ctx, []string{"Folder"}, "A", nil, nil); err != nil {
		t.Fatalf("Move to root: %v", err)
	}
	if got := names(folderItems(t, coll, ctx)); len(got) != 3 || got[0] != "Folder" || got[1] != "R1" || got[2] != "A" {
		t.Errorf("root order = %v, want [Folder R1 A]", got)
	}
	if got := names(folderItems(t, coll, ctx, "Folder")); len(got) != 1 || got[0] != "B" {
		t.Errorf("source folder = %v, want [B]", got)
	}
	mustDir(t, coll, "a")
	mustNotDir(t, coll, "folder", "a")

	if err := coll.Move(ctx, nil, "A", []string{"Folder"}, before("B")); err != nil {
		t.Fatalf("Move into folder: %v", err)
	}
	if got := names(folderItems(t, coll, ctx, "Folder")); len(got) != 2 || got[0] != "A" || got[1] != "B" {
		t.Errorf("dest folder = %v, want [A B]", got)
	}
	if got := names(folderItems(t, coll, ctx)); len(got) != 2 || got[0] != "Folder" || got[1] != "R1" {
		t.Errorf("root order = %v, want [Folder R1]", got)
	}
	mustDir(t, coll, "folder", "a")
	mustNotDir(t, coll, "a")
}

func TestMoveFolderWithChildren(t *testing.T) {
	coll, ctx := newTestCollection(t)
	if err := coll.CreateFolder(ctx, nil, "Outer"); err != nil {
		t.Fatal(err)
	}
	if err := coll.CreateFolder(ctx, []string{"Outer"}, "Inner"); err != nil {
		t.Fatal(err)
	}
	if err := coll.CreateRequest(ctx, []string{"Outer", "Inner"}, "Deep", "s.S", "Deep"); err != nil {
		t.Fatal(err)
	}
	if err := coll.CreateFolder(ctx, nil, "Target"); err != nil {
		t.Fatal(err)
	}

	if err := coll.Move(ctx, []string{"Outer"}, "Inner", []string{"Target"}, nil); err != nil {
		t.Fatalf("Move folder: %v", err)
	}
	if got := names(folderItems(t, coll, ctx, "Outer")); len(got) != 0 {
		t.Errorf("source folder = %v, want empty", got)
	}
	if got := names(folderItems(t, coll, ctx, "Target", "Inner")); len(got) != 1 || got[0] != "Deep" {
		t.Errorf("moved subtree = %v, want [Deep]", got)
	}
	mustDir(t, coll, "target", "inner", "deep")
	mustNotDir(t, coll, "outer", "inner")
	if _, err := coll.ResolveRequest(ctx, []string{"Target", "Inner"}, "Deep"); err != nil {
		t.Errorf("descendant unreachable after move: %v", err)
	}
}

func TestMoveReorderWithinParent(t *testing.T) {
	coll, ctx := newTestCollection(t)
	for _, name := range []string{"A", "B", "C"} {
		if err := coll.CreateRequest(ctx, nil, name, "s", "m"); err != nil {
			t.Fatal(err)
		}
	}

	if err := coll.Move(ctx, nil, "C", nil, before("A")); err != nil {
		t.Fatalf("reorder: %v", err)
	}
	if got := names(folderItems(t, coll, ctx)); len(got) != 3 || got[0] != "C" || got[1] != "A" || got[2] != "B" {
		t.Errorf("root order = %v, want [C A B]", got)
	}
	mustDir(t, coll, "c")

	if err := coll.Move(ctx, nil, "C", nil, nil); err != nil {
		t.Fatalf("reorder append: %v", err)
	}
	if got := names(folderItems(t, coll, ctx)); len(got) != 3 || got[0] != "A" || got[1] != "B" || got[2] != "C" {
		t.Errorf("root order = %v, want [A B C]", got)
	}

	if err := coll.Move(ctx, nil, "A", nil, before("Ghost")); err != nil {
		t.Fatalf("stale before should append, got %v", err)
	}
	if got := names(folderItems(t, coll, ctx)); len(got) != 3 || got[0] != "B" || got[1] != "C" || got[2] != "A" {
		t.Errorf("root order = %v, want [B C A]", got)
	}
}

func TestMoveRejections(t *testing.T) {
	coll, ctx := newTestCollection(t)
	for _, name := range []string{"Src", "Dst"} {
		if err := coll.CreateFolder(ctx, nil, name); err != nil {
			t.Fatal(err)
		}
		if err := coll.CreateRequest(ctx, []string{name}, "Req", "s", "m"); err != nil {
			t.Fatal(err)
		}
	}
	if err := coll.CreateFolder(ctx, []string{"Src"}, "Mid"); err != nil {
		t.Fatal(err)
	}
	if err := coll.CreateFolder(ctx, []string{"Src", "Mid"}, "Leaf"); err != nil {
		t.Fatal(err)
	}

	if err := coll.Move(ctx, []string{"Src"}, "Req", []string{"Dst"}, nil); !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("colliding move = %v, want ErrAlreadyExists", err)
	}
	if err := coll.Move(ctx, nil, "Src", []string{"Src"}, nil); !errors.Is(err, ErrMoveIntoDescendant) {
		t.Errorf("move into self = %v, want ErrMoveIntoDescendant", err)
	}
	if err := coll.Move(ctx, nil, "Src", []string{"Src", "Mid", "Leaf"}, nil); !errors.Is(err, ErrMoveIntoDescendant) {
		t.Errorf("move into grandchild = %v, want ErrMoveIntoDescendant", err)
	}
	if err := coll.Move(ctx, nil, "Ghost", []string{"Dst"}, nil); !errors.Is(err, ErrItemNotFound) {
		t.Errorf("move missing item = %v, want ErrItemNotFound", err)
	}
	if got := names(folderItems(t, coll, ctx, "Src")); len(got) != 2 || got[0] != "Req" || got[1] != "Mid" {
		t.Errorf("Src after rejections = %v, want [Req Mid]", got)
	}
	mustDir(t, coll, "src", "mid", "leaf")
}

func TestMoveSlugCollision(t *testing.T) {
	coll, ctx := newTestCollection(t)
	for _, name := range []string{"Src", "Dst"} {
		if err := coll.CreateFolder(ctx, nil, name); err != nil {
			t.Fatal(err)
		}
	}
	if err := coll.CreateRequest(ctx, []string{"Dst"}, "Get User", "s", "m"); err != nil {
		t.Fatal(err)
	}
	if err := coll.CreateRequest(ctx, []string{"Src"}, "get user", "s", "m"); err != nil {
		t.Fatal(err)
	}
	mustDir(t, coll, "dst", "get-user")
	mustDir(t, coll, "src", "get-user")

	if err := coll.Move(ctx, []string{"Src"}, "get user", []string{"Dst"}, nil); err != nil {
		t.Fatalf("Move with colliding slug: %v", err)
	}
	mustDir(t, coll, "dst", "get-user")
	mustDir(t, coll, "dst", "get-user-2")
	mustNotDir(t, coll, "src", "get-user")
	if got := names(folderItems(t, coll, ctx, "Dst")); len(got) != 2 || got[0] != "Get User" || got[1] != "get user" {
		t.Errorf("Dst after move = %v, want [Get User, get user]", got)
	}
	ff := &grpcviewstorev1.Folder{}
	mustRead(t, filepath.Join(coll.Root(), treeDir, "dst", folderFileName), ff)
	if got := ff.GetItems(); len(got) != 2 || got[0] != "get-user" || got[1] != "get-user-2" {
		t.Errorf("Dst slug order = %v, want [get-user get-user-2]", got)
	}
	if _, err := coll.ResolveRequest(ctx, []string{"Dst"}, "get user"); err != nil {
		t.Errorf("moved request unreachable: %v", err)
	}
}

// fdsNamed is a minimal descriptor set: the store never links what it stores, so a file name is
// enough to tell two schemas apart by content.
func fdsNamed(names ...string) *descriptorpb.FileDescriptorSet {
	fds := &descriptorpb.FileDescriptorSet{}
	for _, name := range names {
		fds.File = append(fds.File, &descriptorpb.FileDescriptorProto{Name: proto.String(name)})
	}
	return fds
}

func reflectionSourceAt(address string) *grpcviewv1.DescriptorSource {
	return &grpcviewv1.DescriptorSource{
		Id:     "reflection:" + address,
		Source: &grpcviewv1.DescriptorSource_Reflection{Reflection: &grpcviewv1.Server{Address: address}},
	}
}

// blobNames lists the descriptor blobs in a workspace's state root, which is where the sharing
// between collections is visible: one file per distinct content, whoever points at it.
func blobNames(t *testing.T, s *Store) []string {
	t.Helper()
	entries, err := os.ReadDir(s.blobsRoot())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("ReadDir blobs: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	slices.Sort(names)
	return names
}

func TestDescriptorStatePersistence(t *testing.T) {
	coll, ctx := newTestCollection(t)

	const id = "reflection:localhost:50051"
	if err := coll.PutDescriptorState(ctx, DescriptorState{
		Sources: []*grpcviewv1.DescriptorSource{reflectionSourceAt("localhost:50051")},
		Resolves: map[string]*grpcviewstorev1.ResolvedSource{
			id: {Id: id, DescriptorSet: fdsNamed("acme/v1/user.proto"), ServiceNames: []string{"acme.v1.UserService"}},
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
	// The store persists no merged view at all any more: Load answers the manifest, the tree and
	// the scripts, and the layer above derives the rest from the blobs.
	if len(ws.GetServices()) != 0 || len(ws.GetDescriptorSet()) != 0 {
		t.Errorf("Load returned a merged view the store must not hold: %v / %d bytes",
			ws.GetServices(), len(ws.GetDescriptorSet()))
	}

	blobs, err := coll.DescriptorBlobs(ctx)
	if err != nil {
		t.Fatalf("DescriptorBlobs: %v", err)
	}
	blob, ok := blobs[id]
	if !ok {
		t.Fatalf("no blob for %q: %v", id, blobs)
	}
	if got := blob.GetDescriptorSet().GetFile(); len(got) != 1 || got[0].GetName() != "acme/v1/user.proto" {
		t.Errorf("blob descriptors not round-tripped: %v", got)
	}
	if got := blob.GetServiceNames(); len(got) != 1 || got[0] != "acme.v1.UserService" {
		t.Errorf("served service names not round-tripped: %v", got)
	}
	if got := blobNames(t, coll.store); len(got) != 1 {
		t.Errorf("want exactly one blob for one resolved source, got %v", got)
	}

	if err := coll.PutDescriptorState(ctx, DescriptorState{}); err != nil {
		t.Fatalf("PutDescriptorState (empty): %v", err)
	}
	if got := blobNames(t, coll.store); len(got) != 0 {
		t.Errorf("dropping the last source must collect its blob, still have %v", got)
	}
	blobs, err = coll.DescriptorBlobs(ctx)
	if err != nil {
		t.Fatalf("DescriptorBlobs after removal: %v", err)
	}
	if len(blobs) != 0 {
		t.Errorf("index not rewritten on removal: %v", blobs)
	}
}

// TestDescriptorBlobsAreSharedAcrossCollections is the reason the blobs are content-addressed and
// workspace-scoped: a monorepo where five collections point at one schema must hold one copy of
// it, and collecting one collection's garbage must not reach into another's.
func TestDescriptorBlobsAreSharedAcrossCollections(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const id = "reflection:localhost:50051"
	shared := fdsNamed("acme/v1/user.proto")
	colls := make([]*Collection, 0, 2)
	for _, name := range []string{"payments", "ledger"} {
		coll, err := s.Open(ctx, name)
		if err != nil {
			t.Fatalf("Open %s: %v", name, err)
		}
		if err := coll.Create(ctx, ""); err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
		if err := coll.PutDescriptorState(ctx, DescriptorState{
			Sources: []*grpcviewv1.DescriptorSource{reflectionSourceAt("localhost:50051")},
			Resolves: map[string]*grpcviewstorev1.ResolvedSource{
				// Independently built, byte-identical: the two collections resolved the same
				// target, which is exactly the case CAS has to collapse.
				id: {Id: id, DescriptorSet: proto.CloneOf(shared), ServiceNames: []string{"acme.v1.UserService"}},
			},
		}); err != nil {
			t.Fatalf("PutDescriptorState %s: %v", name, err)
		}
		colls = append(colls, coll)
	}

	if got := blobNames(t, s); len(got) != 1 {
		t.Fatalf("two collections resolving to identical bytes must share one blob, got %v", got)
	}

	// Re-writing identical content is not a write at all, so nothing churns and a reorder costs
	// no file system traffic.
	before, err := os.Stat(s.blobPath(strings.TrimSuffix(blobNames(t, s)[0], blobFileExt)))
	if err != nil {
		t.Fatalf("Stat blob: %v", err)
	}
	if err := colls[0].PutDescriptorState(ctx, DescriptorState{
		Sources: []*grpcviewv1.DescriptorSource{reflectionSourceAt("localhost:50051")},
		Resolves: map[string]*grpcviewstorev1.ResolvedSource{
			id: {Id: id, DescriptorSet: proto.CloneOf(shared), ServiceNames: []string{"acme.v1.UserService"}},
		},
	}); err != nil {
		t.Fatalf("PutDescriptorState (identical rewrite): %v", err)
	}
	if got := blobNames(t, s); len(got) != 1 {
		t.Fatalf("an identical resolve must not add a blob, got %v", got)
	}
	after, err := os.Stat(s.blobPath(strings.TrimSuffix(blobNames(t, s)[0], blobFileExt)))
	if err != nil {
		t.Fatalf("Stat blob after rewrite: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("an identical resolve rewrote the blob (mtime %v -> %v)", before.ModTime(), after.ModTime())
	}

	// One collection dropping the source must not collect bytes the other still points at.
	if err := colls[0].PutDescriptorState(ctx, DescriptorState{}); err != nil {
		t.Fatalf("PutDescriptorState (drop in payments): %v", err)
	}
	if got := blobNames(t, s); len(got) != 1 {
		t.Fatalf("a blob another collection references must survive the drop, got %v", got)
	}
	blobs, err := colls[1].DescriptorBlobs(ctx)
	if err != nil {
		t.Fatalf("DescriptorBlobs (ledger): %v", err)
	}
	if _, ok := blobs[id]; !ok {
		t.Errorf("ledger lost its source's descriptors when payments dropped its own: %v", blobs)
	}

	// With the last reference gone it is garbage, and the next write collects it.
	if err := colls[1].PutDescriptorState(ctx, DescriptorState{}); err != nil {
		t.Fatalf("PutDescriptorState (drop in ledger): %v", err)
	}
	if got := blobNames(t, s); len(got) != 0 {
		t.Errorf("want the unreferenced blob collected, got %v", got)
	}
}

// TestPutDescriptorStateKeepsUnresolvedIDs pins the contract every non-acquiring mutation relies
// on: a reorder or an unrelated add re-resolves nothing, so an id absent from Resolves must keep
// pointing at what it last resolved to rather than going blank.
func TestPutDescriptorStateKeepsUnresolvedIDs(t *testing.T) {
	coll, ctx := newTestCollection(t)

	const (
		kept  = "reflection:localhost:1"
		added = "reflection:localhost:2"
	)
	if err := coll.PutDescriptorState(ctx, DescriptorState{
		Sources: []*grpcviewv1.DescriptorSource{reflectionSourceAt("localhost:1")},
		Resolves: map[string]*grpcviewstorev1.ResolvedSource{
			kept: {Id: kept, DescriptorSet: fdsNamed("one.proto"), ServiceNames: []string{"one.One"}},
		},
	}); err != nil {
		t.Fatalf("PutDescriptorState (first): %v", err)
	}

	if err := coll.PutDescriptorState(ctx, DescriptorState{
		Sources: []*grpcviewv1.DescriptorSource{reflectionSourceAt("localhost:1"), reflectionSourceAt("localhost:2")},
		Resolves: map[string]*grpcviewstorev1.ResolvedSource{
			added: {Id: added, DescriptorSet: fdsNamed("two.proto"), ServiceNames: []string{"two.Two"}},
		},
	}); err != nil {
		t.Fatalf("PutDescriptorState (second): %v", err)
	}

	blobs, err := coll.DescriptorBlobs(ctx)
	if err != nil {
		t.Fatalf("DescriptorBlobs: %v", err)
	}
	if got := blobs[kept].GetServiceNames(); len(got) != 1 || got[0] != "one.One" {
		t.Errorf("the untouched source lost its entry: %v", got)
	}
	if got := blobs[added].GetServiceNames(); len(got) != 1 || got[0] != "two.Two" {
		t.Errorf("the added source has no entry: %v", got)
	}
	if got := blobNames(t, coll.store); len(got) != 2 {
		t.Errorf("want both sources' blobs kept, got %v", got)
	}
}

func TestAppendHistoryCapAndReload(t *testing.T) {
	root, state := t.TempDir(), t.TempDir()
	s := New(root, state, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()
	coll, err := s.Open(ctx, "test")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := coll.Create(ctx, ""); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := coll.CreateRequest(ctx, nil, "Get User", "acme.v1.UserService", "GetUser"); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	histEntry := func(i int) *grpcviewv1.History {
		return &grpcviewv1.History{
			Request: &grpcviewv1.History_Request{Service: "acme.v1.UserService", Method: "GetUser", Body: []byte(fmt.Sprintf(`{"n":%d}`, i))},
			Response: &grpcviewv1.History_Response{
				Status:   &grpcviewv1.Status{Code: int32(i % 3)},
				Response: []byte(fmt.Sprintf(`{"r":%d}`, i)),
			},
		}
	}

	const limit = 3
	for i := 0; i < 6; i++ {
		if err := coll.AppendHistory(ctx, nil, "Get User", histEntry(i), limit); err != nil {
			t.Fatalf("AppendHistory %d: %v", i, err)
		}
	}

	histFile := filepath.Join(coll.State(), historyDir, "get-user", historyFileName)
	if _, err := os.Stat(histFile); err != nil {
		t.Fatalf("history sidecar missing at %s: %v", histFile, err)
	}
	rf := &grpcviewstorev1.Request{}
	mustRead(t, filepath.Join(root, "test", treeDir, "get-user", requestFileName), rf)
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

	assertHistory(t, historyOf(t, coll, ctx, "Get User"))

	reloaded, err := New(root, state, slog.New(slog.NewTextHandler(io.Discard, nil))).Open(ctx, "test")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	assertHistory(t, historyOf(t, reloaded, ctx, "Get User"))
}

func historyOf(t *testing.T, coll *Collection, ctx context.Context, name string) []*grpcviewv1.History {
	t.Helper()
	req := childByName(rootItems(t, coll, ctx), name)
	if req == nil || req.GetRequest() == nil {
		t.Fatalf("request %q not found", name)
	}
	return req.GetRequest().GetHistory()
}

func TestLoadMissingCollection(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	coll, err := s.Open(ctx, "absent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coll.Load(ctx); err != ErrNotFound {
		t.Errorf("Load of missing collection = %v, want ErrNotFound", err)
	}
}

func TestUpdateRequestMiddleware(t *testing.T) {
	root, state := t.TempDir(), t.TempDir()
	ctx := context.Background()
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	coll, err := New(root, state, discard).Open(ctx, "test")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := coll.Create(ctx, ""); err != nil {
		t.Fatalf("Create: %v", err)
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

	if err := coll.UpdateRequest(ctx, nil, "Echo", RequestPatch{SetMiddleware: true, Middleware: []string{"sign", "trace"}}); err != nil {
		t.Fatalf("UpdateRequest set middleware: %v", err)
	}
	if got := middlewareOf(t, coll, "Echo"); len(got) != 2 || got[0] != "sign" || got[1] != "trace" {
		t.Fatalf("middleware after set = %v, want [sign trace]", got)
	}
	rf := &grpcviewstorev1.Request{}
	mustRead(t, filepath.Join(coll.Root(), treeDir, "echo", requestFileName), rf)
	if len(rf.GetMiddleware()) != 2 || rf.GetMiddleware()[0] != "sign" {
		t.Fatalf("request.json middleware = %v, want [sign trace]", rf.GetMiddleware())
	}

	if got, err := coll.RequestMiddleware(ctx, nil, "Echo"); err != nil || len(got) != 2 || got[1] != "trace" {
		t.Fatalf("RequestMiddleware = %v (err %v), want [sign trace]", got, err)
	}
	if _, err := coll.RequestMiddleware(ctx, nil, "Ghost"); !errors.Is(err, ErrItemNotFound) {
		t.Fatalf("RequestMiddleware of absent request = %v, want ErrItemNotFound", err)
	}

	body := `{"m":1}`
	if err := coll.UpdateRequest(ctx, nil, "Echo", RequestPatch{DraftBody: &body}); err != nil {
		t.Fatalf("UpdateRequest body only: %v", err)
	}
	if got := middlewareOf(t, coll, "Echo"); len(got) != 2 {
		t.Fatalf("middleware after unrelated patch = %v, want [sign trace] unchanged", got)
	}

	reloaded, err := New(root, state, discard).Open(ctx, "test")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := middlewareOf(t, reloaded, "Echo"); len(got) != 2 || got[0] != "sign" {
		t.Fatalf("reloaded middleware = %v, want [sign trace]", got)
	}

	if err := reloaded.UpdateRequest(ctx, nil, "Echo", RequestPatch{SetMiddleware: true, Middleware: nil}); err != nil {
		t.Fatalf("UpdateRequest clear middleware: %v", err)
	}
	if got := middlewareOf(t, reloaded, "Echo"); len(got) != 0 {
		t.Fatalf("middleware after clear = %v, want empty", got)
	}
}

func TestUpdateRequestTarget(t *testing.T) {
	root, state := t.TempDir(), t.TempDir()
	ctx := context.Background()
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	coll, err := New(root, state, discard).Open(ctx, "test")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := coll.Create(ctx, ""); err != nil {
		t.Fatalf("Create: %v", err)
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

	if err := coll.UpdateRequest(ctx, nil, "Echo", RequestPatch{SetTarget: true, Target: serverFromAddressTLS("api.example.com:8443", true)}); err != nil {
		t.Fatalf("UpdateRequest set target: %v", err)
	}
	if got := targetOf(t, coll); got == nil || got.GetAddress() != "api.example.com:8443" || got.GetTls() == nil {
		t.Fatalf("target after set = %+v, want api.example.com:8443 tls", got)
	}
	rf := &grpcviewstorev1.Request{}
	mustRead(t, filepath.Join(coll.Root(), treeDir, "echo", requestFileName), rf)
	if rf.GetTarget().GetAddress() != "api.example.com:8443" || !rf.GetTarget().GetTls() {
		t.Fatalf("request.json target = %+v, want api.example.com:8443 tls", rf.GetTarget())
	}

	body := `{"m":1}`
	if err := coll.UpdateRequest(ctx, nil, "Echo", RequestPatch{DraftBody: &body}); err != nil {
		t.Fatalf("UpdateRequest body only: %v", err)
	}
	if got := targetOf(t, coll); got == nil || got.GetAddress() != "api.example.com:8443" {
		t.Fatalf("target after unrelated patch = %+v, want unchanged", got)
	}

	reloaded, err := New(root, state, discard).Open(ctx, "test")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := targetOf(t, reloaded); got == nil || got.GetAddress() != "api.example.com:8443" {
		t.Fatalf("reloaded target = %+v, want api.example.com:8443", got)
	}

	if err := reloaded.UpdateRequest(ctx, nil, "Echo", RequestPatch{SetTarget: true, Target: nil}); err != nil {
		t.Fatalf("UpdateRequest clear target: %v", err)
	}
	if got := targetOf(t, reloaded); got != nil {
		t.Fatalf("target after clear = %+v, want nil", got)
	}
}

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

	chain, err := coll.FolderMetadataChain(ctx, []string{"a", "b"})
	if err != nil {
		t.Fatalf("FolderMetadataChain: %v", err)
	}
	if len(chain) != 2 || chain[0] != scriptA || chain[1] != scriptB {
		t.Fatalf("FolderMetadataChain(a,b) = %v, want [%q %q]", chain, scriptA, scriptB)
	}

	rootChain, err := coll.FolderMetadataChain(ctx, nil)
	if err != nil || len(rootChain) != 0 {
		t.Fatalf("FolderMetadataChain(root) = %v (err %v), want empty", rootChain, err)
	}

	if _, err := coll.FolderMetadataChain(ctx, []string{"a", "ghost"}); !errors.Is(err, ErrItemNotFound) {
		t.Fatalf("FolderMetadataChain missing path = %v, want ErrItemNotFound", err)
	}

	if err := coll.CreateRequest(ctx, []string{"a", "b"}, "Leaf", "s", "m"); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	if _, err := coll.FolderMetadataChain(ctx, []string{"a", "b", "Leaf"}); !errors.Is(err, ErrNotAFolder) {
		t.Fatalf("FolderMetadataChain through a request = %v, want ErrNotAFolder", err)
	}

	empty := ""
	if err := coll.UpdateFolder(ctx, nil, "a", FolderPatch{DraftMetadataScript: &empty}); err != nil {
		t.Fatalf("UpdateFolder clear: %v", err)
	}
	root = rootItems(t, coll, ctx)
	if got := childByName(root, "a").GetFolder().GetDraftMetadataScript(); got != "" {
		t.Errorf("folder a script after clear = %q, want empty", got)
	}

	if err := coll.UpdateFolder(ctx, []string{"a"}, "b", FolderPatch{}); err != nil {
		t.Fatalf("UpdateFolder no-op: %v", err)
	}
	root = rootItems(t, coll, ctx)
	bItem = childByName(childByName(root, "a").GetFolder().GetItems(), "b")
	if got := bItem.GetFolder().GetDraftMetadataScript(); got != scriptB {
		t.Errorf("folder a/b script after no-op patch = %q, want unchanged %q", got, scriptB)
	}

	if err := coll.UpdateFolder(ctx, nil, "ghost", FolderPatch{DraftMetadataScript: &scriptA}); !errors.Is(err, ErrItemNotFound) {
		t.Fatalf("UpdateFolder missing = %v, want ErrItemNotFound", err)
	}
	if err := coll.UpdateFolder(ctx, []string{"a", "b"}, "Leaf", FolderPatch{DraftMetadataScript: &scriptA}); !errors.Is(err, ErrNotAFolder) {
		t.Fatalf("UpdateFolder on a request = %v, want ErrNotAFolder", err)
	}
}

func TestUpdateFolderRename(t *testing.T) {
	coll, ctx := newTestCollection(t)
	if err := coll.CreateFolder(ctx, nil, "Users"); err != nil {
		t.Fatal(err)
	}
	if err := coll.CreateFolder(ctx, nil, "Admin"); err != nil {
		t.Fatal(err)
	}
	if err := coll.CreateRequest(ctx, nil, "Ping", "s", "m"); err != nil {
		t.Fatal(err)
	}
	if err := coll.CreateRequest(ctx, []string{"Users"}, "Get User", "s.S", "GetUser"); err != nil {
		t.Fatal(err)
	}
	if err := coll.CreateRequest(ctx, []string{"Users"}, "List Users", "s.S", "ListUsers"); err != nil {
		t.Fatal(err)
	}

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
	if got := names(renamed.GetFolder().GetItems()); len(got) != 2 || got[0] != "Get User" || got[1] != "List Users" {
		t.Errorf("children after rename = %v, want [Get User, List Users]", got)
	}
	body := `{"id":"1"}`
	if err := coll.UpdateRequest(ctx, []string{newName}, "Get User", RequestPatch{DraftBody: &body}); err != nil {
		t.Errorf("child unreachable under renamed folder: %v", err)
	}

	for _, collide := range []string{"Admin", "Ping"} {
		if err := coll.UpdateFolder(ctx, nil, newName, FolderPatch{Name: &collide}); !errors.Is(err, ErrAlreadyExists) {
			t.Errorf("rename onto %q = %v, want ErrAlreadyExists", collide, err)
		}
	}
	mustRead(t, filepath.Join(tree, "users", folderFileName), ff)
	if ff.GetMeta().GetName() != newName {
		t.Errorf("failed rename must not mutate meta.name, got %q", ff.GetMeta().GetName())
	}

	script := "export default () => ({ team: ['people'] })"
	if err := coll.UpdateFolder(ctx, nil, newName, FolderPatch{Name: &newName, DraftMetadataScript: &script}); err != nil {
		t.Fatalf("no-op rename + script patch: %v", err)
	}
	mustRead(t, filepath.Join(tree, "users", folderFileName), ff)
	if ff.GetMeta().GetName() != newName || ff.GetDraftMetadataScript() != script {
		t.Errorf("after no-op rename + script: name=%q script=%q", ff.GetMeta().GetName(), ff.GetDraftMetadataScript())
	}

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

func TestOpenRejectsTraversal(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for _, id := range []string{"../evil", "/etc", "foo/../../bar"} {
		if _, err := s.Open(ctx, id); !errors.Is(err, ErrInvalidCollectionID) {
			t.Errorf("Open(%q) = %v, want ErrInvalidCollectionID", id, err)
		}
	}

	entries, err := os.ReadDir(s.root)
	if err != nil {
		t.Fatalf("ReadDir workspace root: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("a rejected id must create nothing on disk, found %v", entries)
	}
}

func TestOpenDotIsTheWorkspaceRoot(t *testing.T) {
	s := newTestStore(t)
	coll, err := s.Open(context.Background(), ".")
	if err != nil {
		t.Fatalf("Open(.): %v", err)
	}
	if coll.Root() != s.root {
		t.Errorf("Open(.).Root() = %q, want the workspace root %q", coll.Root(), s.root)
	}
}

func TestOpenCaseFoldsHandleIdentity(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	lower, err := s.Open(ctx, "requests")
	if err != nil {
		t.Fatalf("Open(requests): %v", err)
	}
	upper, err := s.Open(ctx, "Requests")
	if err != nil {
		t.Fatalf("Open(Requests): %v", err)
	}
	if lower != upper {
		t.Errorf("Open(%q) and Open(%q) returned different *Collection handles for what is one directory on a case-insensitive filesystem", "requests", "Requests")
	}
}

func TestDisplayNameIsNeverTheID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const id = "services/payments/requests"
	coll, err := s.Open(ctx, id)
	if err != nil {
		t.Fatalf("Open(%q): %v", id, err)
	}
	if err := coll.Create(ctx, ""); err != nil {
		t.Fatalf("Create: %v", err)
	}

	col := &grpcviewstorev1.Collection{}
	mustRead(t, coll.collectionFilePath(), col)
	if col.GetName() != "requests" {
		t.Errorf("on-disk grpcview.json name = %q, want the base name %q (never the id %q)", col.GetName(), "requests", id)
	}

	ws, err := coll.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if ws.GetName() != "requests" {
		t.Errorf("Load(...).Name = %q, want %q", ws.GetName(), "requests")
	}
}

// TestLocalStateStaysOutOfCollectionDir exercises every kind of local state (the descriptor
// index, the descriptor blobs it points at, run history) and asserts none of it lands next to the
// committed manifest/tree — it must all be reachable only under the state root passed to
// New, which for a real workspace is wsroot.StateDir's directory, never the repo.
func TestLocalStateStaysOutOfCollectionDir(t *testing.T) {
	root, state := t.TempDir(), t.TempDir()
	s := New(root, state, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx := context.Background()

	coll, err := s.Open(ctx, "test")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := coll.Create(ctx, ""); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := coll.CreateRequest(ctx, nil, "Echo", "s", "m"); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	const sourceID = "reflection:localhost:1"
	if err := coll.PutDescriptorState(ctx, DescriptorState{
		Sources: []*grpcviewv1.DescriptorSource{{
			Id:     sourceID,
			Source: &grpcviewv1.DescriptorSource_Reflection{Reflection: &grpcviewv1.Server{Address: "localhost:1"}},
		}},
		Resolves: map[string]*grpcviewstorev1.ResolvedSource{
			sourceID: {Id: sourceID, DescriptorSet: fdsNamed("echo.proto")},
		},
	}); err != nil {
		t.Fatalf("PutDescriptorState: %v", err)
	}
	if err := coll.AppendHistory(ctx, nil, "Echo", &grpcviewv1.History{}, 0); err != nil {
		t.Fatalf("AppendHistory: %v", err)
	}

	entries, err := os.ReadDir(coll.Root())
	if err != nil {
		t.Fatalf("ReadDir collection root: %v", err)
	}
	want := map[string]bool{CollectionFileName: true, treeDir: true}
	for _, e := range entries {
		if !want[e.Name()] {
			t.Errorf("collection root has unexpected entry %q (local state must live outside it)", e.Name())
		}
	}

	if !strings.HasPrefix(coll.State(), state) {
		t.Errorf("collection state dir %q is not under the configured state root %q", coll.State(), state)
	}
	if _, err := os.Stat(coll.descriptorIndexPath()); err != nil {
		t.Errorf("descriptor index missing under the state root: %v", err)
	}
	if got := blobNames(t, coll.store); len(got) != 1 {
		t.Errorf("want the source's descriptor blob under the state root, got %v", got)
	}
	histPath, err := coll.historyFilePath(filepath.Join(coll.treeRoot(), "echo"))
	if err != nil {
		t.Fatalf("historyFilePath: %v", err)
	}
	if _, err := os.Stat(histPath); err != nil {
		t.Errorf("history file missing under the state root: %v", err)
	}
}
