package bazelbuild

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestCanonicalLabel(t *testing.T) {
	accepted := []struct{ in, want string }{
		{"//pkg/sub:target", "//pkg/sub:target"},
		{"//pkg:target", "//pkg:target"},
		{"//pkg", "//pkg:pkg"},
		{"//grpcview/echo/v1", "//grpcview/echo/v1:v1"},
		{"pkg:target", "//pkg:target"},
		{"pkg/sub:target", "//pkg/sub:target"},
		{"pkg", "//pkg:pkg"},
		{"@repo//pkg:target", "@repo//pkg:target"},
		{"@repo//pkg", "@repo//pkg:pkg"},
		{"@rules_go//go:def.bzl", "@rules_go//go:def.bzl"},
		{"//grpcview/v1:grpcviewv1_proto", "//grpcview/v1:grpcviewv1_proto"},
		{"//pkg:sub/target", "//pkg:sub/target"},
		{"//pkg+1:target+2", "//pkg+1:target+2"},
		{"@@rules_go+//go:def.bzl", "@@rules_go+//go:def.bzl"},
		{"@@rules_go+//go", "@@rules_go+//go:go"},
		{"@repo~1.0//pkg:target", "@repo~1.0//pkg:target"},
		{"@@protobuf~3.19.6//:protoc", "@@protobuf~3.19.6//:protoc"},
		{"  //pkg:target\t", "//pkg:target"},
	}
	for _, c := range accepted {
		got, err := CanonicalLabel(c.in)
		if err != nil {
			t.Errorf("CanonicalLabel(%q) = error %v, want %q", c.in, err, c.want)
			continue
		}
		if got != c.want {
			t.Errorf("CanonicalLabel(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	rejected := []string{
		"",
		"   ",
		":target",
		"//",
		"@repo",
		"@repo:target",
		"@@rules_go+",
		"@@rules_go+:def.bzl",
		"@repo~1.0",
		"//x --output_base=/tmp",
		"--output_base=/tmp",
		"-//pkg:target",
		"//pkg:target --keep_going",
		"//pkg with space:target",
		"//pkg:tar get",
		"//pkg:target\n--output_base=/tmp",
		"//pkg\n",
		"//../etc:passwd",
		"//pkg/../..:target",
		"//pkg:../secret",
		"//pkg//sub:target",
		"//pkg/:target",
		"//pkg:sub/",
		"//pkg/...",
		"//...",
		"//pkg:*",
		"//pkg:all",
		"//pkg:all-targets",
		"pkg:all",
		"@repo//pkg:all",
		"//pkg/all",
		"//pkg:$USER",
		"//pkg:`id`",
		"//pkg:'target'",
		`//pkg:"target"`,
		"//pkg:target;rm -rf /",
		"//pkg:target|cat",
		"//pkg:target&",
		"//pkg;//other:target",
	}
	for _, in := range rejected {
		got, err := CanonicalLabel(in)
		if err == nil {
			t.Errorf("CanonicalLabel(%q) = %q, want an error", in, got)
		}
	}
}

func TestCanonicalLabelPlainNames(t *testing.T) {
	for _, in := range []string{"//pkg:allocator", "//pkg:all_protos", "//pkg:a.b-c_d", "//a1/b2:c3"} {
		if _, err := CanonicalLabel(in); err != nil {
			t.Errorf("CanonicalLabel(%q) = error %v, want accepted", in, err)
		}
	}
}

func TestCanonicalLabelPatternMessage(t *testing.T) {
	for _, in := range []string{"//pkg:all", "//pkg:all-targets", "//pkg/all"} {
		_, err := CanonicalLabel(in)
		if err == nil {
			t.Fatalf("CanonicalLabel(%q) succeeded, want a pattern rejection", in)
		}
		if !strings.Contains(err.Error(), "pattern") || !strings.Contains(err.Error(), "one target") {
			t.Errorf("CanonicalLabel(%q) error %q should say it is a pattern and that a source names one target", in, err)
		}
	}
}

func TestFindRoot(t *testing.T) {
	t.Run("MODULE.bazel in an ancestor", func(t *testing.T) {
		root := t.TempDir()
		write(t, filepath.Join(root, "MODULE.bazel"), []byte("module(name='x')"))
		deep := filepath.Join(root, "a", "b", "c")
		if err := os.MkdirAll(deep, 0o755); err != nil {
			t.Fatal(err)
		}
		assertSameDir(t, FindRoot(deep), root)
	})

	t.Run("WORKSPACE.bazel counts too", func(t *testing.T) {
		root := t.TempDir()
		write(t, filepath.Join(root, "WORKSPACE.bazel"), nil)
		deep := filepath.Join(root, "a")
		if err := os.MkdirAll(deep, 0o755); err != nil {
			t.Fatal(err)
		}
		assertSameDir(t, FindRoot(deep), root)
	})

	t.Run("WORKSPACE counts too", func(t *testing.T) {
		root := t.TempDir()
		write(t, filepath.Join(root, "WORKSPACE"), nil)
		assertSameDir(t, FindRoot(root), root)
	})

	t.Run("start itself is the root", func(t *testing.T) {
		root := t.TempDir()
		write(t, filepath.Join(root, "MODULE.bazel"), nil)
		assertSameDir(t, FindRoot(root), root)
	})

	t.Run("nothing found", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "a", "b")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if marker := markerAbove(dir); marker != "" {
			t.Skipf("the temp tree lives under a bazel workspace (%s)", marker)
		}
		if got := FindRoot(dir); got != "" {
			t.Errorf("FindRoot(%q) = %q, want \"\"", dir, got)
		}
	})
}

