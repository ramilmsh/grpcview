package cli

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

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

func uploadSourceWithPath(fileName, path string, resolved *grpcviewv1.Resolved) *grpcviewv1.DescriptorSource {
	src := uploadSource(fileName, resolved)
	src.GetUpload().Path = path
	return src
}

func bazelSource(label string, resolved *grpcviewv1.Resolved) *grpcviewv1.DescriptorSource {
	return &grpcviewv1.DescriptorSource{
		Id:       "bazel:" + label,
		Source:   &grpcviewv1.DescriptorSource_Bazel{Bazel: &grpcviewv1.Bazel{Label: label}},
		Resolved: resolved,
	}
}

func committed(src *grpcviewv1.DescriptorSource) *grpcviewv1.DescriptorSource {
	src.CommitDescriptors = true
	return src
}

func shared(src *grpcviewv1.DescriptorSource) *grpcviewv1.DescriptorSource {
	src.Origin = grpcviewv1.SourceOrigin_SOURCE_ORIGIN_WORKSPACE
	return src
}

func sourcesWorkspace() *grpcviewv1.Collection {
	ws := testWorkspace()
	ws.Sources = []*grpcviewv1.DescriptorSource{
		shared(reflectionSource("localhost:50055", &grpcviewv1.Resolved{
			FileCount:       4,
			ServiceNames:    []string{"auth.v1.AuthService", "grpcview.echo.v1.EchoService"},
			WonServiceNames: []string{"auth.v1.AuthService", "grpcview.echo.v1.EchoService"},
		})),
		committed(uploadSource("echo.binpb", &grpcviewv1.Resolved{
			FileCount:    3,
			ServiceNames: []string{"grpcview.echo.v1.EchoService"},
		})),
		reflectionSource("gone.example:9999", &grpcviewv1.Resolved{
			Error: "dial tcp 10.0.0.1:9999:\n  connect: connection refused",
		}),
	}
	return ws
}

func refreshableWorkspace() *grpcviewv1.Collection {
	ws := testWorkspace()
	ws.Sources = []*grpcviewv1.DescriptorSource{
		reflectionSource("localhost:50055", &grpcviewv1.Resolved{FileCount: 4}),
		uploadSource("browser.binpb", &grpcviewv1.Resolved{FileCount: 3}),
		uploadSourceWithPath("built.binpb", "proto/built.binpb", &grpcviewv1.Resolved{FileCount: 3}),
		bazelSource("//proto/grpcview/echo/v1:grpcviewechov1_proto", &grpcviewv1.Resolved{FileCount: 2}),
	}
	return ws
}

