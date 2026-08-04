package workspace

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/store"
)

func newWorkspaceAt(t *testing.T, root string) Workspace {
	t.Helper()
	return Workspace{
		store: store.New(root, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil))),
		defs:  newDefinitionsCache(),
	}
}

func listCollections(t *testing.T, w Workspace, ctx context.Context, refresh bool) *grpcviewv1.ListCollectionsResponse {
	t.Helper()
	resp, err := w.ListCollections(ctx, connect.NewRequest(&grpcviewv1.ListCollectionsRequest{Refresh: refresh}))
	if err != nil {
		t.Fatalf("ListCollections(refresh=%v): %v", refresh, err)
	}
	return resp.Msg
}

// TestListCollectionsEmptyWorkspace: a repo nobody has run grpcview in yet is a legitimate
// state, not an error — it is what the UI's empty state renders from.
func TestListCollectionsEmptyWorkspace(t *testing.T) {
	root := t.TempDir()
	msg := listCollections(t, newWorkspaceAt(t, root), context.Background(), false)

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

	got := listCollections(t, w, ctx, false).GetCollections()
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

// TestListCollectionsSeesCreateWithoutRefresh is the test for the invalidation in
// CreateCollection. The parent directory exists before the first listing so that creating the
// collection under it cannot change the ROOT's mtime — which is the index's cache key, and
// therefore the one thing that would otherwise mask a missing InvalidateList.
func TestListCollectionsSeesCreateWithoutRefresh(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "services", "payments"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	w := newWorkspaceAt(t, root)
	ctx := context.Background()

	if got := listCollections(t, w, ctx, false).GetCollections(); len(got) != 0 {
		t.Fatalf("collections before create = %v, want none", got)
	}

	if _, err := w.CreateCollection(ctx, connect.NewRequest(&grpcviewv1.CreateCollectionRequest{
		Collection: "services/payments/requests",
	})); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}

	got := listCollections(t, w, ctx, false).GetCollections()
	if len(got) != 1 || got[0].GetId() != "services/payments/requests" {
		t.Fatalf("collections after create = %v, want the new collection without an explicit refresh", got)
	}
}