func TestDescriptorSetsHappyPath(t *testing.T) {
	root := t.TempDir()
	first := setBytes(t, "a.proto", "b.proto")
	second := setBytes(t, "c.proto")
	write(t, filepath.Join(root, "out", "one.bin"), first)
	write(t, filepath.Join(root, "out", "two.bin"), second)

	log := filepath.Join(root, "argv.log")
	binary := fakeBazel(t, `
printf '%s\n' "$*" >> `+shquote(log)+`
if [ "$1" = cquery ]; then
  echo out/one.bin
  echo ''
  echo out/two.bin
fi
exit 0
`)

	b := Builder{Binary: binary, Root: root}
	sets, err := b.DescriptorSets(context.Background(), "pkg")
	if err != nil {
		t.Fatalf("DescriptorSets: %v", err)
	}
	if len(sets) != 2 {
		t.Fatalf("got %d sets, want 2", len(sets))
	}
	if names := fileNames(sets[0]); strings.Join(names, ",") != "a.proto,b.proto" {
		t.Errorf("first set files = %v, want [a.proto b.proto]", names)
	}
	if names := fileNames(sets[1]); strings.Join(names, ",") != "c.proto" {
		t.Errorf("second set files = %v, want [c.proto]", names)
	}

	argv, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(argv)), "\n")
	if len(lines) != 3 {
		t.Fatalf("bazel was invoked %d times, want 3: %q", len(lines), lines)
	}
	wantQuery := `query --output=label --order_output=no --curses=no --color=no --noshow_progress -- kind("^(proto_library|proto_descriptor_set) rule$", deps(//pkg:pkg))`
	wantBuild := "build --curses=no --color=no --noshow_progress -- //pkg:pkg"
	wantCquery := "cquery --output=files --curses=no --color=no --noshow_progress -- //pkg:pkg"
	if lines[0] != wantQuery {
		t.Errorf("query argv = %q, want %q", lines[0], wantQuery)
	}
	if lines[1] != wantBuild {
		t.Errorf("build argv = %q, want %q", lines[1], wantBuild)
	}
	if lines[2] != wantCquery {
		t.Errorf("cquery argv = %q, want %q", lines[2], wantCquery)
	}
}

