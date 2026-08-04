package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"connectrpc.com/connect"
)

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
		Collection struct {
			Name     string `json:"name"`
			Services []struct {
				Package string `json:"package"`
				Name    string `json:"name"`
			} `json:"services"`
		} `json:"collection"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("stdout does not parse as JSON: %v\n%s", err, out)
	}
	if resp.Collection.Name != "default" {
		t.Errorf("collection.name = %q, want the GetResponse envelope around the collection", resp.Collection.Name)
	}
	if len(resp.Collection.Services) != 2 {
		t.Errorf("collection.services = %d, want the fixture's 2", len(resp.Collection.Services))
	}

	if fc.invokeCalls() != 0 {
		t.Errorf("get invoked %d call(s); it must invoke nothing", fc.invokeCalls())
	}
	if len(fc.gotGet) != 1 || fc.gotGet[0].GetCollection() != "." {
		t.Errorf("gotGet = %+v, want exactly one Get for the default --collection \".\"", fc.gotGet)
	}
	if fc.closed != 1 {
		t.Errorf("session closed %d times, want 1", fc.closed)
	}
}

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
	if !strings.Contains(errOut, `grpcview: failed to read collection "."`) {
		t.Errorf("stderr = %q, want the prefixed one-liner", errOut)
	}
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}
