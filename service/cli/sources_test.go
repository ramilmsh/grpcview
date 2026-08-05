package cli

import (
	"strings"
	"testing"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

func reflectionSource(address string, resolved *grpcviewv1.Resolved) *grpcviewv1.DescriptorSource {
	return &grpcviewv1.DescriptorSource{
		Id:       "reflection:" + address,
		Source:   &grpcviewv1.DescriptorSource_Reflection{Reflection: &grpcviewv1.Server{Address: address}},
		Resolved: resolved,
	}
}

func uploadSource(fileName string, resolved *grpcviewv1.Resolved) *grpcviewv1.DescriptorSource {
	return &grpcviewv1.DescriptorSource{
		Id:       "upload:" + fileName,
		Source:   &grpcviewv1.DescriptorSource_Upload{Upload: &grpcviewv1.Upload{FileName: fileName}},
		Resolved: resolved,
	}
}

func committed(src *grpcviewv1.DescriptorSource) *grpcviewv1.DescriptorSource {
	src.CommitDescriptors = true
	return src
}

func sourcesWorkspace() *grpcviewv1.Collection {
	ws := testWorkspace()
	ws.Sources = []*grpcviewv1.DescriptorSource{
		reflectionSource("localhost:50055", &grpcviewv1.Resolved{
			FileCount:       4,
			ServiceNames:    []string{"auth.v1.AuthService", "echo.v1.EchoService"},
			WonServiceNames: []string{"auth.v1.AuthService", "echo.v1.EchoService"},
		}),
		// The upload is the kind that most wants committing: it cannot be re-fetched.
		committed(uploadSource("echo.binpb", &grpcviewv1.Resolved{
			FileCount:    3,
			ServiceNames: []string{"echo.v1.EchoService"},
		})),
		reflectionSource("gone.example:9999", &grpcviewv1.Resolved{
			Error: "dial tcp 10.0.0.1:9999:\n  connect: connection refused",
		}),
	}
	return ws
}

const sourcesGolden = `1  reflection:localhost:50055    reflection  cached     4 files  serves 2  wins 2
2  upload:echo.binpb             upload      committed  3 files  serves 1  wins 0  shadowed
3  reflection:gone.example:9999  reflection  cached     0 files  serves 0  wins 0  error: dial tcp 10.0.0.1:9999: connect: connection refused
`

func TestSourcesLs(t *testing.T) {
	fc := newFake()
	fc.snapshot = sourcesWorkspace()

	out, errOut, code := runCLI(fc, "", "sources", "ls")

	if out != sourcesGolden {
		t.Errorf("stdout:\n%s\nwant:\n%s\n(got %q)", out, sourcesGolden, out)
	}
	if errOut != "" {
		t.Errorf("stderr = %q, want empty: a failed resolve is a listed state, not a command failure", errOut)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if fc.invokeCalls() != 0 {
		t.Errorf("sources ls invoked %d call(s); it must invoke nothing", fc.invokeCalls())
	}
}

func TestSourcesLsShadowing(t *testing.T) {
	for _, tc := range []struct {
		name     string
		resolved *grpcviewv1.Resolved
		want     string
	}{
		{
			name:     "wins everything it serves: no status at all",
			resolved: &grpcviewv1.Resolved{ServiceNames: []string{"a.A", "b.B"}, WonServiceNames: []string{"a.A", "b.B"}},
			want:     "",
		},
		{
			name:     "serves five, wins none: fully shadowed",
			resolved: &grpcviewv1.Resolved{ServiceNames: []string{"a.A", "b.B", "c.C", "d.D", "e.E"}},
			want:     "shadowed",
		},
		{
			name:     "wins one of three: the count says which part lost",
			resolved: &grpcviewv1.Resolved{ServiceNames: []string{"a.A", "b.B", "c.C"}, WonServiceNames: []string{"a.A"}},
			want:     "2 shadowed",
		},
		{
			name:     "serves nothing and is not shadowed: it is empty",
			resolved: &grpcviewv1.Resolved{},
			want:     "no services",
		},
		{
			name:     "a failed resolve reports why, and the counts stay zero",
			resolved: &grpcviewv1.Resolved{ServiceNames: []string{"a.A"}, Error: "link error: duplicate symbol a.A"},
			want:     "error: link error: duplicate symbol a.A",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sourceStatus(tc.resolved); got != tc.want {
				t.Errorf("sourceStatus = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSourcesLsEmptyAndFailing(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		fake       func(*fakeClient)
		wantOut    string
		wantErrHas string
		wantCode   int
	}{
		{
			name:     "a workspace with no sources lists nothing, successfully",
			args:     []string{"sources", "ls"},
			wantOut:  "",
			wantCode: 0,
		},
		{
			name:       "sources ls takes no arguments",
			args:       []string{"sources", "ls", "extra"},
			wantErrHas: "unknown command",
			wantCode:   2,
		},
		{
			name:       "bare sources reports the missing subcommand on stderr",
			args:       []string{"sources"},
			wantErrHas: `grpcview: "grpcview sources" needs a subcommand: ls`,
			wantCode:   2,
		},
		{
			name:       "a typo'd subcommand is exit 2, with usage on stderr",
			args:       []string{"sources", "list"},
			wantErrHas: `unknown command "list" for "grpcview sources"`,
			wantCode:   2,
		},
		{
			name:       "a Connect error from Get is exit 2",
			args:       []string{"sources", "ls"},
			fake:       func(fc *fakeClient) { fc.getErr = connect.NewError(connect.CodeUnavailable, errNoTarget) },
			wantErrHas: `failed to read collection "."`,
			wantCode:   2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fc := newFake()
			if tc.fake != nil {
				tc.fake(fc)
			}

			out, errOut, code := runCLI(fc, "", tc.args...)

			if out != tc.wantOut {
				t.Errorf("stdout = %q, want %q: usage and diagnostics never go to stdout", out, tc.wantOut)
			}
			if tc.wantErrHas != "" && !strings.Contains(errOut, tc.wantErrHas) {
				t.Errorf("stderr = %q, want it to contain %q", errOut, tc.wantErrHas)
			}
			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d", code, tc.wantCode)
			}
		})
	}
}
