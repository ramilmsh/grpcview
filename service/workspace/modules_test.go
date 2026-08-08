package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

func writeModuleFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func listWorkspaceModules(t *testing.T, root string) []*grpcviewv1.WorkspaceModule {
	t.Helper()
	w := Workspace{root: root}
	resp, err := w.ListWorkspaceModules(context.Background(), connect.NewRequest(&grpcviewv1.ListWorkspaceModulesRequest{}))
	if err != nil {
		t.Fatalf("ListWorkspaceModules: %v", err)
	}
	return resp.Msg.GetModules()
}

func modulePaths(modules []*grpcviewv1.WorkspaceModule) []string {
	out := make([]string, len(modules))
	for i, m := range modules {
		out[i] = m.GetPath()
	}
	return out
}

func TestListWorkspaceModulesNestingAndSort(t *testing.T) {
	root := t.TempDir()
	writeModuleFile(t, root, "b/second.ts", "export const b = 2")
	writeModuleFile(t, root, "a.ts", "export const a = 1")
	writeModuleFile(t, root, "b/nested/third.ts", "export const c = 3")

	modules := listWorkspaceModules(t, root)
	got := modulePaths(modules)
	want := []string{"a.ts", "b/nested/third.ts", "b/second.ts"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("paths = %v, want %v (sorted, nested)", got, want)
	}
	for _, m := range modules {
		if m.GetPath() == "a.ts" && m.GetContent() != "export const a = 1" {
			t.Errorf("a.ts content = %q", m.GetContent())
		}
	}
}

func TestListWorkspaceModulesSkipsNodeModules(t *testing.T) {
	root := t.TempDir()
	writeModuleFile(t, root, "keep.ts", "export const k = 1")
	writeModuleFile(t, root, "node_modules/pkg/index.ts", "export const nope = 1")

	got := modulePaths(listWorkspaceModules(t, root))
	if len(got) != 1 || got[0] != "keep.ts" {
		t.Fatalf("paths = %v, want only keep.ts (node_modules must be skipped)", got)
	}
}

func TestListWorkspaceModulesSkipsDotDirs(t *testing.T) {
	root := t.TempDir()
	writeModuleFile(t, root, "keep.ts", "export const k = 1")
	writeModuleFile(t, root, ".hidden/inside.ts", "export const nope = 1")

	got := modulePaths(listWorkspaceModules(t, root))
	if len(got) != 1 || got[0] != "keep.ts" {
		t.Fatalf("paths = %v, want only keep.ts (dot-directories must be skipped)", got)
	}
}

func TestListWorkspaceModulesSkipsBazelSymlinkDirs(t *testing.T) {
	root := t.TempDir()
	writeModuleFile(t, root, "keep.ts", "export const k = 1")
	writeModuleFile(t, root, "bazel-out/darwin/generated.ts", "export const nope = 1")

	got := modulePaths(listWorkspaceModules(t, root))
	if len(got) != 1 || got[0] != "keep.ts" {
		t.Fatalf("paths = %v, want only keep.ts (bazel-* dirs must be skipped)", got)
	}
}

func TestListWorkspaceModulesSkipsNestedPackageJSONProjects(t *testing.T) {
	root := t.TempDir()
	// The root itself declares a package.json (a repo root often does) and must NOT be
	// skipped on that account.
	writeModuleFile(t, root, "package.json", `{"name":"root"}`)
	writeModuleFile(t, root, "keep.ts", "export const k = 1")
	writeModuleFile(t, root, "ui/package.json", `{"name":"ui"}`)
	writeModuleFile(t, root, "ui/src/App.ts", "export const nope = 1")

	got := modulePaths(listWorkspaceModules(t, root))
	if len(got) != 1 || got[0] != "keep.ts" {
		t.Fatalf("paths = %v, want only keep.ts (a nested package.json project must be pruned)", got)
	}
}

func TestListWorkspaceModulesSkipsDeclarationFiles(t *testing.T) {
	root := t.TempDir()
	writeModuleFile(t, root, "keep.ts", "export const k = 1")
	writeModuleFile(t, root, "types.d.ts", "declare const x: number")

	got := modulePaths(listWorkspaceModules(t, root))
	if len(got) != 1 || got[0] != "keep.ts" {
		t.Fatalf("paths = %v, want only keep.ts (.d.ts files must be skipped)", got)
	}
}

func TestListWorkspaceModulesSkipsOversizedFiles(t *testing.T) {
	root := t.TempDir()
	writeModuleFile(t, root, "keep.ts", "export const k = 1")
	writeModuleFile(t, root, "huge.ts", "export const huge = \""+strings.Repeat("x", maxWorkspaceModuleFileSize+1)+"\"")

	got := modulePaths(listWorkspaceModules(t, root))
	if len(got) != 1 || got[0] != "keep.ts" {
		t.Fatalf("paths = %v, want only keep.ts (an over-size file must be dropped)", got)
	}
}
