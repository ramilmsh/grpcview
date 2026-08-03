package scripting

// Capability-layer tests, exercised through Engine.RunScenario (Gate 1 at compile time,
// Gate 2 at call time in the Go host functions).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func jsonString(t *testing.T, res Result) string {
	t.Helper()
	var s string
	if err := json.Unmarshal(res.Value, &s); err != nil {
		t.Fatalf("value %s is not a JSON string: %v", res.Value, err)
	}
	return s
}

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

func TestFSReadOutOfScopeRefusedInGo(t *testing.T) {
	e := newEngine(t)
	dir := t.TempDir()
	allowed := filepath.Join(dir, "ok")
	if err := os.Mkdir(allowed, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, filepath.Join(allowed, "in.txt"), "in scope")
	secret := filepath.Join(dir, "secret.txt")
	writeFile(t, secret, "TOP SECRET")

	grant := Grant{FS: &FSGrant{AllowedPaths: []string{allowed}}}

	res, err := e.RunScenario(context.Background(),
		fmt.Sprintf(`import fs from "node:fs"; fs.readFileSync(%q)`, filepath.Join(allowed, "in.txt")),
		grant, Input{})
	if err != nil {
		t.Fatalf("in-scope read: %v", err)
	}
	if got := jsonString(t, res); got != "in scope" {
		t.Fatalf("in-scope read = %q, want %q", got, "in scope")
	}

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
	if strings.Contains(jsErr.Message, "TOP SECRET") {
		t.Fatalf("out-of-scope refusal leaked file contents: %q", jsErr.Message)
	}
}

func TestFSUngrantedGate1Bundle(t *testing.T) {
	e := newEngine(t)
	src := `import fs from "node:fs"; fs.readFileSync("/etc/passwd")`

	if _, err := e.RunScenario(context.Background(), src, Grant{}, Input{}); err == nil ||
		!strings.Contains(err.Error(), "capability not granted") {
		t.Fatalf("RunScenario without fs grant: got %v, want a Gate 1 'capability not granted' denial", err)
	}
}

func TestHostFailureIsCatchable(t *testing.T) {
	e := newEngine(t)
	dir := t.TempDir()
	elsewhere := t.TempDir()
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

func TestFetchGlobal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Grpcview", "pong")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"method":%q,"echo":%q,"auth":%q}`, r.Method, string(body), r.Header.Get("Authorization"))
	}))
	defer srv.Close()

	e := newEngine(t)
	src := fmt.Sprintf(`
const res = await fetch(%q, { method: "post", headers: { Authorization: "Bearer t0ken" }, body: "hi" });
const j = await res.json();
({ status: res.status, ok: res.ok, header: res.headers.get("x-grpcview"),
   method: j.method, echo: j.echo, auth: j.auth })`, srv.URL)

	res, err := e.RunScenario(context.Background(), src, Grant{}, Input{})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	var got struct {
		Status         int
		OK             bool
		Header, Method string
		Echo, Auth     string
	}
	if err := json.Unmarshal(res.Value, &got); err != nil {
		t.Fatalf("decode %s: %v", res.Value, err)
	}
	want := struct {
		Status         int
		OK             bool
		Header, Method string
		Echo, Auth     string
	}{201, true, "pong", "POST", "hi", "Bearer t0ken"}
	if got != want {
		t.Fatalf("fetch result = %+v, want %+v", got, want)
	}
}

func TestFetchRejects(t *testing.T) {
	e := newEngine(t)
	// 127.0.0.1:0 is never listening, so the dial fails immediately.
	res, err := e.RunScenario(context.Background(),
		`await fetch("http://127.0.0.1:0").then(() => "resolved").catch((e) => "caught:" + String(e.message || e))`,
		Grant{}, Input{})
	if err != nil {
		t.Fatalf("fetch reject: %v", err)
	}
	if got := jsonString(t, res); !strings.HasPrefix(got, "caught:fetch:") {
		t.Fatalf("rejection = %q, want a caught fetch error", got)
	}
}