// The transitive half: a proto_library's own descriptor set holds only its own files, so the deps
// query's labels have to reach BOTH the build (as patterns) and the cquery (as one union expression),
// or an import of google/protobuf/any.proto has nothing to link against.
func TestDescriptorSetsReadsTheDepClosure(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "out", "own.bin"), setBytes(t, "a.proto"))
	write(t, filepath.Join(root, "out", "any.bin"), setBytes(t, "google/protobuf/any.proto"))

	log := filepath.Join(root, "argv.log")
	binary := fakeBazel(t, `
printf '%s\n' "$*" >> `+shquote(log)+`
case "$1" in
query)
  # The label under query, one dep, a duplicate of it, and a line no label parses out of.
  echo //pkg:pkg
  echo '@protobuf//src/google/protobuf:any_proto'
  echo '@protobuf//src/google/protobuf:any_proto'
  echo 'Loading: 3 packages loaded'
  ;;
cquery)
  echo out/own.bin
  echo out/any.bin
  ;;
esac
exit 0
`)

	sets, err := Builder{Binary: binary, Root: root}.DescriptorSets(context.Background(), "//pkg")
	if err != nil {
		t.Fatalf("DescriptorSets: %v", err)
	}
	if len(sets) != 2 {
		t.Fatalf("got %d sets, want 2", len(sets))
	}

	argv, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(argv)), "\n")
	if len(lines) != 3 {
		t.Fatalf("bazel was invoked %d times, want 3: %q", len(lines), lines)
	}
	wantBuild := "build --curses=no --color=no --noshow_progress -- //pkg:pkg @protobuf//src/google/protobuf:any_proto"
	wantCquery := "cquery --output=files --curses=no --color=no --noshow_progress -- //pkg:pkg + @protobuf//src/google/protobuf:any_proto"
	if lines[1] != wantBuild {
		t.Errorf("build argv = %q, want %q", lines[1], wantBuild)
	}
	if lines[2] != wantCquery {
		t.Errorf("cquery argv = %q, want %q", lines[2], wantCquery)
	}
}

// A failed deps query is fatal, not degraded to the label alone: silently resolving without the
// closure is exactly the "no such file: google/protobuf/any.proto" the closure exists to prevent.
func TestDescriptorSetsQueryFailure(t *testing.T) {
	root := t.TempDir()
	binary := fakeBazel(t, `
if [ "$1" = query ]; then echo 'ERROR: no such package' >&2; exit 7; fi
exit 0
`)
	_, err := Builder{Binary: binary, Root: root}.DescriptorSets(context.Background(), "//pkg:target")
	if err == nil {
		t.Fatal("DescriptorSets succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "query") || !strings.Contains(err.Error(), "//pkg:target") {
		t.Errorf("error %q should name the invocation and the label", err)
	}
}

func TestDescriptorSetsMissingOutput(t *testing.T) {
	root := t.TempDir()
	binary := fakeBazel(t, `
if [ "$1" = cquery ]; then echo bazel-out/k8-fastbuild/bin/pkg/gone.bin; fi
exit 0
`)
	_, err := Builder{Binary: binary, Root: root}.DescriptorSets(context.Background(), "//pkg:target")
	if err == nil {
		t.Fatal("DescriptorSets succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "--remote_download_toplevel") {
		t.Errorf("error %q does not name --remote_download_toplevel", err)
	}
	if !strings.Contains(err.Error(), "gone.bin") {
		t.Errorf("error %q does not name the missing file", err)
	}
}

func TestDescriptorSetsNoOutputs(t *testing.T) {
	root := t.TempDir()
	binary := fakeBazel(t, "exit 0\n")
	_, err := Builder{Binary: binary, Root: root}.DescriptorSets(context.Background(), "//pkg:target")
	if err == nil || !strings.Contains(err.Error(), "no output files") {
		t.Fatalf("err = %v, want it to report no output files", err)
	}
}

func TestDescriptorSetsUnparseableOutput(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "junk.bin"), []byte{0xff, 0xff, 0xff, 0xff})
	binary := fakeBazel(t, `
if [ "$1" = cquery ]; then echo junk.bin; fi
exit 0
`)
	_, err := Builder{Binary: binary, Root: root}.DescriptorSets(context.Background(), "//pkg:target")
	if err == nil {
		t.Fatal("DescriptorSets succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "junk.bin") || !strings.Contains(err.Error(), "FileDescriptorSet") {
		t.Errorf("error %q should name the file and what it is not", err)
	}
}

func TestDescriptorSetsEmptyOutput(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "out", "empty.bin"), setBytes(t))
	binary := fakeBazel(t, `
if [ "$1" = cquery ]; then echo out/empty.bin; fi
exit 0
`)
	_, err := Builder{Binary: binary, Root: root}.DescriptorSets(context.Background(), "//pkg:target")
	if err == nil {
		t.Fatal("DescriptorSets succeeded, want an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "out/empty.bin") || !strings.Contains(msg, "//pkg:target") {
		t.Errorf("error %q should name both the offending file and the target", msg)
	}
	if !strings.Contains(msg, "no proto files") || !strings.Contains(msg, "FileDescriptorSets") {
		t.Errorf("error %q should say the output holds no proto files and what the target must be", msg)
	}
}

