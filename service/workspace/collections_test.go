package workspace

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/store"
)

func newWorkspaceAt(t *testing.T, root string) Workspace {
	t.Helper()
	return Workspace{
		store: store.New(root, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil))),
		defs:  newDefinitionsCache(),
	}
}

func listCollections(t *testing.T, w Workspace, ctx context.Context) *grpcviewv1.ListCollectionsResponse {
	t.Helper()
	resp, err := w.ListCollections(ctx, connect.NewRequest(&grpcviewv1.ListCollectionsRequest{}))
	if err != nil {
		t.Fatalf("ListCollections: %v", err)
	}
	return resp.Msg
}

func TestListCollectionsEmptyWorkspace(t *testing.T) {
	root := t.TempDir()
	msg := listCollections(t, newWorkspaceAt(t, root), context.Background())

	if len(msg.GetCollections()) != 0 {
		t.Errorf("collections = %v, want none", msg.GetCollections())
	}
	if msg.GetRoot() != root {
		t.Errorf("root = %q, want %q", msg.GetRoot(), root)
	}
}

func TestListCollectionsAfterCreate(t *testing.T) {
	w := newWorkspaceAt(t, t.TempDir())
	ctx := context.Background()

	if _, err := w.CreateCollection(ctx, connect.NewRequest(&grpcviewv1.CreateCollectionRequest{
		Collection: "services/payments/requests",
		Name:       "Payments",
	})); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	got := listCollections(t, w, ctx).GetCollections()
	if len(got) != 1 {
		t.Fatalf("collections = %v, want exactly one", got)
	}
	if id := got[0].GetId(); id != "services/payments/requests" {
		t.Errorf("id = %q, want the workspace-relative path", id)
	}
	if name := got[0].GetName(); name != "Payments" {
		t.Errorf("name = %q, want the display name %q", name, "Payments")
	}
	if got[0].GetSourceCount() != 0 || got[0].GetError() != "" {
		t.Errorf("fresh collection = %+v, want no sources and no error", got[0])
	}
}

func TestUpdateCollectionRenamesTheDisplayNameOnly(t *testing.T) {
	w := newWorkspaceAt(t, t.TempDir())
	ctx := context.Background()

	if _, err := w.CreateCollection(ctx, connect.NewRequest(&grpcviewv1.CreateCollectionRequest{
		Collection: "requests",
		Name:       "Payments",
	})); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	name := "Billing"
	resp, err := w.UpdateCollection(ctx, connect.NewRequest(&grpcviewv1.UpdateCollectionRequest{
		Collection: "requests",
		Name:       &name,
	}))
	if err != nil {
		t.Fatalf("UpdateCollection: %v", err)
	}
	if got := resp.Msg.GetCollection().GetName(); got != name {
		t.Errorf("response name = %q, want %q", got, name)
	}
	if got := resp.Msg.GetCollection().GetId(); got != "requests" {
		t.Errorf("response id = %q, want the unchanged id %q", got, "requests")
	}

	got := listCollections(t, w, ctx).GetCollections()
	if len(got) != 1 {
		t.Fatalf("collections = %v, want exactly one", got)
	}
	if got[0].GetName() != name || got[0].GetId() != "requests" {
		t.Errorf("listed collection = %+v, want name %q at id %q", got[0], name, "requests")
	}
}

func TestUpdateCollectionMovesTheCollection(t *testing.T) {
	w := newWorkspaceAt(t, t.TempDir())
	ctx := context.Background()

	if _, err := w.CreateCollection(ctx, connect.NewRequest(&grpcviewv1.CreateCollectionRequest{
		Collection: "requests",
		Name:       "Payments",
	})); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	newID := "services/payments/requests"
	resp, err := w.UpdateCollection(ctx, connect.NewRequest(&grpcviewv1.UpdateCollectionRequest{
		Collection:    "requests",
		NewCollection: &newID,
	}))
	if err != nil {
		t.Fatalf("UpdateCollection: %v", err)
	}
	if got := resp.Msg.GetCollection().GetId(); got != newID {
		t.Errorf("response id = %q, want %q", got, newID)
	}
	if got := resp.Msg.GetCollection().GetName(); got != "Payments" {
		t.Errorf("response name = %q, want the untouched display name %q", got, "Payments")
	}

	if _, err := w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{Collection: newID})); err != nil {
		t.Errorf("Get(%q) after the move: %v", newID, err)
	}
	_, err = w.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{Collection: "requests"}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("Get of the vacated id = %v, want NotFound", err)
	}

	got := listCollections(t, w, ctx).GetCollections()
	if len(got) != 1 || got[0].GetId() != newID {
		t.Errorf("collections after the move = %v, want only %q", got, newID)
	}
}

func TestListCollectionsSeesCreate(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "services", "payments"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	w := newWorkspaceAt(t, root)
	ctx := context.Background()

	if got := listCollections(t, w, ctx).GetCollections(); len(got) != 0 {
		t.Fatalf("collections before create = %v, want none", got)
	}

	if _, err := w.CreateCollection(ctx, connect.NewRequest(&grpcviewv1.CreateCollectionRequest{
		Collection: "services/payments/requests",
	})); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	got := listCollections(t, w, ctx).GetCollections()
	if len(got) != 1 || got[0].GetId() != "services/payments/requests" {
		t.Fatalf("collections after create = %v, want the new collection", got)
	}
}
