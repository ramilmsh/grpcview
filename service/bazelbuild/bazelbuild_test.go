package bazelbuild

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
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

func TestLocate(t *testing.T) {
	const root = "/ws"
	main := protoFile{Name: "pkg/a.proto", Path: "pkg/a.proto", Source: true, Owner: "@@//pkg:a.proto"}
	virtual := protoFile{Name: "google/protobuf/any.proto", Path: "bazel-out/k8/bin/external/protobuf+/src/google/protobuf/_virtual_imports/any_proto/google/protobuf/any.proto", Owner: "@@protobuf+//src/google/protobuf:any_proto"}
	prefixed := protoFile{Name: "acme/x/v.proto", Path: "bazel-out/k8/bin/vend/_virtual_imports/v_proto/acme/x/v.proto", Owner: "@@//vend:v_proto"}
	external := protoFile{Name: "e/e.proto", Path: "external/ext+/e/e.proto", Source: true, Owner: "@@ext+//e:e.proto"}
	q := map[string]target{
		"@@protobuf+//src/google/protobuf:any_proto": {Class: "proto_library", Srcs: []string{"@@protobuf+//src/google/protobuf:any.proto"}},
		"@@protobuf+//src/google/protobuf:any.proto": {Location: "/ob/external/protobuf+/src/google/protobuf/any.proto"},
		// Both srcs end in x/v.proto; only the shortest is imported as it.
		"@@//vend:v_proto":       {Class: "proto_library", Prefix: "acme", Srcs: []string{"@@//vend:deep/x/v.proto", "@@//vend:x/v.proto"}},
		"@@//vend:x/v.proto":      {Location: "/ws/vend/x/v.proto"},
		"@@//vend:deep/x/v.proto": {Location: "/ws/vend/deep/x/v.proto"},
		"@@ext+//e:e.proto":       {Location: "/ob/external/ext+/e/e.proto"},
	}

	got, err := locate(root, []protoFile{main, virtual, prefixed, external, {Name: "pkg/a.proto", Path: "elsewhere/a.proto", Source: true, Owner: "@@//elsewhere:a.proto"}}, q)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"pkg/a.proto":               "/ws/pkg/a.proto", // first wins
		"google/protobuf/any.proto": "/ob/external/protobuf+/src/google/protobuf/any.proto",
		"acme/x/v.proto":            "/ws/vend/x/v.proto",
		"e/e.proto":                 "/ob/external/ext+/e/e.proto",
	}
	if !maps.Equal(got, want) {
		t.Errorf("locate = %v, want %v", got, want)
	}

	for name, tc := range map[string]struct {
		f    protoFile
		q    map[string]target
		want string
	}{
		"generated, no virtual import": {
			protoFile{Name: "pkg/gen.proto", Path: "bazel-out/k8/bin/pkg/gen.proto", Owner: "@@//pkg:make_gen"},
			map[string]target{"@@//pkg:make_gen": {Class: "genrule"}},
			"bazel-out/k8/bin/pkg/gen.proto (of @@//pkg:make_gen) is generated",
		},
		"generated src of a virtual import": {
			protoFile{Name: "gen.proto", Path: "bazel-out/k8/bin/pkg/_virtual_imports/p/gen.proto", Owner: "@@//pkg:p"},
			map[string]target{"@@//pkg:p": {Class: "proto_library", Srcs: []string{"@@//pkg:gen.proto"}}, "@@//pkg:gen.proto": {}},
			"@@//pkg:gen.proto (of @@//pkg:p) is generated",
		},
		"no matching src": {
			protoFile{Name: "x.proto", Path: "bazel-out/k8/bin/pkg/_virtual_imports/p/x.proto", Owner: "@@//pkg:p"},
			map[string]target{"@@//pkg:p": {Class: "proto_library", Srcs: []string{"@@//pkg:y.proto"}}},
			"none of its srcs ends in x.proto",
		},
		"external source not located": {external, nil, "did not locate @@ext+//e:e.proto"},
	} {
		if _, err := locate(root, []protoFile{tc.f}, tc.q); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to contain %q", name, err, tc.want)
		}
	}
}

func TestOwnersSkipsMainSources(t *testing.T) {
	got := owners([]protoFile{
		{Source: true, Owner: "@@//pkg:a.proto"},
		{Owner: "@@b+//p:b_proto"},
		{Owner: "@@b+//p:b_proto"},
		{Source: true, Owner: "@@a+//p:a.proto"},
	})
	if want := []string{"@@a+//p:a.proto", "@@b+//p:b_proto"}; !slices.Equal(got, want) {
		t.Errorf("owners = %q, want %q", got, want)
	}
}

