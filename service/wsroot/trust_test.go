package wsroot

import (
	"os"
	"path/filepath"
	"testing"
)

func isolateUserConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
}

func TestTrustRoundTrip(t *testing.T) {
	isolateUserConfig(t)
	root := t.TempDir()

	trusted, err := IsTrusted(root)
	if err != nil {
		t.Fatalf("IsTrusted: %v", err)
	}
	if trusted {
		t.Fatal("a workspace nobody has trusted must not be trusted")
	}

	if err := Trust(root); err != nil {
		t.Fatalf("Trust: %v", err)
	}
	if trusted, err = IsTrusted(root); err != nil || !trusted {
		t.Fatalf("IsTrusted after Trust = %v, %v; want true, nil", trusted, err)
	}

	if err := os.WriteFile(filepath.Join(root, "grpcview.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if trusted, err = IsTrusted(root); err != nil || !trusted {
		t.Fatalf("editing the workspace must not un-trust it: got %v, %v", trusted, err)
	}

	if err := Revoke(root); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if trusted, err = IsTrusted(root); err != nil || trusted {
		t.Fatalf("IsTrusted after Revoke = %v, %v; want false, nil", trusted, err)
	}
}

func TestTrustNormalizesTheRoot(t *testing.T) {
	isolateUserConfig(t)
	root := t.TempDir()

	if err := Trust(root + string(filepath.Separator)); err != nil {
		t.Fatalf("Trust: %v", err)
	}
	if trusted, err := IsTrusted(filepath.Join(root, "sub", "..")); err != nil || !trusted {
		t.Fatalf("IsTrusted(uncleaned) = %v, %v; want true, nil", trusted, err)
	}
}

func TestTrustUntrustedNeighbourStaysUntrusted(t *testing.T) {
	isolateUserConfig(t)
	tmp := t.TempDir()
	mine := filepath.Join(tmp, "mine")
	theirs := filepath.Join(tmp, "theirs")
	mustMkdirAll(t, mine)
	mustMkdirAll(t, theirs)

	if err := Trust(mine); err != nil {
		t.Fatalf("Trust: %v", err)
	}
	if trusted, err := IsTrusted(theirs); err != nil || trusted {
		t.Fatalf("IsTrusted(a different root) = %v, %v; want false, nil", trusted, err)
	}
}

func TestTrustIsIdempotent(t *testing.T) {
	isolateUserConfig(t)
	root := t.TempDir()

	for range 3 {
		if err := Trust(root); err != nil {
			t.Fatalf("Trust: %v", err)
		}
	}
	if err := Revoke(root); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if trusted, err := IsTrusted(root); err != nil || trusted {
		t.Fatalf("one Revoke must undo any number of Trusts: got %v, %v", trusted, err)
	}
	if err := Revoke(root); err != nil {
		t.Fatalf("Revoke on an untrusted root: %v", err)
	}
}

func TestTrustCorruptFile(t *testing.T) {
	isolateUserConfig(t)
	root := t.TempDir()

	path, err := trustPath()
	if err != nil {
		t.Fatalf("trustPath: %v", err)
	}
	mustMkdirAll(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte("not json at all {{{"), 0o600); err != nil {
		t.Fatal(err)
	}

	trusted, err := IsTrusted(root)
	if err != nil {
		t.Fatalf("IsTrusted on a corrupt list: %v", err)
	}
	if trusted {
		t.Fatal("a corrupt list must not trust anything")
	}
	if err := Trust(root); err != nil {
		t.Fatalf("Trust over a corrupt list: %v", err)
	}
	if trusted, err = IsTrusted(root); err != nil || !trusted {
		t.Fatalf("IsTrusted after Trust over a corrupt list = %v, %v; want true, nil", trusted, err)
	}
}

func TestTrustFileIsUserOnly(t *testing.T) {
	isolateUserConfig(t)
	if err := Trust(t.TempDir()); err != nil {
		t.Fatalf("Trust: %v", err)
	}
	path, err := trustPath()
	if err != nil {
		t.Fatalf("trustPath: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("%s mode = %o, want 600", path, perm)
	}
}
