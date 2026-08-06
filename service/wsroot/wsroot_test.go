package wsroot

import (
	"os"
	"path/filepath"
	"testing"
)

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", path, err)
	}
}

func TestDiscover_OverrideWinsOverNearbyGit(t *testing.T) {
	tmp := t.TempDir()

	repo := filepath.Join(tmp, "repo")
	mustMkdirAll(t, filepath.Join(repo, ".git"))
	cwd := filepath.Join(repo, "sub", "dir")
	mustMkdirAll(t, cwd)

	override := filepath.Join(tmp, "elsewhere")
	mustMkdirAll(t, override)

	root, warn, err := Discover(override, cwd)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if warn != "" {
		t.Errorf("warn = %q, want empty", warn)
	}
	want, err := filepath.Abs(override)
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}
	if root != filepath.Clean(want) {
		t.Errorf("root = %q, want %q", root, want)
	}
}

func TestDiscover_RelativeOverrideResolvesAgainstCwd(t *testing.T) {
	t.Setenv(BuildWorkspaceEnv, "")
	tmp := t.TempDir()
	cwd := filepath.Join(tmp, "cwd")
	mustMkdirAll(t, cwd)

	target := filepath.Join(cwd, "nested", "override")
	mustMkdirAll(t, target)

	root, warn, err := Discover(filepath.Join("nested", "override"), cwd)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if warn != "" {
		t.Errorf("warn = %q, want empty", warn)
	}
	if root != filepath.Clean(target) {
		t.Errorf("root = %q, want %q", root, target)
	}
}

func TestDiscover_OverrideMustExist(t *testing.T) {
	tmp := t.TempDir()
	cwd := t.TempDir()

	missing := filepath.Join(tmp, "does-not-exist")
	_, _, err := Discover(missing, cwd)
	if err == nil {
		t.Fatal("Discover: want error, got nil")
	}
}