func line(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

var starlarkFileRe = regexp.MustCompile(`--starlark:file=\S+`)

func TestClosureArgv(t *testing.T) {
	root := t.TempDir()
	log := filepath.Join(root, "argv.log")
	cq := line(t, protoFile{Name: "pkg/a.proto", Path: "pkg/a.proto", Source: true, Owner: "@@//pkg:a.proto"}) + "\n\n" +
		line(t, protoFile{Name: "e/e.proto", Path: "external/ext+/e/e.proto", Source: true, Owner: "@@ext+//e:e.proto"})
	q := `{"type":"SOURCE_FILE","sourceFile":{"name":"@@ext+//e:e.proto","location":"/ob/external/ext+/e/e.proto:1:1"}}`
	// The fake checks the starlark file is the embedded one, while it still exists.
	binary := fakeBazel(t, `
printf '%s\n' "$*" >> `+shquote(log)+`
case "$1" in
cquery)
	f=$(printf '%s\n' "$@" | sed -n 's/^--starlark:file=//p')
	grep -q 'def format(target)' "$f" || { echo "no starlark file at $f" >&2; exit 9; }
	printf '%s\n' `+shquote(cq)+` ;;
query) printf '%s\n' `+shquote(q)+` ;;
*) exit 1 ;;
esac
`)
	got, err := Builder{Binary: binary, Root: root}.Closure(context.Background(), "pkg")
	if err != nil {
		t.Fatalf("Closure: %v", err)
	}
	want := map[string]string{"pkg/a.proto": filepath.Join(root, "pkg", "a.proto"), "e/e.proto": "/ob/external/ext+/e/e.proto"}
	if !maps.Equal(got, want) {
		t.Errorf("Closure = %v, want %v", got, want)
	}

	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	wantArgv := "cquery --output=starlark --starlark:file=F --curses=no --color=no --noshow_progress -- config(deps(//pkg:pkg, 1), target)\n" +
		"query --output=streamed_jsonproto --consistent_labels --order_output=no --curses=no --color=no --noshow_progress -- let o = @@ext+//e:e.proto in $o + labels(srcs, $o)\n"
	if got := starlarkFileRe.ReplaceAllString(string(raw), "--starlark:file=F"); got != wantArgv {
		t.Errorf("argv =\n%s\nwant\n%s", got, wantArgv)
	}
}

func TestClosureMainOnlySkipsQuery(t *testing.T) {
	root := t.TempDir()
	log := filepath.Join(root, "argv.log")
	cq := line(t, protoFile{Name: "pkg/a.proto", Path: "pkg/a.proto", Source: true, Owner: "@@//pkg:a.proto"})
	binary := fakeBazel(t, "printf '%s\\n' \"$*\" >> "+shquote(log)+"\nprintf '%s\\n' "+shquote(cq)+"\n")
	if _, err := (Builder{Binary: binary, Root: root}).Closure(context.Background(), "//pkg"); err != nil {
		t.Fatalf("Closure: %v", err)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(raw), "\n"); n != 1 {
		t.Errorf("bazel was invoked %d times, want 1: %q", n, raw)
	}
}

func TestClosureNoProtoInfo(t *testing.T) {
	binary := fakeBazel(t, "exit 0\n")
	_, err := Builder{Binary: binary, Root: t.TempDir()}.Closure(context.Background(), "//pkg:bin")
	if err == nil || !strings.Contains(err.Error(), "ProtoInfo") || !strings.Contains(err.Error(), "//pkg:bin") {
		t.Fatalf("err = %v, want it to name the label and the missing ProtoInfo", err)
	}
}

func TestClosureCqueryFailure(t *testing.T) {
	root := t.TempDir()
	binary := fakeBazel(t, `
i=1
while [ $i -le 24 ]; do echo "noise line $i" >&2; i=$((i+1)); done
echo 'ERROR: no such package' >&2
exit 7
`)
	_, err := Builder{Binary: binary, Root: root}.Closure(context.Background(), "//pkg:target")
	if err == nil {
		t.Fatal("Closure succeeded, want an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "cquery") || !strings.Contains(msg, "//pkg:target") || !strings.Contains(msg, "ERROR: no such package") {
		t.Errorf("error %q should name the invocation, the label and bazel's stderr tail", msg)
	}
	if strings.Contains(msg, "noise line 1\n") {
		t.Errorf("error %q kept the head of stderr, want only the tail", msg)
	}
}

func TestClosureTimeout(t *testing.T) {
	root := t.TempDir()
	// `sleep` in the background, not as the script's last command: a shell that
	// execs the last command leaves nothing holding the output pipe once the
	// context kills it, which hides the case this test is about.
	binary := fakeBazel(t, "sleep 30 &\nwait\n")
	start := time.Now()
	_, err := Builder{Binary: binary, Root: root, Timeout: 100 * time.Millisecond}.
		Closure(context.Background(), "//pkg:target")
	if err == nil {
		t.Fatal("Closure succeeded, want a timeout error")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("took %s, want the timeout to cut it off", elapsed)
	}
	if !strings.Contains(err.Error(), "timeout") || !strings.Contains(err.Error(), "bazel.timeout_seconds") {
		t.Errorf("error %q should report the timeout and name bazel.timeout_seconds", err)
	}
}

func TestClosureRejectsBadLabelBeforeExec(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "ran")
	binary := fakeBazel(t, "touch "+shquote(marker)+"\nexit 0\n")
	_, err := Builder{Binary: binary, Root: root}.
		Closure(context.Background(), "//x --output_base=/tmp")
	if err == nil {
		t.Fatal("Closure succeeded, want an invalid-label error")
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("bazel was exec'd for an invalid label")
	}
}

func TestClosureNeedsRoot(t *testing.T) {
	_, err := Builder{Binary: "true"}.Closure(context.Background(), "//pkg:target")
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
