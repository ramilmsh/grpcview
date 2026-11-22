package store

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestHidden(t *testing.T) {
	for name, want := range map[string]bool{
		".env":          true,
		".npmrc":        true,
		".tmp-123":      true,
		".git":          true,
		".gitignore":    true,
		".grpcview":     false,
		"node_modules":  true,
		"bazel-out":     true,
		"bazel-bin":     true,
		"bazel":         false,
		"src":           false,
		"body.ts":       false,
		"grpcview.json": false,
	} {
		if got := Hidden(name); got != want {
			t.Errorf("Hidden(%q) = %v, want %v", name, got, want)
		}
	}
}

func touch(t *testing.T, root string, rels ...string) {
	t.Helper()
	for _, rel := range rels {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

type resolveCase struct {
	path, coll string
	kind       PathKind
	slugs      string // "/"-joined; "" is nil
	file       string
}

func assertResolve(t *testing.T, s *Store, cases []resolveCase) {
	t.Helper()
	for _, c := range cases {
		got, err := s.ResolvePath(c.path)
		if err != nil {
			t.Errorf("ResolvePath(%q): %v", c.path, err)
			continue
		}
		var want []string
		if c.slugs != "" {
			want = strings.Split(c.slugs, "/")
		}
		if got.Collection != c.coll || got.Kind != c.kind || !slices.Equal(got.SlugPath, want) || got.File != c.file {
			t.Errorf("ResolvePath(%q) = {%q %v %q %q}, want {%q %v %q %q}",
				c.path, got.Collection, got.Kind, got.SlugPath, got.File, c.coll, c.kind, want, c.file)
		}
	}
}

// Mirrors example/ (bazelignored, so not readable from a test), plus the classes it lacks.
func TestResolvePathExample(t *testing.T) {
	s := newTestStore(t)
	root := s.Root()
	writeWorkspaceManifest(t, root)
	writeCollectionAt(t, root, "example", "Example")
	touch(t, root,
		"example/README.md",
		"example/scripts/ids.ts",
		"example/scripts/.hidden.ts",
		"example/tree/folder.json",
		"example/tree/metadata.ts",
		"example/tree/listcollections/request.json",
		"example/tree/listcollections/body.ts",
		"example/tree/listcollections/metadata.ts",
		"example/tree/listcollections/nested/request.json",
		"example/tree/workspace/folder.json",
		"example/tree/workspace/describemethod-json/request.json",
		"example/tree/workspace/streaming/folder.json",
		"example/tree/workspace/streaming/invokestreaming/request.json",
		"example/tree/orphan/body.ts",
		"example/descriptors/reflection.json",
		"example/.grpcview/types/grpcview.d.ts",
		"libs/common.ts",
	)

	const ex = "example"
	assertResolve(t, s, []resolveCase{
		{"/", "", PathOther, "", ""},
		{"/grpcview.work.json", "", PathManifest, "", "grpcview.work.json"},
		{"/.grpcview/modules.json", "", PathMount, "", ".grpcview/modules.json"},
		{"/libs/common.ts", "", PathOther, "", "libs/common.ts"},
		{"/example", ex, PathOther, "", ""},
		{"/example/", ex, PathOther, "", ""},
		{"/example/grpcview.json", ex, PathManifest, "", "grpcview.json"},
		{"/example/README.md", ex, PathOther, "", "README.md"},
		{"/example/.grpcview", ex, PathMount, "", ".grpcview"},
		{"/example/.grpcview/types/grpcview.d.ts", ex, PathMount, "", ".grpcview/types/grpcview.d.ts"},
		{"/example/.grpcview/proto/a/b.proto", ex, PathMount, "", ".grpcview/proto/a/b.proto"},
		{"/example/descriptors/reflection.json", ex, PathOther, "", "descriptors/reflection.json"},
		{"/example/scripts", ex, PathOther, "", "scripts"},
		{"/example/scripts/ids.ts", ex, PathScript, "", "scripts/ids.ts"},
		{"/example/scripts/new/x.ts", ex, PathScript, "", "scripts/new/x.ts"},
		{"/example/scripts/.hidden.ts", ex, PathOther, "", "scripts/.hidden.ts"},
		{"/example/scripts/notes.md", ex, PathOther, "", "scripts/notes.md"},
		{"/example/tree", ex, PathOther, "", "tree"},
		{"/example/tree/folder.json", ex, PathOther, "", "tree/folder.json"},
		{"/example/tree/metadata.ts", ex, PathOther, "", "tree/metadata.ts"},
		{"/example/tree/listcollections", ex, PathOther, "listcollections", "tree/listcollections"},
		{"/example/tree/listcollections/request.json", ex, PathManifest, "listcollections", "tree/listcollections/request.json"},
		{"/example/tree/listcollections/body.ts", ex, PathRequestBody, "listcollections", "tree/listcollections/body.ts"},
		{"/example/tree/listcollections/metadata.ts", ex, PathRequestMetadata, "listcollections", "tree/listcollections/metadata.ts"},
		{"/example/tree/listcollections/nested/request.json", ex, PathOther, "", "tree/listcollections/nested/request.json"},
		{"/example/tree/workspace", ex, PathOther, "workspace", "tree/workspace"},
		{"/example/tree/workspace/folder.json", ex, PathManifest, "workspace", "tree/workspace/folder.json"},
		{"/example/tree/workspace/metadata.ts", ex, PathFolderMetadata, "workspace", "tree/workspace/metadata.ts"},
		{"/example/tree/workspace/body.ts", ex, PathOther, "workspace", "tree/workspace/body.ts"},
		{"/example/tree/workspace/describemethod-json/metadata.ts", ex, PathRequestMetadata, "workspace/describemethod-json", "tree/workspace/describemethod-json/metadata.ts"},
		{"/example/tree/workspace/streaming/folder.json", ex, PathManifest, "workspace/streaming", "tree/workspace/streaming/folder.json"},
		{"/example/tree/workspace/streaming/metadata.ts", ex, PathFolderMetadata, "workspace/streaming", "tree/workspace/streaming/metadata.ts"},
		{"/example/tree/workspace/streaming/invokestreaming/body.ts", ex, PathRequestBody, "workspace/streaming/invokestreaming", "tree/workspace/streaming/invokestreaming/body.ts"},
		{"/example/tree/orphan", ex, PathOther, "", "tree/orphan"},
		{"/example/tree/orphan/body.ts", ex, PathOther, "", "tree/orphan/body.ts"},
	})

	for _, bad := range []string{"example/grpcview.json", "", "/example/../etc/passwd", "/.."} {
		if _, err := s.ResolvePath(bad); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("ResolvePath(%q) err = %v, want ErrInvalidPath", bad, err)
		}
	}
}

func TestResolvePathDotCollection(t *testing.T) {
	s := newTestStore(t)
	root := s.Root()
	writeCollectionAt(t, root, ".", "Root")
	touch(t, root, "tree/get/request.json", "tree/users/folder.json")

	assertResolve(t, s, []resolveCase{
		{"/", ".", PathOther, "", ""},
		{"/grpcview.json", ".", PathManifest, "", "grpcview.json"},
		{"/grpcview.work.json", ".", PathManifest, "", "grpcview.work.json"},
		{"/.grpcview/modules.json", ".", PathMount, "", ".grpcview/modules.json"},
		{"/.grpcview/types/grpcview.d.ts", ".", PathMount, "", ".grpcview/types/grpcview.d.ts"},
		{"/scripts/auth.ts", ".", PathScript, "", "scripts/auth.ts"},
		{"/tree/get/body.ts", ".", PathRequestBody, "get", "tree/get/body.ts"},
		{"/tree/users/metadata.ts", ".", PathFolderMetadata, "users", "tree/users/metadata.ts"},
		{"/libs/common.ts", ".", PathOther, "", "libs/common.ts"},
	})
}

func TestResolvePathPinnedCollections(t *testing.T) {
	s := newTestStore(t)
	root := s.Root()
	writeWorkspaceManifest(t, root, ".", "svc/a", "missing")
	writeCollectionAt(t, root, ".", "Root")
	writeCollectionAt(t, root, "svc/a", "A")
	writeCollectionAt(t, root, "svc/b", "B") // on disk, not pinned

	assertResolve(t, s, []resolveCase{
		{"/svc/a/scripts/x.ts", "svc/a", PathScript, "", "scripts/x.ts"},
		{"/svc/a/grpcview.json", "svc/a", PathManifest, "", "grpcview.json"},
		{"/svc/b/grpcview.json", ".", PathOther, "", "svc/b/grpcview.json"},
		{"/svc/b/scripts/x.ts", ".", PathOther, "", "svc/b/scripts/x.ts"},
		{"/missing/grpcview.json", ".", PathOther, "", "missing/grpcview.json"},
		{"/scripts/x.ts", ".", PathScript, "", "scripts/x.ts"},
	})

	// Without the "." pin, unclaimed paths fall outside every collection.
	writeWorkspaceManifest(t, root, "svc/a")
	assertResolve(t, s, []resolveCase{
		{"/svc/a/scripts/x.ts", "svc/a", PathScript, "", "scripts/x.ts"},
		{"/svc/b/scripts/x.ts", "", PathOther, "", "svc/b/scripts/x.ts"},
		{"/grpcview.json", "", PathOther, "", "grpcview.json"},
		{"/scripts/x.ts", "", PathOther, "", "scripts/x.ts"},
	})
}

func TestWriteItemFile(t *testing.T) {
	coll, ctx := newTestCollection(t)
	if err := coll.CreateFolder(ctx, nil, "Users"); err != nil {
		t.Fatal(err)
	}
	if err := coll.CreateRequest(ctx, []string{"Users"}, "Get", "svc", "M"); err != nil {
		t.Fatal(err)
	}
	// A rename leaves the slug alone: the writer addresses by slug, not display name.
	name := "Renamed"
	if err := coll.UpdateRequest(ctx, []string{"Users"}, "Get", RequestPatch{Name: &name}); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(coll.treeRoot(), "users", "get", requestFileName)
	before, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}

	defer coll.lockWrite()()
	for _, w := range []struct {
		slugs, name string
		wantErr     error
	}{
		{"users/get", bodyFileName, nil},
		{"users/get", metadataFileName, nil},
		{"users", metadataFileName, nil},
		{"users", bodyFileName, errNotItemFile},
		{"users/get", requestFileName, errNotItemFile},
		{"users/nope", bodyFileName, ErrItemNotFound},
		{"../users", bodyFileName, ErrItemNotFound},
	} {
		err := coll.writeItemFile(strings.Split(w.slugs, "/"), w.name, "x")
		if !errors.Is(err, w.wantErr) {
			t.Errorf("writeItemFile(%s, %s) err = %v, want %v", w.slugs, w.name, err, w.wantErr)
			continue
		}
		if err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(coll.treeRoot(), filepath.FromSlash(w.slugs), w.name))
		if err != nil || string(data) != "x\n" {
			t.Errorf("%s/%s = %q, %v; want %q", w.slugs, w.name, data, err, "x\n")
		}
	}
	if after, _ := os.ReadFile(manifest); string(after) != string(before) {
		t.Errorf("request manifest changed:\n%s\nwant\n%s", after, before)
	}
}
