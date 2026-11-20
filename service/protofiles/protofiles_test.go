package protofiles

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jhump/protoreflect/desc"
)

func write(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const common = `syntax = "proto3";
package a.v1;

// Common is shared.
message Common { string id = 1; }
`

const svc = `syntax = "proto3";
package a.v1;

import "a/v1/common.proto";
import "google/protobuf/timestamp.proto";

// Greeter greets.
service Greeter {
  // Hello says hello.
  rpc Hello(Common) returns (google.protobuf.Timestamp);
}
`

func TestCompile(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{"a/v1/common.proto": common, "a/v1/svc.proto": svc})

	roots := Roots{root}
	found, err := Find(filepath.Join(root, "a"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range found {
		name, ok := roots.ImportName(p)
		if !ok {
			t.Fatalf("%s under no root", p)
		}
		names = append(names, name)
	}
	if got := strings.Join(names, ","); got != "a/v1/common.proto,a/v1/svc.proto" {
		t.Fatalf("names = %s", got)
	}

	res, err := Compile(context.Background(), roots, names...)
	if err != nil {
		t.Fatal(err)
	}

	var order []string
	for _, f := range res.Set.GetFile() {
		order = append(order, f.GetName())
	}
	if got, want := strings.Join(order, ","), "a/v1/common.proto,google/protobuf/timestamp.proto,a/v1/svc.proto"; got != want {
		t.Fatalf("order = %s, want %s", got, want)
	}

	files, err := desc.CreateFileDescriptorsFromSet(res.Set)
	if err != nil {
		t.Fatal(err)
	}
	sd := files["a/v1/svc.proto"].FindService("a.v1.Greeter")
	if sd == nil {
		t.Fatal("no Greeter")
	}
	if got := sd.GetSourceInfo().GetLeadingComments(); got != " Greeter greets.\n" {
		t.Errorf("service comment = %q", got)
	}
	if got := sd.FindMethodByName("Hello").GetSourceInfo().GetLeadingComments(); got != " Hello says hello.\n" {
		t.Errorf("method comment = %q", got)
	}

	if len(res.Originals) != 2 || res.Originals["a/v1/common.proto"] != common || res.Originals["a/v1/svc.proto"] != svc {
		t.Errorf("originals = %v", res.Originals)
	}
}

func TestCompilePaths(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{"x/common.proto": common, "y/svc.proto": svc})
	res, err := Compile(context.Background(), Paths{
		"a/v1/common.proto": filepath.Join(dir, "x/common.proto"),
		"a/v1/svc.proto":    filepath.Join(dir, "y/svc.proto"),
	}, "a/v1/svc.proto")
	if err != nil {
		t.Fatal(err)
	}
	if n := len(res.Set.GetFile()); n != 3 {
		t.Errorf("files = %d, want 3", n)
	}
}

func TestCompileErrors(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{
		"bad.proto": "syntax = \"proto3\";\nmessage A { Missing m = 1; }\nmessage B { Gone g = 1; }\n",
	})
	_, err := Compile(context.Background(), Roots{root}, "bad.proto")
	if err == nil {
		t.Fatal("no error")
	}
	msg := err.Error()
	for _, want := range []string{"bad.proto:2:13:", "Missing", "bad.proto:3:13:", "Gone"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q lacks %q", msg, want)
		}
	}
}

func TestCompileMissingImport(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{"a.proto": "syntax = \"proto3\";\nimport \"nope.proto\";\n"})
	_, err := Compile(context.Background(), Roots{root}, "a.proto")
	if err == nil || !strings.Contains(err.Error(), "nope.proto") {
		t.Fatalf("err = %v", err)
	}
}
