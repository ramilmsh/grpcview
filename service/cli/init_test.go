package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitEndToEnd(t *testing.T) {
	root := t.TempDir()
	noopServe := func(context.Context, ServeOptions) error { return nil }

	run := func(args ...string) (out, errOut string, code int) {
		var o, e bytes.Buffer
		s := Streams{In: strings.NewReader(""), Out: &o, Err: &e}
		code = Main(context.Background(), append([]string{"--workspace", root}, args...), s, noopServe)
		return o.String(), e.String(), code
	}

	out, errOut, code := run("init", "requests")
	if code != 0 {
		t.Fatalf("init exit code = %d, want 0 (stdout=%q stderr=%q)", code, out, errOut)
	}
	if !strings.Contains(out, "requests") {
		t.Errorf("stdout = %q, want it to mention the created collection", out)
	}

	manifest := filepath.Join(root, "requests", "grpcview.json")
	if _, err := os.Stat(manifest); err != nil {
		t.Errorf("collection manifest missing at %s: %v", manifest, err)
	}

	// A repeat run must not succeed: this is the one verb that creates a collection, and
	// silently reusing an existing address would defeat the point of removing the old
	// implicit auto-create.
	_, _, code2 := run("init", "requests")
	if code2 == 0 {
		t.Fatalf("second init at the same address exit code = %d, want non-zero (AlreadyExists)", code2)
	}
}

func TestInitHonorsAnExplicitName(t *testing.T) {
	root := t.TempDir()
	noopServe := func(context.Context, ServeOptions) error { return nil }

	var o, e bytes.Buffer
	s := Streams{In: strings.NewReader(""), Out: &o, Err: &e}
	code := Main(context.Background(), []string{"--workspace", root, "init", "svc", "--name", "My Collection"}, s, noopServe)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stdout=%q stderr=%q)", code, o.String(), e.String())
	}
	if !strings.Contains(o.String(), "My Collection") {
		t.Errorf("stdout = %q, want it to carry the explicit display name", o.String())
	}
}

func TestWorkspaceFlagRejectsAMissingDirectory(t *testing.T) {
	var out, errBuf bytes.Buffer
	s := Streams{In: strings.NewReader(""), Out: &out, Err: &errBuf}
	code := Main(context.Background(),
		[]string{"--workspace", "/definitely/not/here", "get"}, s,
		func(context.Context, ServeOptions) error { return nil })

	if code != 2 {
		t.Errorf("exit code = %d, want 2 (stdout=%q stderr=%q)", code, out.String(), errBuf.String())
	}
	if out.String() != "" {
		t.Errorf("stdout = %q, want empty", out.String())
	}
}

func TestResolveInitCollection(t *testing.T) {
	t.Run("an explicit dir is returned verbatim, unresolved", func(t *testing.T) {
		got, err := resolveInitCollection(&globalFlags{}, "services/payments/requests")
		if err != nil || got != "services/payments/requests" {
			t.Fatalf("got %q, err %v, want the argument unchanged", got, err)
		}
	})

	t.Run("with no dir, a --workspace elsewhere than the cwd is an error naming both paths", func(t *testing.T) {
		other := t.TempDir()
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}

		_, err = resolveInitCollection(&globalFlags{Workspace: other}, "")
		if err == nil {
			t.Fatal("want an error: the process cwd is not inside the overridden workspace")
		}
		if !strings.Contains(err.Error(), other) || !strings.Contains(err.Error(), cwd) {
			t.Errorf("error = %v, want it to name both %q and %q", err, other, cwd)
		}
	})
}
