package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

func loadScriptList(t *testing.T, coll *Collection, ctx context.Context) []*grpcviewv1.Script {
	t.Helper()
	ws, err := coll.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return ws.GetScripts()
}

func scriptByPath(scripts []*grpcviewv1.Script, path string) *grpcviewv1.Script {
	for _, s := range scripts {
		if s.GetPath() == path {
			return s
		}
	}
	return nil
}

func TestCreateListUpdateDeleteScript(t *testing.T) {
	coll, ctx := newTestCollection(t)

	if err := coll.CreateScript(ctx, "scripts/uuid.ts"); err != nil {
		t.Fatalf("CreateScript: %v", err)
	}
	if err := coll.CreateScript(ctx, "scripts/http/retry.ts"); err != nil {
		t.Fatalf("CreateScript nested: %v", err)
	}

	scripts := loadScriptList(t, coll, ctx)
	if len(scripts) != 2 {
		t.Fatalf("scripts = %d, want 2 (%v)", len(scripts), scripts)
	}
	// Sorted by path.
	if scripts[0].GetPath() != "scripts/http/retry.ts" || scripts[1].GetPath() != "scripts/uuid.ts" {
		t.Fatalf("scripts order = %v", scripts)
	}
	if scripts[0].GetSource() != "" {
		t.Errorf("new script should be empty, got %q", scripts[0].GetSource())
	}

	data, err := os.ReadFile(filepath.Join(coll.Root(), scriptsDir, "uuid.ts"))
	if err != nil {
		t.Fatalf("read uuid.ts on disk: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("uuid.ts on disk should be empty, got %q", data)
	}
	if _, err := os.Stat(filepath.Join(coll.Root(), scriptsDir, "http", "retry.ts")); err != nil {
		t.Fatalf("nested script should exist on disk: %v", err)
	}

	src := `export default () => 42`
	if err := coll.UpdateScript(ctx, "scripts/uuid.ts", ScriptPatch{Source: &src}); err != nil {
		t.Fatalf("UpdateScript source: %v", err)
	}
	if got := scriptByPath(loadScriptList(t, coll, ctx), "scripts/uuid.ts"); got == nil || got.GetSource() != src {
		t.Fatalf("source not persisted: %+v", got)
	}

	newPath := "scripts/uuidv4.ts"
	if err := coll.UpdateScript(ctx, "scripts/uuid.ts", ScriptPatch{NewPath: &newPath}); err != nil {
		t.Fatalf("UpdateScript rename: %v", err)
	}
	if _, err := os.Stat(filepath.Join(coll.Root(), scriptsDir, "uuid.ts")); !os.IsNotExist(err) {
		t.Errorf("old path should be gone after rename, stat err = %v", err)
	}
	scripts = loadScriptList(t, coll, ctx)
	if scriptByPath(scripts, "scripts/uuidv4.ts") == nil || scriptByPath(scripts, "scripts/uuid.ts") != nil {
		t.Errorf("rename not reflected: %v", scripts)
	}
	if got := scriptByPath(scripts, "scripts/uuidv4.ts"); got.GetSource() != src {
		t.Errorf("rename dropped source: %+v", got)
	}

	collide := "scripts/http/retry.ts"
	if err := coll.UpdateScript(ctx, "scripts/uuidv4.ts", ScriptPatch{NewPath: &collide}); !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("rename collision = %v, want ErrAlreadyExists", err)
	}

	if err := coll.CreateScript(ctx, "scripts/http/retry.ts"); !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("duplicate create = %v, want ErrAlreadyExists", err)
	}

	if err := coll.DeleteScript(ctx, "scripts/http/retry.ts"); err != nil {
		t.Fatalf("DeleteScript: %v", err)
	}
	if _, err := os.Stat(filepath.Join(coll.Root(), scriptsDir, "http")); !os.IsNotExist(err) {
		t.Errorf("emptied parent dir should be pruned, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(coll.Root(), scriptsDir)); err != nil {
		t.Errorf("scripts/ itself should survive pruning: %v", err)
	}
	if scriptByPath(loadScriptList(t, coll, ctx), "scripts/http/retry.ts") != nil {
		t.Errorf("deleted script still listed")
	}
	if err := coll.DeleteScript(ctx, "scripts/ghost.ts"); err != nil {
		t.Errorf("deleting missing script should be a no-op, got %v", err)
	}

	if err := coll.UpdateScript(ctx, "scripts/ghost.ts", ScriptPatch{Source: &src}); !errors.Is(err, ErrItemNotFound) {
		t.Errorf("update missing = %v, want ErrItemNotFound", err)
	}
}

