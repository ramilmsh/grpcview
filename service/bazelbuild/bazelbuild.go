// Package bazelbuild locates a bazel label's .proto closure on disk. It is the mechanism, and knows
// nothing about grpcview's protos, store or RPCs.

package bazelbuild

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"maps"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

const DefaultTimeout = 10 * time.Minute

type Builder struct {
	Binary  string
	Root    string
	Timeout time.Duration
}

// Passed to every invocation: output is parsed, never shown.
var commonArgs = []string{"--curses=no", "--color=no", "--noshow_progress"}

var rootMarkers = []string{"MODULE.bazel", "WORKSPACE", "WORKSPACE.bazel"}

func FindRoot(start string) string {
	dir, err := filepath.Abs(start)
	if err != nil {
		return ""
	}
	for {
		for _, marker := range rootMarkers {
			if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// Every accepted character is explicit, and the package and target parts must each START with a letter
// or digit — which is what rejects a label that is really a flag ("--output_base=/tmp") as well as
// "."/".." traversal. The doubled "@" plus "+" and "~" inside a repo name is bzlmod's canonical repo
// spelling, and therefore the one a user pastes.
var labelRe = regexp.MustCompile(`^(@@?[A-Za-z0-9][A-Za-z0-9._+~-]*)?(//)?([A-Za-z0-9][A-Za-z0-9._+/-]*)?(?::([A-Za-z0-9][A-Za-z0-9._+/-]*))?$`)

const labelSyntax = `a label looks like //pkg/sub:target, //pkg, pkg:target, @repo//pkg:target or ` +
	`@@canonical_repo+//pkg:target (letters, digits, "_", "-", ".", "+" and "/" only, plus "~" in a repo name)`

var patternTargets = map[string]bool{"all": true, "all-targets": true, "*": true}

// Canonicalizing BEFORE any id is derived from a label is load-bearing: "//pkg" and "//pkg:pkg" are
// one target, and if both spellings could reach the store they would become two sources, breaking
// "re-adding an id refreshes that source in place".
func CanonicalLabel(label string) (string, error) {
	label = strings.Trim(label, " \t")
	if label == "" {
		return "", fmt.Errorf("empty bazel label: %s", labelSyntax)
	}
	m := labelRe.FindStringSubmatch(label)
	if m == nil {
		return "", fmt.Errorf("invalid bazel label %q: %s", label, labelSyntax)
	}
	repo, slashes, pkg, target := m[1], m[2], m[3], m[4]

	if repo != "" && slashes == "" {
		return "", fmt.Errorf("invalid bazel label %q: an external repository needs \"//\" before its package: %s", label, labelSyntax)
	}
	if slashes == "" && pkg == "" {
		return "", fmt.Errorf("invalid bazel label %q: no package: %s", label, labelSyntax)
	}
	for _, part := range []string{pkg, target} {
		if part == "" {
			continue
		}
		if strings.HasSuffix(part, "/") || strings.Contains(part, "//") {
			return "", fmt.Errorf("invalid bazel label %q: empty path segment: %s", label, labelSyntax)
		}
		for _, seg := range strings.Split(part, "/") {
			if strings.Trim(seg, ".") == "" {
				return "", fmt.Errorf("invalid bazel label %q: %q is not a path segment: %s", label, seg, labelSyntax)
			}
		}
	}

	if target == "" {
		if pkg == "" {
			return "", fmt.Errorf("invalid bazel label %q: no target: %s", label, labelSyntax)
		}
		segs := strings.Split(pkg, "/")
		target = segs[len(segs)-1]
	}
	if patternTargets[target] {
		return "", fmt.Errorf("invalid bazel label %q: %q is a target pattern, and a source names one target, not a pattern: %s", label, target, labelSyntax)
	}
	return repo + "//" + pkg + ":" + target, nil
}

//go:embed closure.cquery
var closureCquery []byte

// One file of a ProtoInfo closure, as closure.cquery reports it.
type protoFile struct {
	Name   string `json:"name"` // import name
	Path   string `json:"path"` // exec path
	Source bool   `json:"source"`
	Owner  string `json:"owner"`
}

// What loading says about one label: a rule's class, import_prefix and srcs, or a source file's
// absolute path. A generated file has none.
type target struct {
	Class, Prefix string
	Srcs          []string
	Location      string
}

// Closure maps every import name in label's ProtoInfo closure to its absolute path, from analysis and
// loading alone: a build would run the user's --aspects and validations on someone else's protos.
func (b Builder) Closure(ctx context.Context, label string) (map[string]string, error) {
	canon, err := CanonicalLabel(label)
	if err != nil {
		return nil, err
	}
	r, cancel, err := b.session(ctx, canon)
	if err != nil {
		return nil, err
	}
	defer cancel()

	files, err := r.cquery(canon)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("bazel target %s carries no ProtoInfo, nor do its direct deps; it must be a proto_library or a proto_descriptor_set", canon)
	}
	q, err := r.query(owners(files))
	if err != nil {
		return nil, err
	}
	out, err := locate(b.Root, files, q)
	if err != nil {
		return nil, fmt.Errorf("bazel target %s: %w", canon, err)
	}
	return out, nil
}

// depth 1 covers a proto_descriptor_set, whose deps carry its ProtoInfo; config(…, target) drops the
// copies --aspects analyze in other configurations.
func (r runner) cquery(canon string) ([]protoFile, error) {
	dir, err := os.MkdirTemp("", "grpcview-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	file := filepath.Join(dir, "closure.cquery")
	if err := os.WriteFile(file, closureCquery, 0o600); err != nil {
		return nil, err
	}
	stdout, err := r.run("cquery", []string{"--output=starlark", "--starlark:file=" + file},
		fmt.Sprintf("config(deps(%s, 1), target)", canon))
	if err != nil {
		return nil, err
	}
	return decodeLines[protoFile](r, stdout)
}

func isMainSource(f protoFile) bool {
	return f.Source && strings.HasPrefix(strings.TrimPrefix(f.Owner, "@@"), "//")
}

// owners are the labels locate needs loading facts about, sorted.
func owners(files []protoFile) []string {
	set := map[string]bool{}
	for _, f := range files {
		if !isMainSource(f) {
			set[f.Owner] = true
		}
	}
	return slices.Sorted(maps.Keys(set))
}

// query loads labels and their srcs.
func (r runner) query(labels []string) (map[string]target, error) {
	if len(labels) == 0 {
		return nil, nil
	}
	expr := "let o = " + strings.Join(labels, " + ") + " in $o + labels(srcs, $o)"
	stdout, err := r.run("query", []string{"--output=streamed_jsonproto", "--consistent_labels", "--order_output=no"}, expr)
	if err != nil {
		return nil, err
	}
	type attr struct {
		Name            string   `json:"name"`
		StringValue     string   `json:"stringValue"`
		StringListValue []string `json:"stringListValue"`
	}
	lines, err := decodeLines[struct {
		Rule *struct {
			Name      string `json:"name"`
			RuleClass string `json:"ruleClass"`
			Attribute []attr `json:"attribute"`
		} `json:"rule"`
		SourceFile *struct{ Name, Location string } `json:"sourceFile"`
	}](r, stdout)
	if err != nil {
		return nil, err
	}
	out := map[string]target{}
	for _, l := range lines {
		switch {
		case l.Rule != nil:
			t := target{Class: l.Rule.RuleClass}
			for _, a := range l.Rule.Attribute {
				switch a.Name {
				case "import_prefix":
					t.Prefix = a.StringValue
				case "srcs":
					t.Srcs = a.StringListValue
				}
			}
			out[l.Rule.Name] = t
		case l.SourceFile != nil:
			// A source file's location is always line 1, column 1.
			out[l.SourceFile.Name] = target{Location: strings.TrimSuffix(l.SourceFile.Location, ":1:1")}
		}
	}
	return out, nil
}

// locate maps each import name in files, first wins, to its absolute path: a main-repo source is under
// root, anything else is found through q's facts about its owner.
func locate(root string, files []protoFile, q map[string]target) (map[string]string, error) {
	out := map[string]string{}
	for _, f := range files {
		if _, ok := out[f.Name]; ok {
			continue
		}
		p, err := locateFile(root, f, q)
		if err != nil {
			return nil, err
		}
		out[f.Name] = p
	}
	return out, nil
}

func locateFile(root string, f protoFile, q map[string]target) (string, error) {
	owner := q[f.Owner]
	switch {
	case isMainSource(f):
		return filepath.Join(root, filepath.FromSlash(f.Path)), nil
	case f.Source:
		if owner.Location == "" {
			return "", fmt.Errorf("bazel query did not locate %s", f.Owner)
		}
		return owner.Location, nil
	case owner.Class != "proto_library":
		return "", generated(f.Path, f.Owner)
	}
	// A virtual import: a symlink, made only by a build, to one of its rule's srcs. Each src is
	// strip_import_prefix/rest, where rest is its import name minus import_prefix, so the shortest src
	// ending in rest is the one.
	rest := strings.TrimPrefix(f.Name, owner.Prefix+"/")
	var src, srcPath string
	for _, s := range owner.Srcs {
		_, p, _ := strings.Cut(s, "//")
		p = strings.TrimPrefix(strings.Replace(p, ":", "/", 1), "/")
		if (p == rest || strings.HasSuffix(p, "/"+rest)) && (src == "" || len(p) < len(srcPath)) {
			src, srcPath = s, p
		}
	}
	switch {
	case src == "":
		return "", fmt.Errorf("%s exposes %s, but none of its srcs ends in %s", f.Owner, f.Name, rest)
	case q[src].Location == "":
		return "", generated(src, f.Owner)
	}
	return q[src].Location, nil
}

func generated(file, owner string) error {
	return fmt.Errorf("%s (of %s) is generated, and resolving it would need a build, which grpcview never runs: depend on a checked-in .proto instead", file, owner)
}

func decodeLines[T any](r runner, stdout string) ([]T, error) {
	var out []T
	for _, line := range splitLines(stdout) {
		var v T
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			return nil, fmt.Errorf("bazel output for %s: unparseable line %q: %w", r.subject, line, err)
		}
		out = append(out, v)
	}
	return out, nil
}

// QueryTargets' kinds. Deliberately exact rather than something like `.*proto.*`: go_proto_library
// and cc_proto_library carry "proto" in their kind but are not what a source names.
const descriptorSetKinds = `proto_library|proto_descriptor_set`

// Runs no actions, but `bazel query` still loads BUILD files and can fetch external repos — code from
// this repo — so THE CALLER MUST HAVE CHECKED TRUST here too. A partial result is a listing plus a
// warning, never an error: one unloadable package must not blank a picker.
func (b Builder) QueryTargets(ctx context.Context) ([]string, string, error) {
	expr := fmt.Sprintf(`kind("^(%s) rule$", //...)`, descriptorSetKinds)
	r, cancel, err := b.session(ctx, expr)
	if err != nil {
		return nil, "", err
	}
	defer cancel()
	stdout, err := r.run("query", []string{"--output=label", "--order_output=no", "--keep_going"}, expr)

	labels, seen := []string(nil), map[string]bool{}
	for _, line := range splitLines(stdout) {
		canon, cerr := CanonicalLabel(line)
		if cerr != nil || seen[canon] {
			continue
		}
		seen[canon] = true
		labels = append(labels, canon)
	}
	sort.Strings(labels)

	if err != nil {
		if len(labels) == 0 {
			return nil, "", err
		}
		return labels, err.Error(), nil
	}
	return labels, "", nil
}

// One bazel session's invocations. `subject` names the target in error messages only — with a set of
// labels after "--", no single argument is the thing the user asked about.
type runner struct {
	b           Builder
	parent, ctx context.Context
	timeout     time.Duration
	subject     string
}

// session bounds a run of invocations about subject by b's timeout; the caller must cancel it.
func (b Builder) session(ctx context.Context, subject string) (runner, context.CancelFunc, error) {
	if b.Root == "" {
		return runner{}, nil, errors.New("no bazel workspace root: no MODULE.bazel, WORKSPACE or WORKSPACE.bazel above this workspace")
	}
	timeout := b.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	return runner{b: b, parent: ctx, ctx: runCtx, timeout: timeout, subject: subject}, cancel, nil
}

// stdout comes back on the error paths too, for the one caller that can use a partial answer
// (QueryTargets). stdout and stderr are captured, never inherited: this runs inside a server.
func (r runner) run(verb string, extra []string, targets ...string) (string, error) {
	binary := r.b.Binary
	if binary == "" {
		binary = "bazel"
	}
	args := append([]string{verb}, extra...)
	args = append(args, commonArgs...)
	args = append(args, "--")
	args = append(args, targets...)

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(r.ctx, binary, args...)
	cmd.Dir = r.b.Root
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Killing bazel on the timeout kills only bazel itself; anything it spawned
	// keeps the captured stdout/stderr pipes open, and Wait blocks on those until
	// the last one exits. Without a delay the timeout bounds nothing.
	cmd.WaitDelay = 2 * time.Second

	err := cmd.Run()
	out := stdout.String()
	switch {
	case err == nil:
		return out, nil
	case r.parent.Err() != nil:
		return out, fmt.Errorf("bazel %s of %s was cancelled: %w", verb, r.subject, r.parent.Err())
	case errors.Is(r.ctx.Err(), context.DeadlineExceeded):
		return out, fmt.Errorf("bazel %s of %s exceeded the %s timeout; raise bazel.timeout_seconds in grpcview.work.json if it really takes this long", verb, r.subject, r.timeout)
	}
	if tail := tailOf(stderr.String()); tail != "" {
		return out, fmt.Errorf("bazel %s of %s failed: %w\n%s", verb, r.subject, err, tail)
	}
	return out, fmt.Errorf("bazel %s of %s failed: %w", verb, r.subject, err)
}

func splitLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

const (
	maxTailLines = 20
	maxTailBytes = 2 << 10
)

func tailOf(s string) string {
	s = strings.TrimRight(s, "\n \t\r")
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > maxTailLines {
		lines = lines[len(lines)-maxTailLines:]
	}
	tail := strings.Join(lines, "\n")
	if len(tail) > maxTailBytes {
		tail = tail[len(tail)-maxTailBytes:]
	}
	return strings.TrimSpace(tail)
}