func TestDiscover_OverrideMustBeDirectory(t *testing.T) {
	tmp := t.TempDir()
	cwd := t.TempDir()

	file := filepath.Join(tmp, "not-a-dir")
	if err := os.WriteFile(file, []byte("hi"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, _, err := Discover(file, cwd)
	if err == nil {
		t.Fatal("Discover: want error, got nil")
	}
}

func TestDiscover_GitAsDirectoryFromNestedCwd(t *testing.T) {
	t.Setenv(BuildWorkspaceEnv, "")
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	mustMkdirAll(t, filepath.Join(repo, ".git"))

	cwd := filepath.Join(repo, "a", "b", "c")
	mustMkdirAll(t, cwd)

	root, warn, err := Discover("", cwd)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if warn != "" {
		t.Errorf("warn = %q, want empty", warn)
	}
	if root != filepath.Clean(repo) {
		t.Errorf("root = %q, want %q", root, repo)
	}
}

func TestDiscover_GitAsFileFromNestedCwd(t *testing.T) {
	t.Setenv(BuildWorkspaceEnv, "")
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "worktree")
	mustMkdirAll(t, repo)
	if err := os.WriteFile(filepath.Join(repo, ".git"), []byte("gitdir: ../elsewhere\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cwd := filepath.Join(repo, "a", "b")
	mustMkdirAll(t, cwd)

	root, warn, err := Discover("", cwd)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if warn != "" {
		t.Errorf("warn = %q, want empty", warn)
	}
	if root != filepath.Clean(repo) {
		t.Errorf("root = %q, want %q", root, repo)
	}
}

func TestDiscover_NoGitAnywhereReturnsCwdWithWarning(t *testing.T) {
	t.Setenv(BuildWorkspaceEnv, "")
	cwd := t.TempDir()

	root, warn, err := Discover("", cwd)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if warn == "" {
		t.Error("warn = \"\", want non-empty")
	}
	want, err := filepath.Abs(cwd)
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}
	if root != filepath.Clean(want) {
		t.Errorf("root = %q, want %q", root, want)
	}
}

func TestStateDir_StableForSameRoot(t *testing.T) {
	root := t.TempDir()

	a, err := StateDir(root)
	if err != nil {
		t.Fatalf("StateDir: %v", err)
	}
	b, err := StateDir(root)
	if err != nil {
		t.Fatalf("StateDir: %v", err)
	}
	if a != b {
		t.Errorf("StateDir not stable: %q != %q", a, b)
	}
}

func TestStateDir_DiffersForSameBasenameDifferentPath(t *testing.T) {
	tmp := t.TempDir()
	rootA := filepath.Join(tmp, "one", "myproject")
	rootB := filepath.Join(tmp, "two", "myproject")
	mustMkdirAll(t, rootA)
	mustMkdirAll(t, rootB)

	a, err := StateDir(rootA)
	if err != nil {
		t.Fatalf("StateDir(rootA): %v", err)
	}
	b, err := StateDir(rootB)
	if err != nil {
		t.Fatalf("StateDir(rootB): %v", err)
	}
	if a == b {
		t.Errorf("StateDir(%q) == StateDir(%q) == %q, want distinct paths", rootA, rootB, a)
	}
	if filepath.Base(rootA) != filepath.Base(rootB) {
		t.Fatalf("test setup broken: basenames differ (%q, %q)", rootA, rootB)
	}
}

func TestStateDir_RootedUnderUserConfigDir(t *testing.T) {
	root := t.TempDir()

	dir, err := StateDir(root)
	if err != nil {
		t.Fatalf("StateDir: %v", err)
	}

	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("os.UserConfigDir: %v", err)
	}

	rel, err := filepath.Rel(configDir, dir)
	if err != nil {
		t.Fatalf("filepath.Rel: %v", err)
	}
	if len(rel) >= 2 && rel[:2] == ".." {
		t.Errorf("StateDir(%q) = %q, not rooted under UserConfigDir %q", root, dir, configDir)
	}
}

func TestStateDir_DoesNotCreateTheDirectory(t *testing.T) {
	root := t.TempDir()

	dir, err := StateDir(root)
	if err != nil {
		t.Fatalf("StateDir: %v", err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Errorf("StateDir(%q) = %q already exists, want StateDir to leave creation to the caller", root, dir)
	} else if !os.IsNotExist(err) {
		t.Errorf("os.Stat(%q): unexpected error: %v", dir, err)
	}
}

func TestDiscover_BuildWorkspaceEnvUsedWhenNoOverride(t *testing.T) {
	tmp := t.TempDir()
	invoked := filepath.Join(tmp, "invoked")
	mustMkdirAll(t, invoked)
	t.Setenv(BuildWorkspaceEnv, invoked)

	root, warn, err := Discover("", filepath.Join(tmp, "runfiles"))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if root != invoked {
		t.Errorf("root = %q, want %q", root, invoked)
	}
	if warn != "" {
		t.Errorf("warn = %q, want none", warn)
	}
}

func TestDiscover_ExplicitOverrideBeatsBuildWorkspaceEnv(t *testing.T) {
	tmp := t.TempDir()
	fromEnv := filepath.Join(tmp, "from-env")
	explicit := filepath.Join(tmp, "explicit")
	mustMkdirAll(t, fromEnv)
	mustMkdirAll(t, explicit)
	t.Setenv(BuildWorkspaceEnv, fromEnv)

	root, _, err := Discover(explicit, tmp)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if root != explicit {
		t.Errorf("root = %q, want the explicit override %q", root, explicit)
	}
}

// The reason the env var outranks the .git walk rather than seeding it: a bazel workspace
// nested inside a larger repository must resolve to the bazel workspace.
func TestDiscover_BuildWorkspaceEnvBeatsEnclosingGitRoot(t *testing.T) {
	tmp := t.TempDir()
	outerRepo := filepath.Join(tmp, "repo")
	mustMkdirAll(t, filepath.Join(outerRepo, ".git"))
	nested := filepath.Join(outerRepo, "tools", "grpcview")
	mustMkdirAll(t, nested)
	t.Setenv(BuildWorkspaceEnv, nested)

	root, _, err := Discover("", filepath.Join(nested, "sub"))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if root != nested {
		t.Errorf("root = %q, want the bazel workspace %q, not the enclosing repo", root, nested)
	}
}

func TestDiscover_BuildWorkspaceEnvMustExist(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv(BuildWorkspaceEnv, filepath.Join(tmp, "missing"))

	if _, _, err := Discover("", tmp); err == nil {
		t.Fatal("Discover: want an error for a $BUILD_WORKSPACE_DIRECTORY that does not exist")
	}
}

func TestInvocationDir(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv(BuildWorkspaceEnv, tmp)
	dir, err := InvocationDir()
	if err != nil {
		t.Fatalf("InvocationDir: %v", err)
	}
	if dir != tmp {
		t.Errorf("dir = %q, want %q", dir, tmp)
	}

	t.Setenv(BuildWorkspaceEnv, "")
	dir, err = InvocationDir()
	if err != nil {
		t.Fatalf("InvocationDir: %v", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if dir != cwd {
		t.Errorf("with the env unset, dir = %q, want cwd %q", dir, cwd)
	}
}
