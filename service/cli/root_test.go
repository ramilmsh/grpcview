package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

type fakeServe struct {
	calls []ServeOptions
}

func (f *fakeServe) serve(_ context.Context, o ServeOptions) error {
	f.calls = append(f.calls, o)
	return nil
}

func unusedFactory(t *testing.T) clientFactory {
	return func(context.Context, *globalFlags) (session, error) {
		t.Fatal("the client factory must not be called: no C0 verb opens a client")
		return session{}, nil
	}
}

func TestMain_outputsAndExitCodes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		wantCode   int
		wantServed []ServeOptions
		checkOut   func(t *testing.T, s string)
		checkErr   func(t *testing.T, s string)
	}{
		{
			name:       "no args serves on the default port",
			args:       nil,
			wantCode:   0,
			wantServed: []ServeOptions{{Port: defaultPort}},
		},
		{
			name:       "serve honors its own port flag",
			args:       []string{"serve", "--port", "1"},
			wantCode:   0,
			wantServed: []ServeOptions{{Port: 1}},
		},
		{
			name:     "version prints exactly one line to stdout",
			args:     []string{"version"},
			wantCode: 0,
			checkOut: func(t *testing.T, s string) {
				lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
				if len(lines) != 1 || lines[0] == "" {
					t.Errorf("want exactly one non-empty stdout line, got %q", s)
				}
			},
		},
		{
			name:     "--version prints what the version verb prints",
			args:     []string{"--version"},
			wantCode: 0,
			checkOut: func(t *testing.T, s string) {
				if s != releaseVersion()+"\n" {
					t.Errorf("want %q, got %q", releaseVersion()+"\n", s)
				}
			},
		},
		{
			name:     "unknown verb is exit 2 with usage on stderr and nothing on stdout",
			args:     []string{"typoe"},
			wantCode: 2,
			checkErr: func(t *testing.T, s string) {
				if !strings.Contains(s, "Usage:") {
					t.Errorf("want a usage dump on stderr, got %q", s)
				}
				if !strings.Contains(s, `grpcview: unknown command "typoe"`) {
					t.Errorf("want the prefixed unknown-command line, got %q", s)
				}
			},
		},
		{
			name:     "a bad flag value is exit 2 without a usage dump",
			args:     []string{"--port", "notanumber"},
			wantCode: 2,
			checkErr: func(t *testing.T, s string) {
				if strings.Contains(s, "Usage:") {
					t.Errorf("want no usage dump for a flag-parse error, got %q", s)
				}
				lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
				if len(lines) != 1 {
					t.Errorf("want exactly one stderr line, got %q", s)
				}
				if !strings.HasPrefix(s, "grpcview: ") {
					t.Errorf("want a %q-prefixed stderr line, got %q", "grpcview: ", s)
				}
			},
		},
		{
			name:     "a bad flag value on a subcommand is exit 2 too",
			args:     []string{"serve", "--port", "notanumber"},
			wantCode: 2,
			checkErr: func(t *testing.T, s string) {
				if strings.Contains(s, "Usage:") {
					t.Errorf("want no usage dump for a flag-parse error, got %q", s)
				}
				if !strings.HasPrefix(s, "grpcview: ") {
					t.Errorf("want a %q-prefixed stderr line, got %q", "grpcview: ", s)
				}
			},
		},
		{
			name:     "version rejects extra args",
			args:     []string{"version", "extra"},
			wantCode: 2,
			checkErr: func(t *testing.T, s string) {
				if !strings.HasPrefix(s, "grpcview: ") {
					t.Errorf("want a %q-prefixed stderr line, got %q", "grpcview: ", s)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errBuf bytes.Buffer
			s := Streams{In: strings.NewReader(""), Out: &out, Err: &errBuf}
			fs := &fakeServe{}

			code := execute(
				context.Background(),
				newRootCmd(s, fs.serve, unusedFactory(t)),
				tc.args,
				s,
			)

			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d (stdout=%q stderr=%q)", code, tc.wantCode, out.String(), errBuf.String())
			}

			if tc.checkOut != nil {
				tc.checkOut(t, out.String())
			} else if out.Len() != 0 {
				t.Errorf("want empty stdout, got %q", out.String())
			}

			if tc.checkErr != nil {
				tc.checkErr(t, errBuf.String())
			} else if errBuf.Len() != 0 {
				t.Errorf("want empty stderr, got %q", errBuf.String())
			}

			if len(fs.calls) != len(tc.wantServed) {
				t.Fatalf("serve called %d time(s) with %v, want %v", len(fs.calls), fs.calls, tc.wantServed)
			}
			for i, want := range tc.wantServed {
				if fs.calls[i] != want {
					t.Errorf("serve call %d = %+v, want %+v", i, fs.calls[i], want)
				}
			}
		})
	}
}

