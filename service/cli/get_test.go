package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"connectrpc.com/connect"
)

// TestGet pins the two things `get` promises: the whole GetResponse, and exactly
// one line of it, so `grpcview get | jq` works.
func TestGet(t *testing.T) {
	fc := newFake()

	out, errOut, code := runCLI(fc, "", "get")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", code, errOut)
	}
	if errOut != "" {
		t.Errorf("stderr = %q, want empty: stdout is data and there are no diagnostics here", errOut)
	}
	if strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, "\n") {
		t.Errorf("stdout is not exactly one line: %q", out)
	}

	var resp struct {
		Workspace struct {
			Name     string `json:"name"`
			Services []struct {
				Package string `json:"package"`
				Name    string `json:"name"`
			} `json:"services"`
		} `json:"workspace"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("stdout does not parse as JSON: %v\n%s", err, out)
	}
	// The envelope, not a bare workspace: a script written against `get` and one
	// written against the RPC must read the same paths.
	if resp.Workspace.Name != "default" {
		t.Errorf("workspace.name = %q, want the GetResponse envelope around the workspace", resp.Workspace.Name)
	}
	if len(resp.Workspace.Services) != 2 {
		t.Errorf("workspace.services = %d, want the fixture's 2", len(resp.Workspace.Services))
	}

	if fc.invokeCalls() != 0 {
		t.Errorf("get invoked %d call(s); it must invoke nothing", fc.invokeCalls())
	}
	if len(fc.gotGet) != 1 || fc.gotGet[0].GetWorkspaceName() != "default" {
		t.Errorf("gotGet = %+v, want exactly one Get for --workspace default", fc.gotGet)
	}
	if fc.closed != 1 {
		t.Errorf("session closed %d times, want 1", fc.closed)
	}
}

// TestGetTakesNoOutputFlag holds the D8 line that -o is per verb: a whole
// workspace has one shape, so `get` registers no -o at all and a stray one is a
// flag error on stderr with exit 2.
func TestGetTakesNoOutputFlag(t *testing.T) {
	out, errOut, code := runCLI(newFake(), "", "get", "-o", "json")

	if out != "" {
		t.Errorf("stdout = %q, want empty: a flag error is not data", out)
	}
	if !strings.Contains(errOut, "unknown shorthand flag") {
		t.Errorf("stderr = %q, want cobra's unknown-flag error", errOut)
	}
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}

func TestGetWorkspaceReadFailure(t *testing.T) {
	fc := newFake()
	fc.getErr = connect.NewError(connect.CodeUnavailable, errNoTarget)

	out, errOut, code := runCLI(fc, "", "get")

	if out != "" {
		t.Errorf("stdout = %q, want empty", out)
	}
	if !strings.Contains(errOut, `grpcview: failed to read workspace "default"`) {
		t.Errorf("stderr = %q, want the prefixed one-liner", errOut)
	}
	// A read verb cannot produce exit 1: nothing was invoked, so there is no gRPC
	// status to report (D9).
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}
