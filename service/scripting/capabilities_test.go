package scripting

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeFile creates a file with contents under dir and returns its path.
func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestInertModuleNoGrant: an INERT node:* module (pure computation, no capability)
// is usable with an EMPTY grant — no import gate, no host function, no privilege.
func TestInertModuleNoGrant(t *testing.T) {
	rt := newRuntime(t, generousPages)
	got, err := rt.RunScript(context.Background(),
		`import path from "node:path"; path.join("a", "b", "c")`,
		Grant{}, 0)
	if err != nil {
		t.Fatalf("inert module under empty grant: %v", err)
	}
	if got != "a/b/c" {
		t.Fatalf("path.join = %q, want %q", got, "a/b/c")
	}
}

// TestFSReadGrantedInScope: the full end-to-end capability path — JS fs.readFileSync
// -> C marshaller -> host_fs_read import -> Go grant+scope check -> os.ReadFile ->
// contents back to JS across linear memory.
func TestFSReadGrantedInScope(t *testing.T) {
	rt := newRuntime(t, generousPages)
	dir := t.TempDir()
	file := filepath.Join(dir, "hello.txt")
	writeFile(t, file, "file contents ☑")

	grant := Grant{FS: &FSGrant{AllowedPaths: []string{dir}}}
	got, err := rt.RunScript(context.Background(),
		fmt.Sprintf(`import fs from "node:fs"; fs.readFileSync(%q)`, file),
		grant, 0)
	if err != nil {
		t.Fatalf("granted in-scope read: %v", err)
	}
	if got != "file contents ☑" {
		t.Fatalf("readFileSync = %q, want file contents", got)
	}
}

// TestFSReadOutOfScopeRefusedInGo: a granted fs cap whose scope does NOT cover the
// requested path is refused — and the refusal happens in Go (the host function), not
// in the sandbox. The error text originates from fsRead's allowlist check.
func TestFSReadOutOfScopeRefusedInGo(t *testing.T) {
	rt := newRuntime(t, generousPages)
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
	if got, err := rt.RunScript(context.Background(),
		fmt.Sprintf(`import fs from "node:fs"; fs.readFileSync(%q)`, filepath.Join(allowed, "in.txt")),
		grant, 0); err != nil || got != "in scope" {
		t.Fatalf("in-scope read = %q, %v; want %q", got, err, "in scope")
	}

	// Out of scope is refused as a JS exception carrying the Go-side reason.
	_, err := rt.RunScript(context.Background(),
		fmt.Sprintf(`import fs from "node:fs"; fs.readFileSync(%q)`, secret),
		grant, 0)
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
	src := `import fs from "node:fs"; fs.readFileSync("/etc/passwd")`

	if _, err := Bundle(src, Grant{}); err == nil ||
		!strings.Contains(err.Error(), "capability not granted") {
		t.Fatalf("Bundle without fs grant: got %v, want a 'capability not granted' resolve failure", err)
	}

	// RunScript surfaces the same Gate 1 denial (it never instantiates or evaluates).
	rt := newRuntime(t, generousPages)
	if _, err := rt.RunScript(context.Background(), src, Grant{}, 0); err == nil ||
		!strings.Contains(err.Error(), "capability not granted") {
		t.Fatalf("RunScript without fs grant: got %v, want Gate 1 denial", err)
	}
}

// TestFSUngrantedGate2DenyStub: the SECOND, independent gate. Even if a call site
// exists (we bundle WITH the grant to inject the shim), evaluating under a grant that
// lacks fs is refused by the host function BEFORE any syscall — the import is present
// but denies, it does not silently return empty.
func TestFSUngrantedGate2DenyStub(t *testing.T) {
	rt := newRuntime(t, generousPages)
	dir := t.TempDir()
	file := filepath.Join(dir, "hello.txt")
	writeFile(t, file, "should never be read")

	granted := Grant{FS: &FSGrant{AllowedPaths: []string{dir}}}
	src := fmt.Sprintf(`import fs from "node:fs"; fs.readFileSync(%q)`, file)

	// Gate 1 passes (fs granted for bundling) so the call site exists in the bundle.
	bundled, err := Bundle(src, granted)
	if err != nil {
		t.Fatalf("bundle with grant: %v", err)
	}

	inst, err := rt.Instantiate(context.Background())
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer inst.Close(context.Background())

	// ...but we evaluate under an EMPTY grant: Gate 2 denies at the host boundary.
	_, err = inst.EvalWithGrant(context.Background(), bundled, Grant{}, 0)
	var jsErr *JSError
	if !errors.As(err, &jsErr) {
		t.Fatalf("Gate 2 deny: got %v, want a *JSError", err)
	}
	if !strings.Contains(jsErr.Message, `capability "fs" not granted`) {
		t.Fatalf("Gate 2 error = %q, want a not-granted denial", jsErr.Message)
	}
	if strings.Contains(jsErr.Message, "should never be read") {
		t.Fatalf("deny-stub leaked file contents: %q", jsErr.Message)
	}
}