func TestCompletion(t *testing.T) {
	var out, errBuf bytes.Buffer
	s := Streams{In: strings.NewReader(""), Out: &out, Err: &errBuf}
	fs := &fakeServe{}

	code := execute(context.Background(), newRootCmd(s, fs.serve, unusedFactory(t)), []string{"completion", "fish"}, s)

	if code != 0 {
		t.Errorf("exit code = %d, want 0 (stderr=%q)", code, errBuf.String())
	}
	if out.Len() == 0 {
		t.Error("want a completion script on stdout, got nothing")
	}
	if errBuf.Len() != 0 {
		t.Errorf("want empty stderr, got %q", errBuf.String())
	}
	if len(fs.calls) != 0 {
		t.Errorf("completion must not serve, got %v", fs.calls)
	}
}

func TestRootCmd_persistentFlags(t *testing.T) {
	s := Streams{In: strings.NewReader(""), Out: &bytes.Buffer{}, Err: &bytes.Buffer{}}
	root := newRootCmd(s, (&fakeServe{}).serve, unusedFactory(t))

	for name, want := range map[string]string{
		"workspace":  "",
		"collection": "",
		"server":     "",
		"timeout":    "30s",
	} {
		f := root.PersistentFlags().Lookup(name)
		if f == nil {
			t.Errorf("--%s is not a persistent flag on the root", name)
			continue
		}
		if f.DefValue != want {
			t.Errorf("--%s default = %q, want %q", name, f.DefValue, want)
		}
	}

	if root.PersistentFlags().Lookup("o") != nil || root.PersistentFlags().ShorthandLookup("o") != nil {
		t.Error("-o must not be persistent: verbs register it with disjoint value sets")
	}
}

func TestExitCodeMapping(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		wantCode int
		wantErr  string
	}{
		{name: "success", err: nil, wantCode: 0},
		{
			name:     "a gRPC status failure is exit 1",
			err:      statusError{code: 1, err: errors.New("Auth/Login: NOT_FOUND: nope")},
			wantCode: 1,
			wantErr:  "grpcview: Auth/Login: NOT_FOUND: nope\n",
		},
		{
			name:     "a wrapped status error keeps its code",
			err:      wrap(statusError{code: 1, err: errors.New("boom")}),
			wantCode: 1,
			wantErr:  "grpcview: while doing the thing: boom\n",
		},
		{
			name:     "any other error is exit 2",
			err:      errors.New("something else"),
			wantCode: 2,
			wantErr:  "grpcview: something else\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errBuf bytes.Buffer
			s := Streams{In: strings.NewReader(""), Out: &out, Err: &errBuf}
			root := newRootCmd(s, (&fakeServe{}).serve, unusedFactory(t))
			root.AddCommand(&cobra.Command{
				Use:  "faketest",
				Args: cobra.NoArgs,
				RunE: func(*cobra.Command, []string) error { return tc.err },
			})

			code := execute(context.Background(), root, []string{"faketest"}, s)

			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d", code, tc.wantCode)
			}
			if errBuf.String() != tc.wantErr {
				t.Errorf("stderr = %q, want %q", errBuf.String(), tc.wantErr)
			}
			if out.Len() != 0 {
				t.Errorf("want empty stdout, got %q", out.String())
			}
		})
	}
}

func wrap(err error) error {
	return &wrapped{err: err}
}

type wrapped struct{ err error }

func (w *wrapped) Error() string { return "while doing the thing: " + w.err.Error() }
func (w *wrapped) Unwrap() error { return w.err }
