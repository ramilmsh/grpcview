package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveWorkspaceFileAccepts(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "protos", "image.binpb"), []byte("x"))
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks(root): %v", err)
	}

	t.Run("root-relative", func(t *testing.T) {
		real, rel, err := resolveWorkspaceFile(root, filepath.Join("protos", "image.binpb"))
		if err != nil {
			t.Fatalf("resolveWorkspaceFile: %v", err)
		}
		if rel != "protos/image.binpb" {
			t.Errorf("rel = %q, want %q", rel, "protos/image.binpb")
		}
		if want := filepath.Join(realRoot, "protos", "image.binpb"); real != want {
			t.Errorf("real = %q, want the resolved %q", real, want)
		}
	})

	t.Run("absolute inside root", func(t *testing.T) {
		_, rel, err := resolveWorkspaceFile(root, filepath.Join(root, "protos", "image.binpb"))
		if err != nil {
			t.Fatalf("resolveWorkspaceFile: %v", err)
		}
		if rel != "protos/image.binpb" {
			t.Errorf("rel = %q, want %q", rel, "protos/image.binpb")
		}
	})

	t.Run("link inside root resolves to its target", func(t *testing.T) {
		link := filepath.Join(root, "link.binpb")
		if err := os.Symlink(filepath.Join(root, "protos", "image.binpb"), link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		real, rel, err := resolveWorkspaceFile(root, "link.binpb")
		if err != nil {
			t.Fatalf("resolveWorkspaceFile: %v", err)
		}
		if want := filepath.Join(realRoot, "protos", "image.binpb"); real != want {
			t.Errorf("real = %q, want the link's target %q", real, want)
		}
		if rel != "link.binpb" {
			t.Errorf("rel = %q, want the recorded recipe %q", rel, "link.binpb")
		}
	})

	t.Run("symlinked root", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), "link-to-root")
		if err := os.Symlink(root, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if _, rel, err := resolveWorkspaceFile(link, "protos/image.binpb"); err != nil || rel != "protos/image.binpb" {
			t.Errorf("resolveWorkspaceFile through a symlinked root = %q, %v; want the file accepted", rel, err)
		}
	})
}

func TestResolveWorkspaceFileRejects(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "in.binpb"), []byte("x"))

	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.binpb")
	writeFile(t, secret, []byte("x"))

	escape := filepath.Join(root, "escape.binpb")
	if err := os.Symlink(secret, escape); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(root, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct{ name, path string }{
		{"dot-dot escape", filepath.Join("..", filepath.Base(outside), "secret.binpb")},
		{"deep dot-dot escape", "a/b/../../../etc/passwd"},
		{"absolute outside", secret},
		{"symlink escape", "escape.binpb"},
		{"directory", "adir"},
		{"root itself", "."},
		{"empty", ""},
		{"missing", "nope.binpb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			abs, rel, err := resolveWorkspaceFile(root, tc.path)
			if err == nil {
				t.Fatalf("resolveWorkspaceFile(%q) = %q, %q, nil; want an error", tc.path, abs, rel)
			}
		})
	}

	if _, rel, err := resolveWorkspaceFile(root, "in.binpb"); err != nil || rel != "in.binpb" {
		t.Errorf("resolveWorkspaceFile(in.binpb) = %q, %v; want it accepted", rel, err)
	}
}