func TestDescriptorSetsEmptyAmongOutputs(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "out", "one.bin"), setBytes(t, "a.proto"))
	write(t, filepath.Join(root, "out", "two.bin"), setBytes(t))
	binary := fakeBazel(t, `
if [ "$1" = cquery ]; then echo out/one.bin; echo out/two.bin; fi
exit 0
`)
	_, err := Builder{Binary: binary, Root: root}.DescriptorSets(context.Background(), "//pkg:target")
	if err == nil || !strings.Contains(err.Error(), "out/two.bin") {
		t.Fatalf("err = %v, want it to name out/two.bin", err)
	}
}

func TestDescriptorSetsBuildFailure(t *testing.T) {
	root := t.TempDir()
	binary := fakeBazel(t, `
if [ "$1" = query ]; then exit 0; fi
i=1
while [ $i -le 24 ]; do echo "noise line $i" >&2; i=$((i+1)); done
echo 'ERROR: no such package' >&2
exit 1
`)
	_, err := Builder{Binary: binary, Root: root}.DescriptorSets(context.Background(), "//pkg:target")
	if err == nil {
		t.Fatal("DescriptorSets succeeded, want an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "ERROR: no such package") {
		t.Errorf("error %q is missing the stderr tail", msg)
	}
	if !strings.Contains(msg, "build") || !strings.Contains(msg, "//pkg:target") {
		t.Errorf("error %q should name the invocation and the label", msg)
	}
	if strings.Contains(msg, "noise line 1\n") {
		t.Errorf("error %q kept the head of stderr, want only the tail", msg)
	}
}

func TestDescriptorSetsTimeout(t *testing.T) {
	root := t.TempDir()
	// `sleep` in the background, not as the script's last command: a shell that
	// execs the last command leaves nothing holding the output pipe once the
	// context kills it, which hides the case this test is about.
	binary := fakeBazel(t, "sleep 30 &\nwait\n")
	start := time.Now()
	_, err := Builder{Binary: binary, Root: root, Timeout: 100 * time.Millisecond}.
		DescriptorSets(context.Background(), "//pkg:target")
	if err == nil {
		t.Fatal("DescriptorSets succeeded, want a timeout error")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("took %s, want the timeout to cut it off", elapsed)
	}
	if !strings.Contains(err.Error(), "timeout") || !strings.Contains(err.Error(), "bazel.timeout_seconds") {
		t.Errorf("error %q should report the timeout and name bazel.timeout_seconds", err)
	}
}

func TestDescriptorSetsRejectsBadLabelBeforeExec(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "ran")
	binary := fakeBazel(t, "touch "+shquote(marker)+"\nexit 0\n")
	_, err := Builder{Binary: binary, Root: root}.
		DescriptorSets(context.Background(), "//x --output_base=/tmp")
	if err == nil {
		t.Fatal("DescriptorSets succeeded, want an invalid-label error")
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("bazel was exec'd for an invalid label")
	}
}

func TestDescriptorSetsNeedsRoot(t *testing.T) {
	_, err := Builder{Binary: "true"}.DescriptorSets(context.Background(), "//pkg:target")
	if err == nil || !strings.Contains(err.Error(), "MODULE.bazel") {
		t.Fatalf("err = %v, want it to explain the missing bazel root", err)
	}
}

