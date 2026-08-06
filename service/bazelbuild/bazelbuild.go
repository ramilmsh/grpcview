// Package bazelbuild turns a bazel label into the descriptor sets its default
// outputs name. It knows nothing about grpcview's protos, store or RPCs: it is
// the mechanism, and the descriptor-source resolve path is its only caller.
//
// Two responsibilities that are easy to conflate live here:
//
//   - Label safety. A label reaches this package from a committed
//     `grpcview.json` a colleague wrote, so it is untrusted text. Nothing here
//     ever builds a shell string — bazel is exec'd with an argv slice and a `--`
//     before the label, so a label can never arrive as a flag
//     (`//x --output_base=/tmp`) — and CanonicalLabel rejects anything that is
//     not a plain label.
//   - Locating the outputs. `bazel cquery --output=files` is asked where they
//     landed rather than assembling `bazel-bin/<pkg>/<name>-descriptor-set.proto.bin`
//     by hand, which would bake in both the rule's output naming and
//     `--symlink_prefix`.
//
// Two things this package deliberately does NOT do:
//
//   - It does not check trust. Building a label runs arbitrary build code (bazel
//     actions are not guaranteed to be sandboxed), so THE CALLER MUST HAVE
//     CHECKED that the workspace is trusted before calling DescriptorSets. The
//     gate lives next to the resolve path that decides to acquire; this package
//     is only the exec point's mechanism.
//   - It does not dedupe or link. THE CALLER dedupes by proto file name before
//     linking: a merging rule concatenates its inputs' per-target sets, so the
//     same file name can appear in more than one returned set (or twice in one),
//     and desc.CreateFileDescriptorsFromSet rejects a duplicate file name.
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

// DefaultTimeout bounds a build+cquery pair when Builder.Timeout is zero. A
// descriptor build is seconds in practice; the timeout is for the pathological
// case (a cold server, a huge dependency graph, a wedged remote cache).
const DefaultTimeout = 10 * time.Minute

// Builder builds a label's descriptor sets. Zero Timeout means DefaultTimeout.
type Builder struct {
	Binary  string        // "bazel" when empty
	Root    string        // the bazel workspace root: the process cwd for every invocation
	Timeout time.Duration
}

// commonArgs are passed to BOTH invocations, identically and on purpose: cquery
// reports the output path of the configuration it is asked about, so a flag that
// differs between the two would report a path the build never wrote.
var commonArgs = []string{"--curses=no", "--color=no", "--noshow_progress"}

// rootMarkers name a bazel workspace root, newest spelling first.
var rootMarkers = []string{"MODULE.bazel", "WORKSPACE", "WORKSPACE.bazel"}

// FindRoot returns the nearest ancestor of start (start included) holding
// MODULE.bazel, WORKSPACE or WORKSPACE.bazel, or "" when there is none.
//
// Walking is deliberate: `bazel info workspace` would answer the same question
// by starting a server, and this has to be cheap enough to call on every load.
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

// labelRe matches `@repo//pkg/sub:target`, with the repo, the `//` and the
// `:target` each optional. Every accepted character is explicit — letters,
// digits, `_`, `-`, `.`, `+` and `/` — and each of the package and target parts
// must START with a letter or digit, which is what rejects a label that is
// really a flag (`--output_base=/tmp`) as well as `.`/`..` traversal in the
// first segment. Anything else (a space, a newline, `$`, a quote, a backtick,
// `*`, `...`) simply does not match.
//
// The repo part takes a second optional `@` and, inside the name, `+` and `~`,
// because that is the canonical repo spelling bzlmod prints and therefore the
// one a user pastes: `@@rules_go+//go:def.bzl` on bazel 7.2+, `@@rules_go~0.46.0//…`
// before it. A repo still has to be followed by `//`.
var labelRe = regexp.MustCompile(`^(@@?[A-Za-z0-9][A-Za-z0-9._+~-]*)?(//)?([A-Za-z0-9][A-Za-z0-9._+/-]*)?(?::([A-Za-z0-9][A-Za-z0-9._+/-]*))?$`)

const labelSyntax = `a label looks like //pkg/sub:target, //pkg, pkg:target, @repo//pkg:target or ` +
	`@@canonical_repo+//pkg:target (letters, digits, "_", "-", ".", "+" and "/" only, plus "~" in a repo name)`

