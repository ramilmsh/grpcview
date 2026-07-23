package store

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	grpcviewstorev1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/store/v1"
	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

// loadScriptList loads the collection and returns its ordered wire scripts.
func loadScriptList(t *testing.T, coll *Collection, ctx context.Context) []*grpcviewv1.Script {
	t.Helper()
	ws, err := coll.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return ws.GetScripts()
}

func scriptByName(scripts []*grpcviewv1.Script, name string) *grpcviewv1.Script {
	for _, s := range scripts {
		if s.GetName() == name {
			return s
		}
	}
	return nil
}

func TestCreateListUpdateDeleteScript(t *testing.T) {
	coll, ctx := newTestCollection(t)

	// Create three scripts of assorted kinds.
	if err := coll.CreateScript(ctx, "uuid", grpcviewv1.ScriptKind_SCRIPT_KIND_GENERATOR); err != nil {
		t.Fatalf("CreateScript generator: %v", err)
	}
	if err := coll.CreateScript(ctx, "sign", grpcviewv1.ScriptKind_SCRIPT_KIND_MIDDLEWARE); err != nil {
		t.Fatalf("CreateScript middleware: %v", err)
	}
	if err := coll.CreateScript(ctx, "happy path", grpcviewv1.ScriptKind_SCRIPT_KIND_SCENARIO); err != nil {
		t.Fatalf("CreateScript scenario: %v", err)
	}

	// List (via Load): three scripts, in creation order, with the right kinds.
	scripts := loadScriptList(t, coll, ctx)
	if len(scripts) != 3 {
		t.Fatalf("scripts = %d, want 3 (%v)", len(scripts), scripts)
	}
	gotKind := map[string]grpcviewv1.ScriptKind{}
	for _, s := range scripts {
		gotKind[s.GetName()] = s.GetKind()
	}
	if gotKind["uuid"] != grpcviewv1.ScriptKind_SCRIPT_KIND_GENERATOR ||
		gotKind["sign"] != grpcviewv1.ScriptKind_SCRIPT_KIND_MIDDLEWARE ||
		gotKind["happy path"] != grpcviewv1.ScriptKind_SCRIPT_KIND_SCENARIO {
		t.Fatalf("kinds not persisted: %+v", gotKind)
	}

	// On-disk shape: slug dirs, meta.name, ordered scripts[], kind.
	col := &grpcviewstorev1.Collection{}
	mustRead(t, coll.collectionFilePath(), col)
	if len(col.GetScripts()) != 3 || col.GetScripts()[0] != "uuid" {
		t.Fatalf("manifest scripts = %v, want [uuid sign happy-path]", col.GetScripts())
	}
	sf := &grpcviewstorev1.Script{}
	mustRead(t, filepath.Join(coll.Root(), scriptsDir, "happy-path", scriptFileName), sf)
	if sf.GetMeta().GetName() != "happy path" || sf.GetKind() != grpcviewstorev1.ScriptKind_SCRIPT_KIND_SCENARIO {
		t.Fatalf("script.json = %+v, want name=happy path kind=scenario", sf)
	}

	// Update source (must not touch kind).
	src := `export default () => 42`
	if err := coll.UpdateScript(ctx, "uuid", ScriptPatch{Source: &src}); err != nil {
		t.Fatalf("UpdateScript source: %v", err)
	}
	if got := scriptByName(loadScriptList(t, coll, ctx), "uuid"); got == nil ||
		got.GetSource() != src || got.GetKind() != grpcviewv1.ScriptKind_SCRIPT_KIND_GENERATOR {
		t.Fatalf("source not persisted / kind changed: %+v", got)
	}

	// Rename: slug/dir stays stable, only meta.name changes, source survives.
	newName := "uuidv4"
	if err := coll.UpdateScript(ctx, "uuid", ScriptPatch{Name: &newName}); err != nil {
		t.Fatalf("UpdateScript rename: %v", err)
	}
	if _, err := os.Stat(filepath.Join(coll.Root(), scriptsDir, "uuid")); err != nil {
		t.Errorf("slug dir should be stable across rename: %v", err)
	}
	scripts = loadScriptList(t, coll, ctx)
	if scriptByName(scripts, "uuidv4") == nil || scriptByName(scripts, "uuid") != nil {
		t.Errorf("rename not reflected: %v", scripts)
	}
	if got := scriptByName(scripts, "uuidv4"); got.GetSource() != src {
		t.Errorf("rename dropped source: %+v", got)
	}

	// Rename collision fails with ErrAlreadyExists.
	collide := "sign"
	if err := coll.UpdateScript(ctx, "uuidv4", ScriptPatch{Name: &collide}); !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("rename collision = %v, want ErrAlreadyExists", err)
	}

	// Duplicate create fails.
	if err := coll.CreateScript(ctx, "sign", grpcviewv1.ScriptKind_SCRIPT_KIND_MIDDLEWARE); !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("duplicate create = %v, want ErrAlreadyExists", err)
	}

	// Delete removes the dir and the manifest entry.
	if err := coll.DeleteScript(ctx, "sign"); err != nil {
		t.Fatalf("DeleteScript: %v", err)
	}
	if _, err := os.Stat(filepath.Join(coll.Root(), scriptsDir, "sign")); !os.IsNotExist(err) {
		t.Errorf("deleted script dir should be gone, stat err = %v", err)
	}
	if scriptByName(loadScriptList(t, coll, ctx), "sign") != nil {
		t.Errorf("deleted script still listed")
	}
	// Deleting a missing script is a no-op.
	if err := coll.DeleteScript(ctx, "ghost"); err != nil {
		t.Errorf("deleting missing script should be a no-op, got %v", err)
	}

	// Update of a missing script reports ErrItemNotFound.
	if err := coll.UpdateScript(ctx, "ghost", ScriptPatch{Source: &src}); !errors.Is(err, ErrItemNotFound) {
		t.Errorf("update missing = %v, want ErrItemNotFound", err)
	}
}

// TestScriptPersistRoundTrip confirms scripts survive a fresh reload from a brand-new
// Store over the same directory (no in-memory caches shared), in order.
func TestScriptPersistRoundTrip(t *testing.T) {
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
	for _, n := range []string{"Alpha", "Bravo", "Charlie"} {
		if err := coll.CreateScript(ctx, n, grpcviewv1.ScriptKind_SCRIPT_KIND_GENERATOR); err != nil {
			t.Fatalf("CreateScript %s: %v", n, err)
		}
	}
	src := `export default (x) => x + 1`
	if err := coll.UpdateScript(ctx, "Bravo", ScriptPatch{Source: &src}); err != nil {
		t.Fatalf("UpdateScript: %v", err)
	}

	reloaded, err := New(base, discard).Open(ctx, "test")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	scripts := loadScriptList(t, reloaded, ctx)
	got := make([]string, len(scripts))
	for i, s := range scripts {
		got[i] = s.GetName()
	}
	want := []string{"Alpha", "Bravo", "Charlie"}
	if len(got) != len(want) {
		t.Fatalf("reloaded order = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("reloaded order = %v, want %v", got, want)
		}
	}
	if b := scriptByName(scripts, "Bravo"); b.GetSource() != src {
		t.Errorf("reloaded source lost: %+v", b)
	}
}
