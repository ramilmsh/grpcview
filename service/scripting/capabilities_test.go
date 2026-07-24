package scripting

// Capability-layer tests, exercised through the production Engine.RunScenario path (both
// gates: esbuild Gate 1 at compile time + the Go host functions' Gate 2 at call time).
//
// Dropped in the port from the legacy string API: the Gate-2-deny-stub test (it needed the
// legacy API's ability to bundle WITH a grant but evaluate under a DIFFERENT one, which the
// production API deliberately does not expose) and the marshalling-cost timing benchmark
// (it used per-call EvalWithGrant isolation).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile creates a file with contents under dir and returns its path.
func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// jsonString decodes a Result's value as a JSON string literal (a script whose last
// expression is a string comes back JSON-encoded) into the Go string it represents.
func jsonString(t *testing.T, res Result) string {
	t.Helper()
	var s string
	if err := json.Unmarshal(res.Value, &s); err != nil {
		t.Fatalf("value %s is not a JSON string: %v", res.Value, err)
	}
	return s
}

// TestInertModuleNoGrant: an INERT node:* module (pure computation, no capability)
// is usable with an EMPTY grant — no import gate, no host function, no privilege.
func TestInertModuleNoGrant(t *testing.T) {
	e := newEngine(t)
	res, err := e.RunScenario(context.Background(),
		`import path from "node:path"; path.join("a", "b", "c")`,
		Grant{}, Input{})
	if err != nil {
		t.Fatalf("inert module under empty grant: %v", err)
	}
	if got := jsonString(t, res); got != "a/b/c" {
		t.Fatalf("path.join = %q, want %q", got, "a/b/c")
	}
}

// TestFSReadGrantedInScope: the full end-to-end capability path — JS fs.readFileSync
// -> C marshaller -> host_fs_read import -> Go grant+scope check -> os.ReadFile ->
// contents back to JS across linear memory.
func TestFSReadGrantedInScope(t *testing.T) {
	e := newEngine(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "hello.txt")
	writeFile(t, file, "file contents ☑")

	grant := Grant{FS: &FSGrant{AllowedPaths: []string{dir}}}
	res, err := e.RunScenario(context.Background(),
		fmt.Sprintf(`import fs from "node:fs"; fs.readFileSync(%q)`, file),
		grant, Input{})
	if err != nil {
		t.Fatalf("granted in-scope read: %v", err)
	}
	if got := jsonString(t, res); got != "file contents ☑" {
		t.Fatalf("readFileSync = %q, want file contents", got)
	}
}

// TestFSReadOutOfScopeRefusedInGo: a granted fs cap whose scope does NOT cover the
// requested path is refused — and the refusal happens in Go (the host function), not
// in the sandbox. The error text originates from fsRead's allowlist check.
func TestFSReadOutOfScopeRefusedInGo(t *testing.T) {
	e := newEngine(t)
	dir := t.TempDir()
	allowed := filepath.Join(dir, "ok")
	if err := os.Mkdir(allowed, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(allowed, "in.txt"), "in scope")
	secret := filepath.Join(dir, "secret.txt") // sibling of the allowed dir, NOT under it
	writeFile(t, secret, "TOP SECRET")

	grant := Grant{FS: &FSGrant{AllowedPaths: []string{allowed}}}

	// In scope still works.
	res, err := e.RunScenario(context.Background(),
		fmt.Sprintf(`import fs from "node:fs"; fs.readFileSync(%q)`, filepath.Join(allowed, "in.txt")),
		grant, Input{})
	if err != nil {
		t.Fatalf("in-scope read: %v", err)
	}
	if got := jsonString(t, res); got != "in scope" {
		t.Fatalf("in-scope read = %q, want %q", got, "in scope")
	}

	// Out of scope is refused as a JS exception carrying the Go-side reason.
	_, err = e.RunScenario(context.Background(),
		fmt.Sprintf(`import fs from "node:fs"; fs.readFileSync(%q)`, secret),
		grant, Input{})
	var jsErr *JSError
	if !errors.As(err, &jsErr) {
		t.Fatalf("out-of-scope read: got %v, want a *JSError", err)
	}
	if !strings.Contains(jsErr.Message, "not in allowlist") {
		t.Fatalf("out-of-scope error = %q, want it to mention the allowlist", jsErr.Message)
	}
	// Prove the secret never crossed: the message must not contain the file bytes.
	if strings.Contains(jsErr.Message, "TOP SECRET") {
		t.Fatalf("out-of-scope refusal leaked file contents: %q", jsErr.Message)
	}
}

