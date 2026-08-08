package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReadWriteRemove_roundTrip(t *testing.T) {
	t.Setenv("GRPCVIEW_CONFIG_DIR", t.TempDir())
	root := t.TempDir()

	if _, err := Read(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Read of an unregistered root = %v, want os.ErrNotExist", err)
	}

	want := Registration{Port: 12345, Pid: os.Getpid(), Root: root, Version: "dev"}
	if err := Write(want); err != nil {
		t.Fatal(err)
	}

	got, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != want.Port || got.Pid != want.Pid || got.Root != want.Root {
		t.Fatalf("Read = %+v, want %+v", got, want)
	}
	if got.URL() != "http://127.0.0.1:12345" {
		t.Fatalf("URL = %q", got.URL())
	}

	if err := Remove(root); err != nil {
		t.Fatal(err)
	}
	if err := Remove(root); err != nil {
		t.Fatalf("Remove of an absent registration = %v, want nil", err)
	}
}

func TestWrite_permissions(t *testing.T) {
	t.Setenv("GRPCVIEW_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	if err := Write(Registration{Port: 1, Root: root}); err != nil {
		t.Fatal(err)
	}

	path, err := Path(root)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("registration mode = %o, want 600", perm)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("registration dir mode = %o, want 700", perm)
	}
}

// A hint that does not parse is no hint: it must read as absent rather than as an error the
// caller has to classify.
func TestRead_corruptIsAbsent(t *testing.T) {
	t.Setenv("GRPCVIEW_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	if _, err := ensureDir(); err != nil {
		t.Fatal(err)
	}
	path, err := Path(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Read of a corrupt registration = %v, want os.ErrNotExist", err)
	}

	if err := os.WriteFile(path, []byte(`{"port":0,"workspace_root":""}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Read of an empty registration = %v, want os.ErrNotExist", err)
	}
}

func TestPath_isPerRoot(t *testing.T) {
	t.Setenv("GRPCVIEW_CONFIG_DIR", t.TempDir())
	a, err := Path(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, err := Path(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("two roots share one registration path %q", a)
	}
}

func TestExecutable_Same(t *testing.T) {
	self := SelfExecutable()
	if self == (Executable{}) {
		t.Skip("the test binary cannot stat itself")
	}
	if self != SelfExecutable() {
		t.Error("SelfExecutable is not stable across calls")
	}
	// A rebuild changes mtime and usually size; a version string would not change at all.
	rebuilt := self
	rebuilt.Modified++
	if self == rebuilt {
		t.Error("a changed mtime compared equal")
	}
}

func TestAlive(t *testing.T) {
	if !Alive(os.Getpid()) {
		t.Error("this process is not alive")
	}
	if Alive(0) || Alive(-1) {
		t.Error("a non-pid reported alive")
	}
}