// patternTargets are bazel's target-pattern wildcards, which expand to every
// target in a package. They are not targets and this package resolves one
// target; `...` is rejected as a path segment instead, next to `..`.
var patternTargets = map[string]bool{"all": true, "all-targets": true, "*": true}

// CanonicalLabel validates label and returns its canonical `@repo//pkg:target`
// spelling. Canonicalizing BEFORE any id is derived from a label is load
// bearing: `//pkg` and `//pkg:pkg` are one target, and if both spellings could
// reach the store they would become two sources, breaking "re-adding an id
// refreshes that source in place".
func CanonicalLabel(label string) (string, error) {
	// Spaces and tabs around a hand-typed label are trimmed; a newline, a CR or
	// any other whitespace is NOT, and falls through to the regex as a rejection.
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
		// ":target" — a target with no package. Rejected rather than resolved
		// against a "current package", because this package has no such notion.
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
			// "." and ".." are traversal; "..." is bazel's recursive wildcard,
			// and this package resolves one target, never a pattern.
			if strings.Trim(seg, ".") == "" {
				return "", fmt.Errorf("invalid bazel label %q: %q is not a path segment: %s", label, seg, labelSyntax)
			}
		}
	}

	if target == "" {
		// bazel's shorthand: //a/b means //a/b:b — the LAST path segment, not
		// the whole package path.
		if pkg == "" {
			return "", fmt.Errorf("invalid bazel label %q: no target: %s", label, labelSyntax)
		}
		segs := strings.Split(pkg, "/")
		target = segs[len(segs)-1]
	}
	// Checked on the RESOLVED target, so the shorthand cannot smuggle a pattern
	// in either (`//pkg/all` means `//pkg/all:all`). `bazel build -- //pkg:all`
	// builds every target in the package and `cquery --output=files` then lists
	// all of their outputs, so a pattern here would resolve a source to whatever
	// the package happens to contain.
	if patternTargets[target] {
		return "", fmt.Errorf("invalid bazel label %q: %q is a target pattern, and a source names one target, not a pattern: %s", label, target, labelSyntax)
	}
	return repo + "//" + pkg + ":" + target, nil
}

// DescriptorSets builds label, then reads every descriptor set its default
// outputs name, in the order cquery printed them. Duplicates across (and within)
// the returned sets are expected; see the package comment on who dedupes.
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

	if _, err := b.run(ctx, runCtx, timeout, canon, "build"); err != nil {
		return nil, err
	}
	stdout, err := b.run(ctx, runCtx, timeout, canon, "cquery", "--output=files")
	if err != nil {
		return nil, err
	}

	paths := splitLines(stdout)
	if len(paths) == 0 {
		return nil, fmt.Errorf("bazel target %s has no output files; it must be a target whose default outputs are FileDescriptorSets (a proto_library or a proto_descriptor_set)", canon)
	}

	sets := make([]*descriptorpb.FileDescriptorSet, 0, len(paths))
	for _, rel := range paths {
		// cquery prints workspace-root-relative paths (into bazel-out/, i.e.
		// through the convenience symlink). These come from bazel, not from a
		// human, so there is nothing to confine — unlike an upload's path.
		full := rel
		if !filepath.IsAbs(full) {
			full = filepath.Join(b.Root, rel)
		}
		raw, err := os.ReadFile(full)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				// A build with --remote_download_minimal succeeds without
				// materialising outputs locally. Say so instead of silently
				// adding download flags to the user's build.
				return nil, fmt.Errorf("bazel target %s built, but its output %s does not exist locally; if you build with remote execution, add --remote_download_toplevel so the descriptor set is downloaded", canon, rel)
			}
			return nil, fmt.Errorf("failed to read bazel output %s of %s: %w", rel, canon, err)
		}
		set := &descriptorpb.FileDescriptorSet{}
		if err := proto.Unmarshal(raw, set); err != nil {
			return nil, fmt.Errorf("bazel output %s of %s is not a FileDescriptorSet: %w", rel, canon, err)
		}
		if len(set.GetFile()) == 0 {
			// proto.Unmarshal succeeds on almost any bytes (unknown fields are
			// kept, not rejected), so an empty set is the only signal that the
			// label's outputs are not descriptor sets at all — a go_binary, say.
			// Without this the source resolves to "0 files" and no error.
			return nil, fmt.Errorf("bazel output %s of %s contains no proto files; it must be a target whose default outputs are FileDescriptorSets (a proto_library or a proto_descriptor_set)", rel, canon)
		}
		sets = append(sets, set)
	}
	return sets, nil
}

