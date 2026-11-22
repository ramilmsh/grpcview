package wsroot

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfine(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "ws")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{filepath.Join(root, "in"), outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{filepath.Join(root, "in", "a.ts"), filepath.Join(outside, "secret.ts")} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	links := map[string]string{
		filepath.Join(root, "escape.ts"):  filepath.Join(outside, "secret.ts"),
		filepath.Join(root, "escdir"):     outside,
		filepath.Join(root, "inlink.ts"):  filepath.Join(root, "in", "a.ts"),
		filepath.Join(root, "dangling"):   filepath.Join(outside, "later"),
		filepath.Join(base, "link-to-ws"): root,
	}
	for link, target := range links {
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("accepts", func(t *testing.T) {
		cases := []struct{ root, p, want string }{
			{root, "in/a.ts", filepath.Join(realRoot, "in", "a.ts")},
			{root, filepath.Join(root, "in", "a.ts"), filepath.Join(realRoot, "in", "a.ts")},
			{root, "inlink.ts", filepath.Join(realRoot, "in", "a.ts")},
			{root, ".", realRoot},
			{root, "missing/deeper/new.ts", filepath.Join(realRoot, "missing", "deeper", "new.ts")},
			{filepath.Join(root, "absent"), "x/new.ts", filepath.Join(realRoot, "absent", "x", "new.ts")},
			{filepath.Join(base, "link-to-ws"), "in/a.ts", filepath.Join(realRoot, "in", "a.ts")},
			{filepath.Join(base, "link-to-ws"), filepath.Join(base, "link-to-ws", "new.ts"), filepath.Join(realRoot, "new.ts")},
		}
		for _, tc := range cases {
			got, err := Confine(tc.root, tc.p)
			if err != nil || got != tc.want {
				t.Errorf("Confine(%q, %q) = %q, %v; want %q", tc.root, tc.p, got, err, tc.want)
			}
		}
	})

	t.Run("rejects", func(t *testing.T) {
		cases := []struct {
			name, p string
			escape  bool
		}{
			{"dot-dot", "../outside/secret.ts", true},
			{"deep dot-dot", "in/../../outside/secret.ts", true},
			{"absolute outside", filepath.Join(outside, "secret.ts"), true},
			{"symlink escape on read", "escape.ts", true},
			{"create through symlinked dir", "escdir/new/x.ts", true},
			{"dangling link", "dangling", false},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got, err := Confine(root, tc.p)
				if err == nil {
					t.Fatalf("Confine(%q) = %q, nil; want an error", tc.p, got)
				}
				if tc.escape && !errors.Is(err, ErrEscapes) {
					t.Errorf("Confine(%q) = %v, want ErrEscapes", tc.p, err)
				}
				if strings.Contains(err.Error(), base) || strings.Contains(err.Error(), realRoot) {
					t.Errorf("Confine(%q) error %q names a host path", tc.p, err)
				}
			})
		}
	})
}

func TestWithin(t *testing.T) {
	for _, tc := range []struct {
		dir, p string
		want   bool
	}{
		{"/a", "/a", true},
		{"/a", "/a/b", true},
		{"/a", "/ab", false},
		{"/a", "/a/../b", false},
		{"/a", "/", false},
		{"/a", "/a/..b", true},
	} {
		if got := Within(tc.dir, tc.p); got != tc.want {
			t.Errorf("Within(%q, %q) = %v, want %v", tc.dir, tc.p, got, tc.want)
		}
	}
}

func TestCanonical(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	for in, want := range map[string]string{
		"":                                  "",
		link:                                realRoot,
		filepath.Join(link, "missing", "x"): filepath.Join(realRoot, "missing", "x"),
		filepath.Join(link, "a", "..", "b"): filepath.Join(realRoot, "b"),
	} {
		if got := Canonical(in); got != want {
			t.Errorf("Canonical(%q) = %q, want %q", in, got, want)
		}
	}
}
