// Package bazelbuild turns a bazel label into the descriptor sets its default outputs name. It is the
// mechanism, and knows nothing about grpcview's protos, store or RPCs.

package bazelbuild

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

const DefaultTimeout = 10 * time.Minute

type Builder struct {
	Binary  string
	Root    string
	Timeout time.Duration
}

// Passed to EVERY invocation, identically and on purpose: cquery reports the output path of the
// configuration it is asked about, so a flag differing from the build's would report a path the build
// never wrote.
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

func (b Builder) DescriptorSets(ctx context.Context, label string) ([]*descriptorpb.FileDescriptorSet, error) {
	canon, err := CanonicalLabel(label)
	if err != nil {
		return nil, err
	}
	if b.Root == "" {
		return nil, errors.New("no bazel workspace root: no MODULE.bazel, WORKSPACE or WORKSPACE.bazel above this workspace")
	}

	timeout := b.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	targets, err := b.protoClosure(ctx, runCtx, timeout, canon)
	if err != nil {
		return nil, err
	}

	if _, err := b.run(ctx, runCtx, timeout, canon, "build", nil, targets...); err != nil {
		return nil, err
	}
	// cquery takes ONE expression, so the closure is unioned into it; `bazel build` takes patterns and
	// gets them as separate arguments. Same configuration in both, see commonArgs.
	stdout, err := b.run(ctx, runCtx, timeout, canon, "cquery", []string{"--output=files"}, strings.Join(targets, " + "))
	if err != nil {
		return nil, err
	}

	paths := splitLines(stdout)
	if len(paths) == 0 {
		return nil, fmt.Errorf("bazel target %s has no output files; it must be a target whose default outputs are FileDescriptorSets (a proto_library or a proto_descriptor_set)", canon)
	}

	sets := make([]*descriptorpb.FileDescriptorSet, 0, len(paths))
	for _, rel := range paths {
		full := rel
		if !filepath.IsAbs(full) {
			full = filepath.Join(b.Root, rel)
		}
		raw, err := os.ReadFile(full)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("bazel target %s built, but its output %s does not exist locally; if you build with remote execution, add --remote_download_toplevel so the descriptor set is downloaded", canon, rel)
			}
			return nil, fmt.Errorf("failed to read bazel output %s of %s: %w", rel, canon, err)
		}
		set := &descriptorpb.FileDescriptorSet{}
		if err := proto.Unmarshal(raw, set); err != nil {
			return nil, fmt.Errorf("bazel output %s of %s is not a FileDescriptorSet: %w", rel, canon, err)
		}
		// proto.Unmarshal succeeds on almost any bytes (unknown fields are kept, not rejected), so an empty
		// set is the only signal that the label's outputs are not descriptor sets at all — a go_binary, say.
		if len(set.GetFile()) == 0 {
			return nil, fmt.Errorf("bazel output %s of %s contains no proto files; it must be a target whose default outputs are FileDescriptorSets (a proto_library or a proto_descriptor_set)", rel, canon)
		}
		sets = append(sets, set)
	}
	return sets, nil
}

// Deliberately exact rather than something like `.*proto.*`: go_proto_library and cc_proto_library
// carry "proto" in their kind and output no descriptor set, so a loose pattern would fill the picker
// with targets that fail the moment they are added.
const descriptorSetKinds = `proto_library|proto_descriptor_set`

// The label, then every descriptor-set-producing target below it. Load-bearing, not an optimization: a
// proto_library's descriptor set holds ONLY the files that target declares, so a label whose protos
// import another target's — google/protobuf/any.proto, most often — links to nothing on its own
// ("no such file: google/protobuf/any.proto"). Reading the closure's sets and deduping by file name
// (the caller's job) is what reconstitutes the transitive set protoc --include_imports would emit.
//
// A dep whose kind produces no descriptor set is filtered out here rather than tolerated later,
// because an output that is not a FileDescriptorSet is a hard error further down.
func (b Builder) protoClosure(parent, ctx context.Context, timeout time.Duration, canon string) ([]string, error) {
	expr := fmt.Sprintf(`kind("^(%s) rule$", deps(%s))`, descriptorSetKinds, canon)
	stdout, err := b.run(parent, ctx, timeout, canon, "query", []string{"--output=label", "--order_output=no"}, expr)
	if err != nil {
		return nil, err
	}
	// canon leads, and stays even if the query listed nothing: its own outputs are what the user asked
	// for, and a rule kind this query does not recognize can still emit a descriptor set.
	targets, seen := []string{canon}, map[string]bool{canon: true}
	for _, line := range splitLines(stdout) {
		dep, cerr := CanonicalLabel(line)
		if cerr != nil || seen[dep] {
			continue
		}
		seen[dep] = true
		targets = append(targets, dep)
	}
	return targets, nil
}

// Runs no actions, but `bazel query` still loads BUILD files and can fetch external repos — code from
// this repo — so THE CALLER MUST HAVE CHECKED TRUST here too. A partial result is a listing plus a
// warning, never an error: one unloadable package must not blank a picker.
func (b Builder) QueryTargets(ctx context.Context) ([]string, string, error) {
	if b.Root == "" {
		return nil, "", errors.New("no bazel workspace root: no MODULE.bazel, WORKSPACE or WORKSPACE.bazel above this workspace")
	}
	timeout := b.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	expr := fmt.Sprintf(`kind("^(%s) rule$", //...)`, descriptorSetKinds)
	stdout, err := b.run(ctx, runCtx, timeout, expr, "query", []string{"--output=label", "--order_output=no", "--keep_going"}, expr)

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

// `subject` names the target in error messages only — with a closure of labels after "--", no single
// argument is the thing the user asked about.
//
// stdout comes back on the error paths too, for the one caller that can use a partial answer
// (QueryTargets). stdout and stderr are captured, never inherited: this runs inside a server.
func (b Builder) run(parent, ctx context.Context, timeout time.Duration, subject, verb string, extra []string, targets ...string) (string, error) {
	binary := b.Binary
	if binary == "" {
		binary = "bazel"
	}
	args := append([]string{verb}, extra...)
	args = append(args, commonArgs...)
	args = append(args, "--")
	args = append(args, targets...)

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = b.Root
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
	case parent.Err() != nil:
		return out, fmt.Errorf("bazel %s of %s was cancelled: %w", verb, subject, parent.Err())
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return out, fmt.Errorf("bazel %s of %s exceeded the %s timeout; raise bazel.timeout_seconds in grpcview.work.json if the build really takes this long", verb, subject, timeout)
	}
	if tail := tailOf(stderr.String()); tail != "" {
		return out, fmt.Errorf("bazel %s of %s failed: %w\n%s", verb, subject, err, tail)
	}
	return out, fmt.Errorf("bazel %s of %s failed: %w", verb, subject, err)
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