// descriptorSetKinds is the rule-kind alternation QueryTargets matches: the two rules
// whose DEFAULT OUTPUTS are FileDescriptorSets. It is deliberately exact rather than
// something like `.*proto.*` — `go_proto_library`, `cc_proto_library` and friends all
// carry "proto" in their kind and none of them outputs a descriptor set, so a loose
// pattern would fill the picker with targets that fail the moment they are added.
//
// A ruleset this list does not know about is exactly why the label field it feeds stays
// free text: the picker is a shortcut, never the set of legal answers.
const descriptorSetKinds = `proto_library|proto_descriptor_set`

// QueryTargets lists the main repository's descriptor-set-producing labels, canonical,
// sorted and deduped, plus a warning when bazel reported trouble the query survived.
//
// Partial results are returned rather than discarded, and that is the whole design of
// the second return value: this feeds a picker, and one unloadable package in a monorepo
// must not blank out every target the query DID find. The caller shows the warning and
// keeps the labels.
//
// Unlike DescriptorSets this runs no actions — but `bazel query` still loads BUILD files
// and can fetch external repositories, i.e. it executes this repo's code, so THE CALLER
// MUST HAVE CHECKED TRUST here too (see the package comment).
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

	// kind() matches against bazel's own "<rule class> rule" spelling, anchored so
	// `proto_library` cannot also match `go_proto_library`. The expression is a constant
	// assembled here and never client input; --order_output=no skips a graph sort whose
	// result is thrown away by the sort below.
	expr := fmt.Sprintf(`kind("^(%s) rule$", //...)`, descriptorSetKinds)
	stdout, err := b.run(ctx, runCtx, timeout, expr, "query", "--output=label", "--order_output=no", "--keep_going")

	labels, seen := []string(nil), map[string]bool{}
	for _, line := range splitLines(stdout) {
		// A label bazel printed is well-formed by construction; canonicalizing is for the
		// ids derived from it downstream, where "//pkg" and "//pkg:pkg" must be one source.
		canon, cerr := CanonicalLabel(line)
		if cerr != nil || seen[canon] {
			continue
		}
		seen[canon] = true
		labels = append(labels, canon)
	}
	sort.Strings(labels)

	if err != nil {
		// Any nonzero exit with labels on stdout is the --keep_going partial case, whatever
		// code bazel chose for it; with nothing on stdout there is no listing to salvage.
		if len(labels) == 0 {
			return nil, "", err
		}
		return labels, err.Error(), nil
	}
	return labels, "", nil
}

// run execs one bazel invocation with cmd.Dir = b.Root and returns its stdout. That
// stdout comes back on the error paths too, for the one caller that can use a partial
// answer (QueryTargets); every other caller checks err first and ignores it.
// stdout and stderr are captured, never inherited: this runs inside a server.
func (b Builder) run(parent, ctx context.Context, timeout time.Duration, label, verb string, extra ...string) (string, error) {
	binary := b.Binary
	if binary == "" {
		binary = "bazel"
	}
	// The argv is assembled, never a shell string, and "--" always precedes the
	// label so a label can never be read as a flag.
	args := append([]string{verb}, extra...)
	args = append(args, commonArgs...)
	args = append(args, "--", label)

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = b.Root
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	out := stdout.String()
	switch {
	case err == nil:
		return out, nil
	case parent.Err() != nil:
		// The caller went away (a cancelled request); not our timeout.
		return out, fmt.Errorf("bazel %s of %s was cancelled: %w", verb, label, parent.Err())
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return out, fmt.Errorf("bazel %s of %s exceeded the %s timeout; raise bazel.timeout_seconds in grpcview.work.json if the build really takes this long", verb, label, timeout)
	}
	if tail := tailOf(stderr.String()); tail != "" {
		return out, fmt.Errorf("bazel %s of %s failed: %w\n%s", verb, label, err, tail)
	}
	return out, fmt.Errorf("bazel %s of %s failed: %w", verb, label, err)
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

// tailOf returns the last maxTailLines lines of s, capped at maxTailBytes. The
// tail is what carries a bazel failure's actual reason; the head is startup
// noise ("Starting local Bazel server", "Computing main repo mapping").
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