func TestReadScriptsSkipsDotfilesAndNonTS(t *testing.T) {
	coll, ctx := newTestCollection(t)

	if err := coll.CreateScript(ctx, "scripts/keep.ts"); err != nil {
		t.Fatalf("CreateScript: %v", err)
	}
	if err := os.WriteFile(filepath.Join(coll.Root(), scriptsDir, "notes.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write notes.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(coll.Root(), scriptsDir, ".hidden.ts"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write .hidden.ts: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(coll.Root(), scriptsDir, ".git", "sub"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(coll.Root(), scriptsDir, ".git", "sub", "buried.ts"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write buried.ts: %v", err)
	}

	scripts := loadScriptList(t, coll, ctx)
	if len(scripts) != 1 || scripts[0].GetPath() != "scripts/keep.ts" {
		t.Fatalf("scripts = %v, want only scripts/keep.ts", scripts)
	}
}

func TestReadScriptsMissingDirIsEmpty(t *testing.T) {
	coll, ctx := newTestCollection(t)

	scripts := loadScriptList(t, coll, ctx)
	if scripts != nil {
		t.Fatalf("scripts = %v, want nil with no scripts/ dir", scripts)
	}
}

func TestScriptPathValidation(t *testing.T) {
	coll, ctx := newTestCollection(t)

	cases := []string{
		"/abs/scripts/x.ts",
		"",
		"scripts/../x.ts",
		"scripts/x",
		"lib/x.ts",
		"../scripts/x.ts",
	}
	for _, p := range cases {
		if err := coll.CreateScript(ctx, p); err == nil {
			t.Errorf("CreateScript(%q) = nil error, want rejection", p)
		}
		if err := coll.UpdateScript(ctx, p, ScriptPatch{}); err == nil {
			t.Errorf("UpdateScript(%q) = nil error, want rejection", p)
		}
		if err := coll.DeleteScript(ctx, p); err == nil {
			t.Errorf("DeleteScript(%q) = nil error, want rejection", p)
		}
	}
}

func TestScriptPersistRoundTrip(t *testing.T) {
	coll, ctx := newTestCollection(t)

	for _, p := range []string{"scripts/alpha.ts", "scripts/bravo.ts", "scripts/charlie.ts"} {
		if err := coll.CreateScript(ctx, p); err != nil {
			t.Fatalf("CreateScript %s: %v", p, err)
		}
	}
	src := `export default (x) => x + 1`
	if err := coll.UpdateScript(ctx, "scripts/bravo.ts", ScriptPatch{Source: &src}); err != nil {
		t.Fatalf("UpdateScript: %v", err)
	}

	reloaded, err := New(coll.store.Root(), coll.store.stateRoot, coll.logger).Open(ctx, "test")
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	scripts := loadScriptList(t, reloaded, ctx)
	got := make([]string, len(scripts))
	for i, s := range scripts {
		got[i] = s.GetPath()
	}
	want := []string{"scripts/alpha.ts", "scripts/bravo.ts", "scripts/charlie.ts"}
	if len(got) != len(want) {
		t.Fatalf("reloaded paths = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("reloaded paths = %v, want %v", got, want)
		}
	}
	if b := scriptByPath(scripts, "scripts/bravo.ts"); b.GetSource() != src {
		t.Errorf("reloaded source lost: %+v", b)
	}
}