func TestQueryTargetsSortsDedupesAndCanonicalizes(t *testing.T) {
	root := t.TempDir()
	log := filepath.Join(root, "argv.log")
	binary := fakeBazel(t, `
printf '%s\n' "$*" >> `+shquote(log)+`
cat <<'EOF'
//b/pkg:descriptors
//a/pkg:a_proto

//a/pkg
//a/pkg:a_pkg
EOF
exit 0
`)
	labels, warning, err := Builder{Binary: binary, Root: root}.QueryTargets(context.Background())
	if err != nil {
		t.Fatalf("QueryTargets: %v", err)
	}
	if warning != "" {
		t.Errorf("warning = %q, want none on a clean query", warning)
	}
	want := "//a/pkg:a_pkg,//a/pkg:a_proto,//a/pkg:pkg,//b/pkg:descriptors"
	if got := strings.Join(labels, ","); got != want {
		t.Errorf("labels = %q, want %q", got, want)
	}

	argv, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(argv))
	wantArgv := `query --output=label --order_output=no --keep_going --curses=no --color=no ` +
		`--noshow_progress -- kind("^(proto_library|proto_descriptor_set) rule$", //...)`
	if line != wantArgv {
		t.Errorf("query argv = %q, want %q", line, wantArgv)
	}
}

func TestQueryTargetsKeepsPartialResults(t *testing.T) {
	root := t.TempDir()
	binary := fakeBazel(t, `
echo //a:a_proto
echo 'ERROR: no such package b: BUILD file not found' >&2
exit 3
`)
	labels, warning, err := Builder{Binary: binary, Root: root}.QueryTargets(context.Background())
	if err != nil {
		t.Fatalf("QueryTargets: %v", err)
	}
	if strings.Join(labels, ",") != "//a:a_proto" {
		t.Errorf("labels = %v, want [//a:a_proto]", labels)
	}
	if !strings.Contains(warning, "no such package b") {
		t.Errorf("warning %q does not carry bazel's reason", warning)
	}
}

func TestQueryTargetsFailsWithNoOutput(t *testing.T) {
	root := t.TempDir()
	binary := fakeBazel(t, "echo 'ERROR: query interrupted' >&2\nexit 7\n")
	_, _, err := Builder{Binary: binary, Root: root}.QueryTargets(context.Background())
	if err == nil {
		t.Fatal("QueryTargets succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "query interrupted") {
		t.Errorf("error %q does not carry bazel's stderr tail", err)
	}
}

func TestQueryTargetsNeedsRoot(t *testing.T) {
	_, _, err := Builder{Binary: "true"}.QueryTargets(context.Background())
	if err == nil || !strings.Contains(err.Error(), "MODULE.bazel") {
		t.Fatalf("err = %v, want it to explain the missing bazel root", err)
	}
}

func fakeBazel(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-bazel")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("write fake bazel: %v", err)
	}
	return path
}

func setBytes(t *testing.T, names ...string) []byte {
	t.Helper()
	set := &descriptorpb.FileDescriptorSet{}
	for _, name := range names {
		set.File = append(set.File, &descriptorpb.FileDescriptorProto{
			Name:    proto.String(name),
			Package: proto.String("test.v1"),
			Syntax:  proto.String("proto3"),
		})
	}
	raw, err := proto.Marshal(set)
	if err != nil {
		t.Fatalf("marshal FileDescriptorSet: %v", err)
	}
	return raw
}

func fileNames(set *descriptorpb.FileDescriptorSet) []string {
	var out []string
	for _, f := range set.GetFile() {
		out = append(out, f.GetName())
	}
	return out
}

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertSameDir(t *testing.T, got, want string) {
	t.Helper()
	if got == "" {
		t.Fatalf("FindRoot returned \"\", want %q", want)
	}
	gotReal, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatal(err)
	}
	wantReal, err := filepath.EvalSymlinks(want)
	if err != nil {
		t.Fatal(err)
	}
	if gotReal != wantReal {
		t.Errorf("FindRoot = %q, want %q", got, want)
	}
}

func markerAbove(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		parent := filepath.Dir(abs)
		if parent == abs {
			return ""
		}
		abs = parent
		for _, marker := range rootMarkers {
			p := filepath.Join(abs, marker)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
}

func shquote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