const sourcesGolden = `1  reflection:localhost:50055    reflection  workspace   cached     4 files  serves 2  wins 2
2  upload:echo.binpb             upload      collection  committed  3 files  serves 1  wins 0  shadowed
3  reflection:gone.example:9999  reflection  collection  cached     0 files  serves 0  wins 0  error: dial tcp 10.0.0.1:9999: connect: connection refused
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

func TestSourcesLsTrust(t *testing.T) {
	bazelWorkspace := func(labels ...string) *grpcviewv1.Collection {
		ws := testWorkspace()
		ws.Sources = []*grpcviewv1.DescriptorSource{
			reflectionSource("localhost:50055", &grpcviewv1.Resolved{FileCount: 4}),
		}
		for _, label := range labels {
			ws.Sources = append(ws.Sources, bazelSource(label, &grpcviewv1.Resolved{
				Error: "workspace is not trusted",
			}))
		}
		return ws
	}

	for _, tc := range []struct {
		name     string
		trusted  bool
		snapshot *grpcviewv1.Collection
		wantNote string
		wantList int
	}{
		{
			name:     "an untrusted workspace with a bazel source says so, once, after the rows",
			snapshot: bazelWorkspace("//proto/grpcview/echo/v1:grpcviewechov1_proto"),
			wantNote: "! %s is not trusted: 1 bazel source above cannot build here — `grpcview trust` allows it",
			wantList: 2,
		},
		{
			name:     "the count is the number of rows that would build",
			snapshot: bazelWorkspace("//proto/grpcview/echo/v1:grpcviewechov1_proto", "//proto/auth/v1:authv1_proto"),
			wantNote: "! %s is not trusted: 2 bazel sources above cannot build here — `grpcview trust` allows it",
			wantList: 2,
		},
		{
			name:     "a trusted workspace says nothing at all",
			trusted:  true,
			snapshot: bazelWorkspace("//proto/grpcview/echo/v1:grpcviewechov1_proto"),
			wantList: 2,
		},
		{
			name:     "reflection and upload only: untrusted, and not nagged about it",
			snapshot: sourcesWorkspace(),
			wantList: 1,
		},
		{
			name:     "no sources at all: nothing to say either",
			snapshot: testWorkspace(),
			wantList: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(mustCollectionDir(t, root))

			fc := newFake()
			fc.snapshot = tc.snapshot
			fc.listRoot = root
			fc.listTrusted = tc.trusted

			out, errOut, code := runCLI(fc, "", "sources", "ls")
			if code != 0 {
				t.Fatalf("exit code = %d, want 0 (stdout=%q stderr=%q)", code, out, errOut)
			}
			if errOut != "" {
				t.Errorf("stderr = %q, want empty: trust is a listed fact, not a diagnostic", errOut)
			}

			lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
			wantRows := len(tc.snapshot.GetSources())
			if tc.wantNote == "" {
				if out != "" && len(lines) != wantRows {
					t.Errorf("stdout = %q, want the %d source row(s) alone", out, wantRows)
				}
				if strings.Contains(out, "grpcview trust") {
					t.Errorf("stdout = %q, want no trust note", out)
				}
			} else {
				if len(lines) != wantRows+1 {
					t.Fatalf("stdout = %q, want the %d rows plus exactly one trust line", out, wantRows)
				}
				if want := fmt.Sprintf(tc.wantNote, root); lines[len(lines)-1] != want {
					t.Errorf("trust line = %q, want %q", lines[len(lines)-1], want)
				}
			}
			if len(fc.gotList) != tc.wantList {
				t.Errorf("ListCollections called %d time(s), want %d", len(fc.gotList), tc.wantList)
			}
		})
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

func TestTheBuildingVerbsGetALongerDefaultTimeout(t *testing.T) {
	const label = "//proto/grpcview/echo/v1:grpcviewechov1_proto"

	for _, tc := range []struct {
		name string
		args []string
		want time.Duration
	}{
		{
			name: "sources add of a bazel label waits for the build",
			args: []string{"sources", "add", label},
			want: buildTimeout,
		},
		{
			name: "sources refresh does too: any row in the list may be a bazel source",
			args: []string{"sources", "refresh"},
			want: buildTimeout,
		},
		{
			name: "refreshing one named source is the same budget: the id says nothing about its kind",
			args: []string{"sources", "refresh", "bazel:" + label},
			want: buildTimeout,
		},
		{
			name: "sources add of a reflection target keeps the short one: it dials, it does not build",
			args: []string{"sources", "add", "localhost:50055"},
			want: defaultTimeout,
		},
		{
			name: "get keeps it: it reads",
			args: []string{"get"},
			want: defaultTimeout,
		},
		{
			name: "invoke keeps it",
			args: []string{"invoke", "Auth/Login"},
			want: defaultTimeout,
		},
		{
			name: "sources ls keeps it: listing resolves nothing",
			args: []string{"sources", "ls"},
			want: defaultTimeout,
		},
		{
			name: "an explicit --timeout wins on a building verb",
			args: []string{"sources", "add", label, "--timeout", "5s"},
			want: 5 * time.Second,
		},
		{
			name: "and on a reading one",
			args: []string{"get", "--timeout", "5s"},
			want: 5 * time.Second,
		},
		{
			name: "an explicit --timeout equal to the default is still explicit",
			args: []string{"sources", "add", label, "--timeout", defaultTimeout.String()},
			want: defaultTimeout,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errBuf bytes.Buffer
			s := Streams{In: strings.NewReader(""), Out: &out, Err: &errBuf}
			fc := newFake()
			fc.snapshot = refreshableWorkspace()

			var budget time.Duration
			factory := func(ctx context.Context, _ *globalFlags) (session, error) {
				deadline, ok := ctx.Deadline()
				if !ok {
					t.Error("want the timeout applied as a context deadline around the call")
				}
				budget = time.Until(deadline)
				return session{Client: fc, close: func(context.Context) error { return nil }}, nil
			}

			code := execute(context.Background(), newRootCmd(s, (&fakeServe{}).serve, factory), tc.args, s)
			if code != 0 {
				t.Fatalf("exit code = %d, want 0 (stdout=%q stderr=%q)", code, out.String(), errBuf.String())
			}
			if budget > tc.want || budget < tc.want-time.Second {
				t.Errorf("deadline is %s away, want %s", budget, tc.want)
			}
		})
	}
}