// TestHostFailureIsCatchable: a host-side failure surfaces as a normal, catchable JS
// exception (not a trap, not a silent zero) — a script can try/catch it and inspect
// the message. This pins the ABI's error-propagation guarantee.
func TestHostFailureIsCatchable(t *testing.T) {
	rt := newRuntime(t, generousPages)
	dir := t.TempDir()
	elsewhere := t.TempDir() // granted scope, unrelated to the file we try to read
	secret := filepath.Join(dir, "secret.txt")
	writeFile(t, secret, "unreadable")

	grant := Grant{FS: &FSGrant{AllowedPaths: []string{elsewhere}}}
	src := fmt.Sprintf(`import fs from "node:fs";
try { fs.readFileSync(%q); "NOT REACHED" }
catch (e) { "caught:" + e.message }`, secret)

	got, err := rt.RunScript(context.Background(), src, grant, 0)
	if err != nil {
		t.Fatalf("catchable host failure: unexpected Go error %v", err)
	}
	if !strings.HasPrefix(got, "caught:") || !strings.Contains(got, "not in allowlist") {
		t.Fatalf("catch result = %q, want a caught 'not in allowlist' message", got)
	}
}

// TestNetStubGeneralizes: the SAME uniform ABI carries a second capability. The net
// import is stubbed (no real network) but proves the pattern is not fs-specific:
// granted -> a value crosses back; the deny path is exercised by the ungranted case.
func TestNetStubGeneralizes(t *testing.T) {
	rt := newRuntime(t, generousPages)

	// Granted: a value crosses back through the identical [tag|len|payload] envelope.
	got, err := rt.RunScript(context.Background(),
		`import net from "node:net"; net.fetch("hello")`,
		Grant{Net: &NetGrant{}}, 0)
	if err != nil {
		t.Fatalf("granted net stub: %v", err)
	}
	if got != "stub-fetched:hello" {
		t.Fatalf("net.fetch = %q, want %q", got, "stub-fetched:hello")
	}

	// Ungranted: Gate 1 refuses to resolve node:net at bundle time.
	if _, err := rt.RunScript(context.Background(),
		`import net from "node:net"; net.fetch("hello")`, Grant{}, 0); err == nil ||
		!strings.Contains(err.Error(), "capability not granted") {
		t.Fatalf("ungranted net: got %v, want Gate 1 denial", err)
	}
}

// TestMarshallingCost reports the per-call round-trip cost of one capability read
// (JS -> C marshal -> import -> Go grant/scope check + os.ReadFile -> result back).
// Run with -v to see it; this is the "per-call marshalling cost" note in the doc.
func TestMarshallingCost(t *testing.T) {
	rt := newRuntime(t, generousPages)
	dir := t.TempDir()
	file := filepath.Join(dir, "n.txt")
	writeFile(t, file, "x")
	grant := Grant{FS: &FSGrant{AllowedPaths: []string{dir}}}

	// One instance, many reads: isolates the per-CALL cost from instantiate cost.
	inst, err := rt.Instantiate(context.Background())
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	defer inst.Close(context.Background())

	bundled, err := Bundle(fmt.Sprintf(`import fs from "node:fs"; fs.readFileSync(%q)`, file), grant)
	if err != nil {
		t.Fatalf("bundle: %v", err)
	}
	if _, err := inst.EvalWithGrant(context.Background(), bundled, grant, 0); err != nil {
		t.Fatalf("prime: %v", err)
	}

	const n = 200
	start := time.Now()
	for i := 0; i < n; i++ {
		if _, err := inst.EvalWithGrant(context.Background(), bundled, grant, 0); err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
	}
	// NB: this includes the per-eval QuickJS context bootstrap (see the first spike's
	// numbers); the marshalling itself is a small fraction on top of that.
	t.Logf("fs.readFileSync round-trip (incl. per-eval context bootstrap): %v/op (n=%d)",
		time.Since(start)/n, n)
}