// TestFSUngrantedGate1Bundle: WITHOUT the fs grant, GATE 1 refuses to resolve the
// node:fs import at bundle time — the script cannot be assembled, so no call site
// exists. This is the strongest "genuinely absent": nothing reaches the host at all.
func TestFSUngrantedGate1Bundle(t *testing.T) {
	e := newEngine(t)
	src := `import fs from "node:fs"; fs.readFileSync("/etc/passwd")`

	// RunScenario surfaces the Gate 1 denial at compile/bundle time — it never
	// instantiates or evaluates.
	if _, err := e.RunScenario(context.Background(), src, Grant{}, Input{}); err == nil ||
		!strings.Contains(err.Error(), "capability not granted") {
		t.Fatalf("RunScenario without fs grant: got %v, want a Gate 1 'capability not granted' denial", err)
	}
}

// TestHostFailureIsCatchable: a host-side failure surfaces as a normal, catchable JS
// exception (not a trap, not a silent zero) — a script can try/catch it and inspect
// the message. This pins the ABI's error-propagation guarantee.
func TestHostFailureIsCatchable(t *testing.T) {
	e := newEngine(t)
	dir := t.TempDir()
	elsewhere := t.TempDir() // granted scope, unrelated to the file we try to read
	secret := filepath.Join(dir, "secret.txt")
	writeFile(t, secret, "unreadable")

	grant := Grant{FS: &FSGrant{AllowedPaths: []string{elsewhere}}}
	src := fmt.Sprintf(`import fs from "node:fs";
let out;
try { fs.readFileSync(%q); out = "NOT REACHED" }
catch (e) { out = "caught:" + e.message }
out`, secret)

	res, err := e.RunScenario(context.Background(), src, grant, Input{})
	if err != nil {
		t.Fatalf("catchable host failure: unexpected Go error %v", err)
	}
	got := jsonString(t, res)
	if !strings.HasPrefix(got, "caught:") || !strings.Contains(got, "not in allowlist") {
		t.Fatalf("catch result = %q, want a caught 'not in allowlist' message", got)
	}
}

// TestNetStubGeneralizes: the SAME uniform ABI carries a second capability. The net
// import is stubbed (no real network) but proves the pattern is not fs-specific:
// granted -> a value crosses back; the deny path is exercised by the ungranted case.
func TestNetStubGeneralizes(t *testing.T) {
	e := newEngine(t)

	// Granted: a value crosses back through the identical [tag|len|payload] envelope.
	res, err := e.RunScenario(context.Background(),
		`import net from "node:net"; net.fetch("hello")`,
		Grant{Net: &NetGrant{}}, Input{})
	if err != nil {
		t.Fatalf("granted net stub: %v", err)
	}
	if got := jsonString(t, res); got != "stub-fetched:hello" {
		t.Fatalf("net.fetch = %q, want %q", got, "stub-fetched:hello")
	}

	// Ungranted: Gate 1 refuses to resolve node:net at bundle time.
	if _, err := e.RunScenario(context.Background(),
		`import net from "node:net"; net.fetch("hello")`, Grant{}, Input{}); err == nil ||
		!strings.Contains(err.Error(), "capability not granted") {
		t.Fatalf("ungranted net: got %v, want Gate 1 denial", err)
	}
}
