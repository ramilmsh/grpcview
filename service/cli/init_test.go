package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

func TestInitEndToEnd(t *testing.T) {
	root := t.TempDir()
	noopServe := func(context.Context, ServeOptions) error { return nil }

	run := func(args ...string) (out, errOut string, code int) {
		var o, e bytes.Buffer
		s := Streams{In: strings.NewReader(""), Out: &o, Err: &e}
		code = Main(context.Background(), append([]string{"--workspace", root, "--in-process"}, args...), s, noopServe)
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
	code := Main(context.Background(), []string{"--workspace", root, "--in-process", "init", "svc", "--name", "My Collection"}, s, noopServe)
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

func TestInitReportsSeededSources(t *testing.T) {
	fc := newFake()
	fc.writes.createdCollection = &grpcviewv1.Collection{
		Name: "requests",
		Sources: []*grpcviewv1.DescriptorSource{
			shared(reflectionSource("shared.example:50051", nil)),
		},
	}

	out, errOut, code := runCLI(fc, "", "init", "services/payments/requests")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stdout=%q stderr=%q)", code, out, errOut)
	}
	if !strings.Contains(out, "services/payments/requests") {
		t.Errorf("stdout = %q, want the created collection", out)
	}
	if !strings.Contains(errOut, "sources refresh") {
		t.Errorf("stderr = %q, want it to name the refresh that makes a seeded collection resolve", errOut)
	}

	quiet := newFake()
	quiet.writes.createdCollection = &grpcviewv1.Collection{Name: "requests"}
	_, quietErr, quietCode := runCLI(quiet, "", "init", "requests")
	if quietCode != 0 {
		t.Fatalf("exit code = %d, want 0", quietCode)
	}
	if quietErr != "" {
		t.Errorf("stderr = %q, want empty when nothing was seeded", quietErr)
	}
}
