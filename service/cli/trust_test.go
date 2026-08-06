package cli

import (
	"strings"
	"testing"

	"connectrpc.com/connect"
)

func TestTrust(t *testing.T) {
	for _, tc := range []struct {
		name        string
		args        []string
		fake        func(*fakeClient)
		wantTrusted bool
		wantErrHas  string
		wantCode    int
	}{
		{
			name:        "bare trust grants it",
			args:        []string{"trust"},
			wantTrusted: true,
		},
		{
			name:        "--off revokes it",
			args:        []string{"trust", "--off"},
			wantTrusted: false,
		},
		{
			name:        "a Connect error is exit 2, naming the failed direction",
			args:        []string{"trust"},
			fake:        func(fc *fakeClient) { fc.writes.err = connect.NewError(connect.CodeInternal, errNoTarget) },
			wantTrusted: true,
			wantErrHas:  "failed to trust this workspace",
			wantCode:    2,
		},
		{
			name:       "the revoking direction is named too",
			args:       []string{"trust", "--off"},
			fake:       func(fc *fakeClient) { fc.writes.err = connect.NewError(connect.CodeInternal, errNoTarget) },
			wantErrHas: "failed to un-trust this workspace",
			wantCode:   2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fc := newFake()
			if tc.fake != nil {
				tc.fake(fc)
			}

			out, errOut, code := runCLI(fc, "", tc.args...)

			if tc.wantCode == 0 {
				assertSilent(t, out, errOut, code)
			} else {
				if out != "" {
					t.Errorf("stdout = %q, want empty: a diagnostic never goes to stdout", out)
				}
				if code != tc.wantCode {
					t.Errorf("exit code = %d, want %d", code, tc.wantCode)
				}
			}
			if tc.wantErrHas != "" && !strings.Contains(errOut, tc.wantErrHas) {
				t.Errorf("stderr = %q, want it to contain %q", errOut, tc.wantErrHas)
			}

			if len(fc.writes.setTrust) != 1 {
				t.Fatalf("SetWorkspaceTrust called %d time(s), want 1", len(fc.writes.setTrust))
			}
			if got := fc.writes.setTrust[0].GetTrusted(); got != tc.wantTrusted {
				t.Errorf("trusted = %v, want %v", got, tc.wantTrusted)
			}
		})
	}
}

func TestTrustNeedsNoCollection(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	fc := newFake()
	fc.listRoot = root
	fc.listing = nil

	out, errOut, code := runCLI(fc, "", "trust")
	assertSilent(t, out, errOut, code)

	if len(fc.gotList) != 0 {
		t.Errorf("ListCollections called %d time(s), want none: trust addresses the root itself", len(fc.gotList))
	}
	if len(fc.writes.setTrust) != 1 {
		t.Errorf("SetWorkspaceTrust called %d time(s), want 1", len(fc.writes.setTrust))
	}
}

func TestTrustTakesNoArguments(t *testing.T) {
	fc := newFake()
	out, errOut, code := runCLI(fc, "", "trust", "on")
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if out != "" {
		t.Errorf("stdout = %q, want empty", out)
	}
	if !strings.Contains(errOut, "grpcview: ") {
		t.Errorf("stderr = %q, want a prefixed diagnostic", errOut)
	}
	if len(fc.writes.setTrust) != 0 {
		t.Errorf("SetWorkspaceTrust called %d time(s), want 0", len(fc.writes.setTrust))
	}
}
