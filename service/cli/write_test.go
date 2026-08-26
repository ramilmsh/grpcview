package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

type writeCalls struct {
	createCollection []*grpcviewv1.CreateCollectionRequest
	addSource        []*grpcviewv1.AddDescriptorSourceRequest
	refreshSource    []*grpcviewv1.RefreshDescriptorSourceRequest
	removeSource     []*grpcviewv1.RemoveDescriptorSourceRequest
	reorderSource    []*grpcviewv1.ReorderDescriptorSourcesRequest
	commitSource     []*grpcviewv1.SetDescriptorSourceCommitRequest
	createFolder     []*grpcviewv1.CreateFolderRequest
	createRequest    []*grpcviewv1.CreateRequestRequest
	updateRequest    []*grpcviewv1.UpdateRequestRequest
	deleteRequest    []*grpcviewv1.DeleteRequestRequest
	moveItem         []*grpcviewv1.MoveItemRequest
	runScript        []*grpcviewv1.RunScriptRequest
	setTrust         []*grpcviewv1.SetWorkspaceTrustRequest

	order []string

	err               error
	script            *grpcviewv1.RunScriptResponse
	createdCollection *grpcviewv1.Collection
}

func (f *fakeClient) CreateCollection(_ context.Context, r *connect.Request[grpcviewv1.CreateCollectionRequest]) (*connect.Response[grpcviewv1.CreateCollectionResponse], error) {
	f.writes.createCollection = append(f.writes.createCollection, r.Msg)
	f.writes.order = append(f.writes.order, "CreateCollection")
	if f.writes.err != nil {
		return nil, f.writes.err
	}
	created := f.writes.createdCollection
	if created == nil {
		created = f.snapshot
	}
	return connect.NewResponse(&grpcviewv1.CreateCollectionResponse{Collection: created}), nil
}

func (f *fakeClient) AddDescriptorSource(_ context.Context, r *connect.Request[grpcviewv1.AddDescriptorSourceRequest]) (*connect.Response[grpcviewv1.AddDescriptorSourceResponse], error) {
	f.writes.addSource = append(f.writes.addSource, r.Msg)
	f.writes.order = append(f.writes.order, "AddDescriptorSource")
	if f.writes.err != nil {
		return nil, f.writes.err
	}
	return connect.NewResponse(&grpcviewv1.AddDescriptorSourceResponse{Collection: f.snapshot}), nil
}

func (f *fakeClient) RefreshDescriptorSource(_ context.Context, r *connect.Request[grpcviewv1.RefreshDescriptorSourceRequest]) (*connect.Response[grpcviewv1.RefreshDescriptorSourceResponse], error) {
	f.writes.refreshSource = append(f.writes.refreshSource, r.Msg)
	f.writes.order = append(f.writes.order, "RefreshDescriptorSource")
	if f.writes.err != nil {
		return nil, f.writes.err
	}
	return connect.NewResponse(&grpcviewv1.RefreshDescriptorSourceResponse{Collection: f.snapshot}), nil
}

func (f *fakeClient) RemoveDescriptorSource(_ context.Context, r *connect.Request[grpcviewv1.RemoveDescriptorSourceRequest]) (*connect.Response[grpcviewv1.RemoveDescriptorSourceResponse], error) {
	f.writes.removeSource = append(f.writes.removeSource, r.Msg)
	f.writes.order = append(f.writes.order, "RemoveDescriptorSource")
	if f.writes.err != nil {
		return nil, f.writes.err
	}
	return connect.NewResponse(&grpcviewv1.RemoveDescriptorSourceResponse{Collection: f.snapshot}), nil
}

func (f *fakeClient) ReorderDescriptorSources(_ context.Context, r *connect.Request[grpcviewv1.ReorderDescriptorSourcesRequest]) (*connect.Response[grpcviewv1.ReorderDescriptorSourcesResponse], error) {
	f.writes.reorderSource = append(f.writes.reorderSource, r.Msg)
	f.writes.order = append(f.writes.order, "ReorderDescriptorSources")
	if f.writes.err != nil {
		return nil, f.writes.err
	}
	return connect.NewResponse(&grpcviewv1.ReorderDescriptorSourcesResponse{Collection: f.snapshot}), nil
}

func (f *fakeClient) SetDescriptorSourceCommit(_ context.Context, r *connect.Request[grpcviewv1.SetDescriptorSourceCommitRequest]) (*connect.Response[grpcviewv1.SetDescriptorSourceCommitResponse], error) {
	f.writes.commitSource = append(f.writes.commitSource, r.Msg)
	f.writes.order = append(f.writes.order, "SetDescriptorSourceCommit")
	if f.writes.err != nil {
		return nil, f.writes.err
	}
	return connect.NewResponse(&grpcviewv1.SetDescriptorSourceCommitResponse{Collection: f.snapshot}), nil
}

func (f *fakeClient) SetWorkspaceTrust(_ context.Context, r *connect.Request[grpcviewv1.SetWorkspaceTrustRequest]) (*connect.Response[grpcviewv1.SetWorkspaceTrustResponse], error) {
	f.writes.setTrust = append(f.writes.setTrust, r.Msg)
	f.writes.order = append(f.writes.order, "SetWorkspaceTrust")
	if f.writes.err != nil {
		return nil, f.writes.err
	}
	return connect.NewResponse(&grpcviewv1.SetWorkspaceTrustResponse{Trusted: r.Msg.GetTrusted()}), nil
}

func (f *fakeClient) CreateFolder(_ context.Context, r *connect.Request[grpcviewv1.CreateFolderRequest]) (*connect.Response[grpcviewv1.CreateFolderResponse], error) {
	f.writes.createFolder = append(f.writes.createFolder, r.Msg)
	f.writes.order = append(f.writes.order, "CreateFolder")
	if f.writes.err != nil {
		return nil, f.writes.err
	}
	return connect.NewResponse(&grpcviewv1.CreateFolderResponse{Collection: f.snapshot}), nil
}

func (f *fakeClient) CreateRequest(_ context.Context, r *connect.Request[grpcviewv1.CreateRequestRequest]) (*connect.Response[grpcviewv1.CreateRequestResponse], error) {
	f.writes.createRequest = append(f.writes.createRequest, r.Msg)
	f.writes.order = append(f.writes.order, "CreateRequest")
	if f.writes.err != nil {
		return nil, f.writes.err
	}
	return connect.NewResponse(&grpcviewv1.CreateRequestResponse{Collection: f.snapshot}), nil
}

func (f *fakeClient) UpdateRequest(_ context.Context, r *connect.Request[grpcviewv1.UpdateRequestRequest]) (*connect.Response[grpcviewv1.UpdateRequestResponse], error) {
	f.writes.updateRequest = append(f.writes.updateRequest, r.Msg)
	f.writes.order = append(f.writes.order, "UpdateRequest")
	if f.writes.err != nil {
		return nil, f.writes.err
	}
	return connect.NewResponse(&grpcviewv1.UpdateRequestResponse{Collection: f.snapshot}), nil
}

func (f *fakeClient) DeleteRequest(_ context.Context, r *connect.Request[grpcviewv1.DeleteRequestRequest]) (*connect.Response[grpcviewv1.DeleteRequestResponse], error) {
	f.writes.deleteRequest = append(f.writes.deleteRequest, r.Msg)
	f.writes.order = append(f.writes.order, "DeleteRequest")
	if f.writes.err != nil {
		return nil, f.writes.err
	}
	return connect.NewResponse(&grpcviewv1.DeleteRequestResponse{Collection: f.snapshot}), nil
}

func (f *fakeClient) MoveItem(_ context.Context, r *connect.Request[grpcviewv1.MoveItemRequest]) (*connect.Response[grpcviewv1.MoveItemResponse], error) {
	f.writes.moveItem = append(f.writes.moveItem, r.Msg)
	f.writes.order = append(f.writes.order, "MoveItem")
	if f.writes.err != nil {
		return nil, f.writes.err
	}
	return connect.NewResponse(&grpcviewv1.MoveItemResponse{Collection: f.snapshot}), nil
}

func (f *fakeClient) RunScript(_ context.Context, r *connect.Request[grpcviewv1.RunScriptRequest]) (*connect.Response[grpcviewv1.RunScriptResponse], error) {
	f.writes.runScript = append(f.writes.runScript, r.Msg)
	f.writes.order = append(f.writes.order, "RunScript")
	if f.writes.err != nil {
		return nil, f.writes.err
	}
	if f.writes.script == nil {
		return connect.NewResponse(&grpcviewv1.RunScriptResponse{}), nil
	}
	return connect.NewResponse(f.writes.script), nil
}

func assertSilent(t *testing.T, out, errOut string, code int) {
	t.Helper()
	if out != "" {
		t.Errorf("stdout = %q, want empty: silence is success", out)
	}
	if errOut != "" {
		t.Errorf("stderr = %q, want empty: a successful mutation says nothing", errOut)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}

func TestSourcesAdd(t *testing.T) {
	raw := []byte("\x00\x01a FileDescriptorSet as far as this verb knows")

	dir := t.TempDir()
	file := filepath.Join(dir, "image.binpb")
	if err := os.WriteFile(file, raw, 0o600); err != nil {
		t.Fatalf("failed to write the fixture: %v", err)
	}

	for _, tc := range []struct {
		name       string
		arg        string
		tls        bool
		commit     bool
		fake       func(*fakeClient)
		wantErrHas string
		wantCode   int
		check      func(t *testing.T, fc *fakeClient)
	}{
		{
			name: "a path that stats as a file is an upload, identified by its BASENAME",
			arg:  "{file}",
			check: func(t *testing.T, fc *fakeClient) {
				got := onlyAdd(t, fc)
				if string(got.GetDescriptorSet()) != string(raw) {
					t.Errorf("descriptor_set = %q, want the file's bytes unchanged (%q)", got.GetDescriptorSet(), raw)
				}
				if got.GetFileName() != "image.binpb" {
					t.Errorf("file_name = %q, want %q: the identity is the basename, never the path",
						got.GetFileName(), "image.binpb")
				}
				if got.GetPath() != file {
					t.Errorf("path = %q, want the file's absolute path %q", got.GetPath(), file)
				}
				if got.GetReflection() != nil {
					t.Errorf("reflection = %v, want nil: a file is not a dial address", got.GetReflection())
				}
				if got.GetBazel() != nil {
					t.Errorf("bazel = %v, want nil: a file is not a label", got.GetBazel())
				}
				if got.GetCommitDescriptors() {
					t.Error("commit_descriptors = true without the flag; committing is opt-in for every kind, uploads included")
				}
			},
		},
		{
			name:   "--commit-descriptors rides along on an upload",
			arg:    "{file}",
			commit: true,
			check: func(t *testing.T, fc *fakeClient) {
				if !onlyAdd(t, fc).GetCommitDescriptors() {
					t.Error("commit_descriptors = false, want it set by --commit-descriptors")
				}
			},
		},
		{
			name:   "--commit-descriptors rides along on a reflection target too",
			arg:    "localhost:50055",
			commit: true,
			check: func(t *testing.T, fc *fakeClient) {
				if !onlyAdd(t, fc).GetCommitDescriptors() {
					t.Error("commit_descriptors = false, want it set by --commit-descriptors")
				}
			},
		},
		{
			name: "a full label is a bazel source",
			arg:  "//proto/grpcview/echo/v1:grpcviewechov1_proto",
			check: func(t *testing.T, fc *fakeClient) {
				got := onlyAdd(t, fc)
				if got.GetBazel().GetLabel() != "//proto/grpcview/echo/v1:grpcviewechov1_proto" {
					t.Errorf("bazel.label = %q, want the label passed through verbatim: the server canonicalizes",
						got.GetBazel().GetLabel())
				}
				if got.GetReflection() != nil || got.GetDescriptorSet() != nil {
					t.Errorf("reflection = %v / descriptor_set = %q, want a label to be neither",
						got.GetReflection(), got.GetDescriptorSet())
				}
				if got.GetFileName() != "" || got.GetPath() != "" {
					t.Errorf("file_name = %q / path = %q, want both unset: a label is its own recipe",
						got.GetFileName(), got.GetPath())
				}
			},
		},
		{
			name: "an external repository's label is one too",
			arg:  "@buf_deps//acme/payments:payments_proto",
			check: func(t *testing.T, fc *fakeClient) {
				if got := onlyAdd(t, fc).GetBazel().GetLabel(); got != "@buf_deps//acme/payments:payments_proto" {
					t.Errorf("bazel.label = %q, want the @repo spelling kept", got)
				}
			},
		},
		{
			name:   "--commit-descriptors rides along on a bazel source: the fresh-clone-without-bazel answer",
			arg:    "//proto/grpcview/echo/v1:grpcviewechov1_proto",
			commit: true,
			check: func(t *testing.T, fc *fakeClient) {
				got := onlyAdd(t, fc)
				if !got.GetCommitDescriptors() {
					t.Error("commit_descriptors = false, want it set by --commit-descriptors")
				}
				if got.GetBazel() == nil {
					t.Error("bazel = nil, want the label arm: --commit-descriptors must not change the kind")
				}
			},
		},
		{
			name:       "--tls on a label is refused the same way it is on a file",
			arg:        "//proto/grpcview/echo/v1:grpcviewechov1_proto",
			tls:        true,
			wantErrHas: "--tls does not apply",
			wantCode:   2,
		},
		{
			name: "anything that does not stat is a reflection target",
			arg:  "localhost:50055",
			check: func(t *testing.T, fc *fakeClient) {
				got := onlyAdd(t, fc)
				if got.GetReflection().GetAddress() != "localhost:50055" {
					t.Errorf("reflection.address = %q, want %q", got.GetReflection().GetAddress(), "localhost:50055")
				}
				if got.GetReflection().GetTls() != nil {
					t.Errorf("reflection.tls = %v, want nil without --tls", got.GetReflection().GetTls())
				}
				if got.GetDescriptorSet() != nil || got.GetFileName() != "" {
					t.Errorf("descriptor_set = %q / file_name = %q, want both unset for a reflection source",
						got.GetDescriptorSet(), got.GetFileName())
				}
			},
		},
		{
			name: "a host:port is an ADDRESS and never the `pkg:target` shorthand",
			arg:  "localhost:8080",
			check: func(t *testing.T, fc *fakeClient) {
				got := onlyAdd(t, fc)
				if got.GetReflection().GetAddress() != "localhost:8080" {
					t.Errorf("reflection.address = %q, want %q", got.GetReflection().GetAddress(), "localhost:8080")
				}
				if got.GetBazel() != nil {
					t.Errorf("bazel = %v, want nil: a colon does not make a label", got.GetBazel())
				}
			},
		},
		{
			name: "a public host:port is an address too",
			arg:  "example.com:443",
			check: func(t *testing.T, fc *fakeClient) {
				got := onlyAdd(t, fc)
				if got.GetReflection().GetAddress() != "example.com:443" {
					t.Errorf("reflection.address = %q, want %q", got.GetReflection().GetAddress(), "example.com:443")
				}
				if got.GetBazel() != nil {
					t.Errorf("bazel = %v, want nil", got.GetBazel())
				}
			},
		},
		{
			name: "--tls sets the TLS marker, which is part of the source's identity",
			arg:  "secure.example:443",
			tls:  true,
			check: func(t *testing.T, fc *fakeClient) {
				if onlyAdd(t, fc).GetReflection().GetTls() == nil {
					t.Error("reflection.tls = nil, want the empty TLS message")
				}
			},
		},
		{
			name:       "--tls on a file is refused rather than silently dropped",
			arg:        "{file}",
			tls:        true,
			wantErrHas: "--tls does not apply",
			wantCode:   2,
		},
		{
			name:       "a directory is not a descriptor set",
			arg:        "{dir}",
			wantErrHas: "it is a directory",
			wantCode:   2,
		},
		{
			name:       "a Connect error from the RPC is exit 2",
			arg:        "gone.example:9999",
			fake:       func(fc *fakeClient) { fc.writes.err = connect.NewError(connect.CodeUnavailable, errNoTarget) },
			wantErrHas: `failed to add the definition source "gone.example:9999"`,
			wantCode:   2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			arg := tc.arg
			switch arg {
			case "{file}":
				arg = file
			case "{dir}":
				arg = dir
			}

			fc := newFake()
			if tc.fake != nil {
				tc.fake(fc)
			}

			args := []string{"sources", "add", arg}
			if tc.tls {
				args = append(args, "--tls")
			}
			if tc.commit {
				args = append(args, "--commit-descriptors")
			}
			out, errOut, code := runCLI(fc, "", args...)

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
			if tc.check != nil {
				tc.check(t, fc)
			}
		})
	}
}

func onlyAdd(t *testing.T, fc *fakeClient) *grpcviewv1.AddDescriptorSourceRequest {
	t.Helper()
	if len(fc.writes.addSource) != 1 {
		t.Fatalf("AddDescriptorSource called %d time(s), want 1", len(fc.writes.addSource))
	}
	got := fc.writes.addSource[0]
	if got.GetCollection() != "." {
		t.Errorf("collection = %q, want %q", got.GetCollection(), ".")
	}
	return got
}

func TestSourcesAddFailuresReachNoRPC(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "image.binpb")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("failed to write the fixture: %v", err)
	}

	for _, args := range [][]string{
		{"sources", "add", file, "--tls"},
		{"sources", "add", "//proto/grpcview/echo/v1:grpcviewechov1_proto", "--tls"},
		{"sources", "add", dir},
		{"sources", "add"},
	} {
		fc := newFake()
		if _, _, code := runCLI(fc, "", args...); code != 2 {
			t.Errorf("%v: exit code = %d, want 2", args, code)
		}
		if len(fc.writes.addSource) != 0 {
			t.Errorf("%v: AddDescriptorSource called %d time(s), want 0", args, len(fc.writes.addSource))
		}
	}
}

func TestSourcesAddRecordsAnAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "image.binpb"), []byte("x"), 0o600); err != nil {
		t.Fatalf("failed to write the fixture: %v", err)
	}
	t.Chdir(dir)

	fc := newFake()
	out, errOut, code := runCLI(fc, "", "sources", "add", "image.binpb")
	assertSilent(t, out, errOut, code)

	got := onlyAdd(t, fc).GetPath()
	if !filepath.IsAbs(got) {
		t.Fatalf("path = %q, want an absolute path", got)
	}
	if filepath.Base(got) != "image.binpb" {
		t.Errorf("path = %q, want it to end at the file named on the command line", got)
	}
}

func TestSourcesRefresh(t *testing.T) {
	t.Run("no id refreshes every refreshable source, in priority order, skipping only pathless uploads", func(t *testing.T) {
		fc := newFake()
		fc.snapshot = refreshableWorkspace()

		out, errOut, code := runCLI(fc, "", "sources", "refresh")
		assertSilent(t, out, errOut, code)

		var got []string
		for _, msg := range fc.writes.refreshSource {
			got = append(got, msg.GetId())
		}
		want := []string{
			"reflection:localhost:50055",
			"upload:built.binpb",
			"bazel://proto/grpcview/echo/v1:grpcviewechov1_proto",
		}
		if !slices.Equal(got, want) {
			t.Errorf("refreshed %v, want %v in that order", got, want)
		}
	})

	t.Run("an id refreshes exactly that source and reads no snapshot", func(t *testing.T) {
		fc := newFake()
		fc.snapshot = sourcesWorkspace()

		out, errOut, code := runCLI(fc, "", "sources", "refresh", "upload:echo.binpb")
		assertSilent(t, out, errOut, code)

		if len(fc.writes.refreshSource) != 1 {
			t.Fatalf("RefreshDescriptorSource called %d time(s), want 1", len(fc.writes.refreshSource))
		}
		if got := fc.writes.refreshSource[0].GetId(); got != "upload:echo.binpb" {
			t.Errorf("id = %q, want %q", got, "upload:echo.binpb")
		}
		if len(fc.gotGet) != 0 {
			t.Errorf("Get called %d time(s), want 0: an explicit id needs no source list", len(fc.gotGet))
		}
	})

	t.Run("a workspace with no sources refreshes nothing, successfully", func(t *testing.T) {
		fc := newFake()
		out, errOut, code := runCLI(fc, "", "sources", "refresh")
		assertSilent(t, out, errOut, code)
		if len(fc.writes.refreshSource) != 0 {
			t.Errorf("RefreshDescriptorSource called %d time(s), want 0", len(fc.writes.refreshSource))
		}
	})

	t.Run("the run stops at the first failure", func(t *testing.T) {
		fc := newFake()
		fc.snapshot = sourcesWorkspace()
		fc.writes.err = connect.NewError(connect.CodeUnavailable, errNoTarget)

		out, errOut, code := runCLI(fc, "", "sources", "refresh")

		if out != "" {
			t.Errorf("stdout = %q, want empty", out)
		}
		if code != 2 {
			t.Errorf("exit code = %d, want 2", code)
		}
		if !strings.Contains(errOut, `failed to refresh the definition source "reflection:localhost:50055"`) {
			t.Errorf("stderr = %q, want it to name the source that failed", errOut)
		}
		if len(fc.writes.refreshSource) != 1 {
			t.Errorf("RefreshDescriptorSource called %d time(s), want 1: the run stops at the first failure",
				len(fc.writes.refreshSource))
		}
	})
}

func TestSourcesRmAndReorder(t *testing.T) {
	t.Run("rm passes the id through", func(t *testing.T) {
		fc := newFake()
		out, errOut, code := runCLI(fc, "", "sources", "rm", "upload:echo.binpb")
		assertSilent(t, out, errOut, code)

		if len(fc.writes.removeSource) != 1 {
			t.Fatalf("RemoveDescriptorSource called %d time(s), want 1", len(fc.writes.removeSource))
		}
		got := fc.writes.removeSource[0]
		if got.GetId() != "upload:echo.binpb" || got.GetCollection() != "." {
			t.Errorf("got %+v, want id=%q workspace=%q", got, "upload:echo.binpb", ".")
		}
	})

	t.Run("reorder sends the ids verbatim, in the order given", func(t *testing.T) {
		fc := newFake()
		out, errOut, code := runCLI(fc, "", "sources", "reorder", "a", "b", "c")
		assertSilent(t, out, errOut, code)

		if len(fc.writes.reorderSource) != 1 {
			t.Fatalf("ReorderDescriptorSources called %d time(s), want 1", len(fc.writes.reorderSource))
		}
		if got := fc.writes.reorderSource[0].GetIds(); !slices.Equal(got, []string{"a", "b", "c"}) {
			t.Errorf("ids = %v, want [a b c]", got)
		}
		if len(fc.gotGet) != 0 {
			t.Errorf("Get called %d time(s), want 0: reorder trusts the caller's list", len(fc.gotGet))
		}
	})

	t.Run("reorder needs at least one id", func(t *testing.T) {
		fc := newFake()
		if _, _, code := runCLI(fc, "", "sources", "reorder"); code != 2 {
			t.Errorf("exit code = %d, want 2", code)
		}
		if len(fc.writes.reorderSource) != 0 {
			t.Errorf("ReorderDescriptorSources called %d time(s), want 0", len(fc.writes.reorderSource))
		}
	})
}

func TestSourcesCommit(t *testing.T) {
	t.Run("commit asks for the flag on", func(t *testing.T) {
		fc := newFake()
		out, errOut, code := runCLI(fc, "", "sources", "commit", "upload:echo.binpb")
		assertSilent(t, out, errOut, code)

		if len(fc.writes.commitSource) != 1 {
			t.Fatalf("SetDescriptorSourceCommit called %d time(s), want 1", len(fc.writes.commitSource))
		}
		got := fc.writes.commitSource[0]
		if got.GetId() != "upload:echo.binpb" || got.GetCollection() != "." || !got.GetCommit() {
			t.Errorf("got %+v, want id=upload:echo.binpb collection=. commit=true", got)
		}
	})

	t.Run("--off asks for it off", func(t *testing.T) {
		fc := newFake()
		out, errOut, code := runCLI(fc, "", "sources", "commit", "upload:echo.binpb", "--off")
		assertSilent(t, out, errOut, code)

		if len(fc.writes.commitSource) != 1 {
			t.Fatalf("SetDescriptorSourceCommit called %d time(s), want 1", len(fc.writes.commitSource))
		}
		if fc.writes.commitSource[0].GetCommit() {
			t.Error("commit = true with --off")
		}
	})

	t.Run("an id is required", func(t *testing.T) {
		fc := newFake()
		if _, _, code := runCLI(fc, "", "sources", "commit"); code != 2 {
			t.Errorf("exit code = %d, want 2", code)
		}
		if len(fc.writes.commitSource) != 0 {
			t.Errorf("SetDescriptorSourceCommit called %d time(s), want 0", len(fc.writes.commitSource))
		}
	})

	t.Run("a refusal from the RPC is exit 2 and names the source", func(t *testing.T) {
		fc := newFake()
		fc.writes.err = connect.NewError(connect.CodeInvalidArgument, errors.New("has never resolved"))

		out, errOut, code := runCLI(fc, "", "sources", "commit", "reflection:gone.example:9999")

		if out != "" {
			t.Errorf("stdout = %q, want empty", out)
		}
		if code != 2 {
			t.Errorf("exit code = %d, want 2", code)
		}
		if !strings.Contains(errOut, `definition source "reflection:gone.example:9999"`) {
			t.Errorf("stderr = %q, want it to name the source", errOut)
		}
	})
}

func TestFolderCreate(t *testing.T) {
	for _, tc := range []struct {
		name     string
		arg      string
		wantPath []string
		wantName string
	}{
		{name: "a nested path splits into parent folders and the new name", arg: "A/B", wantPath: []string{"A"}, wantName: "B"},
		{name: "a bare name lands at the collection root", arg: "A", wantPath: nil, wantName: "A"},
		{name: "two levels of parent", arg: "A/B/C", wantPath: []string{"A", "B"}, wantName: "C"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fc := newFake()
			out, errOut, code := runCLI(fc, "", "folder", "create", tc.arg)
			assertSilent(t, out, errOut, code)

			if len(fc.writes.createFolder) != 1 {
				t.Fatalf("CreateFolder called %d time(s), want 1", len(fc.writes.createFolder))
			}
			got := fc.writes.createFolder[0]
			if !slices.Equal(got.GetPath(), tc.wantPath) {
				t.Errorf("path = %v, want %v", got.GetPath(), tc.wantPath)
			}
			if got.GetItemName() != tc.wantName {
				t.Errorf("item_name = %q, want %q", got.GetItemName(), tc.wantName)
			}
			if got.GetCollection() != "." {
				t.Errorf("collection = %q, want %q", got.GetCollection(), ".")
			}
		})
	}
}

func TestRequestCreate(t *testing.T) {
	t.Run("without a body it is one CreateRequest and no UpdateRequest", func(t *testing.T) {
		fc := newFake()
		out, errOut, code := runCLI(fc, "",
			"request", "create", "A/B", "--service", "grpcview.echo.v1.EchoService", "--method", "Unary")
		assertSilent(t, out, errOut, code)

		if len(fc.writes.createRequest) != 1 {
			t.Fatalf("CreateRequest called %d time(s), want 1", len(fc.writes.createRequest))
		}
		got := fc.writes.createRequest[0]
		if !slices.Equal(got.GetPath(), []string{"A"}) {
			t.Errorf("path = %v, want [A]", got.GetPath())
		}
		if got.GetItemName() != "B" {
			t.Errorf("item_name = %q, want %q", got.GetItemName(), "B")
		}
		if got.GetService() != "grpcview.echo.v1.EchoService" || got.GetMethod() != "Unary" {
			t.Errorf("service/method = %q/%q, want %q/%q",
				got.GetService(), got.GetMethod(), "grpcview.echo.v1.EchoService", "Unary")
		}
		if len(fc.writes.updateRequest) != 0 {
			t.Errorf("UpdateRequest called %d time(s), want 0 without -f", len(fc.writes.updateRequest))
		}
	})

	t.Run("-f seeds the body with a second call, after the create", func(t *testing.T) {
		body := "{\n  \"message\": \"hi\"\n}\n"
		file := filepath.Join(t.TempDir(), "body.json")
		if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
			t.Fatalf("failed to write the fixture: %v", err)
		}

		fc := newFake()
		out, errOut, code := runCLI(fc, "",
			"request", "create", "A/B", "--service", "grpcview.echo.v1.EchoService", "--method", "Unary", "-f", file)
		assertSilent(t, out, errOut, code)

		if want := []string{"CreateRequest", "UpdateRequest"}; !slices.Equal(fc.writes.order, want) {
			t.Fatalf("calls = %v, want %v", fc.writes.order, want)
		}
		got := fc.writes.updateRequest[0]
		if got.DraftBody == nil {
			t.Fatal("draft_body is unset, want the file's bytes")
		}
		if got.GetDraftBody() != body {
			t.Errorf("draft_body = %q, want the file's bytes unchanged (%q)", got.GetDraftBody(), body)
		}
		if !slices.Equal(got.GetPath(), []string{"A"}) || got.GetItemName() != "B" {
			t.Errorf("the patch addressed %v/%q, want [A]/%q", got.GetPath(), got.GetItemName(), "B")
		}
		if got.Service != nil || got.Method != nil || got.Name != nil || got.DraftMetadataScript != nil {
			t.Errorf("the patch set a field other than draft_body: %+v", got)
		}
	})

	t.Run("-f - reads the body from stdin", func(t *testing.T) {
		fc := newFake()
		out, errOut, code := runCLI(fc, `{"message":"hi"}`,
			"request", "create", "B", "--service", "S", "--method", "M", "-f", "-")
		assertSilent(t, out, errOut, code)

		if len(fc.writes.updateRequest) != 1 {
			t.Fatalf("UpdateRequest called %d time(s), want 1", len(fc.writes.updateRequest))
		}
		if got := fc.writes.updateRequest[0].GetDraftBody(); got != `{"message":"hi"}` {
			t.Errorf("draft_body = %q, want the stdin bytes", got)
		}
	})

	t.Run("a missing required flag is exit 2 and creates nothing", func(t *testing.T) {
		for _, args := range [][]string{
			{"request", "create", "A/B", "--method", "Unary"},
			{"request", "create", "A/B", "--service", "grpcview.echo.v1.EchoService"},
			{"request", "create", "--service", "S", "--method", "M"},
		} {
			fc := newFake()
			out, errOut, code := runCLI(fc, "", args...)
			if code != 2 {
				t.Errorf("%v: exit code = %d, want 2", args, code)
			}
			if out != "" {
				t.Errorf("%v: stdout = %q, want empty", args, out)
			}
			if !strings.HasPrefix(errOut, "grpcview: ") {
				t.Errorf("%v: stderr = %q, want the one prefixed line", args, errOut)
			}
			if strings.Contains(errOut, "Usage:") {
				t.Errorf("%v: stderr = %q, want no usage dump for a flag error", args, errOut)
			}
			if len(fc.writes.createRequest) != 0 {
				t.Errorf("%v: CreateRequest called %d time(s), want 0", args, len(fc.writes.createRequest))
			}
		}
	})
}

func TestRequestRm(t *testing.T) {
	fc := newFake()
	out, errOut, code := runCLI(fc, "", "request", "rm", "Auth/Login")
	assertSilent(t, out, errOut, code)

	if len(fc.writes.deleteRequest) != 1 {
		t.Fatalf("DeleteRequest called %d time(s), want 1", len(fc.writes.deleteRequest))
	}
	got := fc.writes.deleteRequest[0]
	if !slices.Equal(got.GetPath(), []string{"Auth"}) || got.GetItemName() != "Login" {
		t.Errorf("addressed %v/%q, want [Auth]/%q", got.GetPath(), got.GetItemName(), "Login")
	}
	if len(fc.gotGet) != 0 {
		t.Errorf("Get called %d time(s), want 0", len(fc.gotGet))
	}
}

func TestRequestMv(t *testing.T) {
	for _, tc := range []struct {
		name        string
		args        []string
		wantPath    []string
		wantName    string
		wantNewPath []string
		wantBefore  *string
	}{
		{
			name:        "a reparent into another folder",
			args:        []string{"Auth/Login", "Archive"},
			wantPath:    []string{"Auth"},
			wantName:    "Login",
			wantNewPath: []string{"Archive"},
		},
		{
			name:        "a pure reorder: the destination is the item's current parent",
			args:        []string{"Auth/Login", "Auth", "--before", "Logout"},
			wantPath:    []string{"Auth"},
			wantName:    "Login",
			wantNewPath: []string{"Auth"},
			wantBefore:  strptr("Logout"),
		},
		{
			name:        "an empty destination is the collection root",
			args:        []string{"Auth/Login", ""},
			wantPath:    []string{"Auth"},
			wantName:    "Login",
			wantNewPath: nil,
		},
		{
			name:        `so is "/"`,
			args:        []string{"Auth/Login", "/"},
			wantPath:    []string{"Auth"},
			wantName:    "Login",
			wantNewPath: nil,
		},
		{
			name:        "--before at the root reorders a top-level item",
			args:        []string{"Stream", "", "--before", "Upload"},
			wantPath:    nil,
			wantName:    "Stream",
			wantNewPath: nil,
			wantBefore:  strptr("Upload"),
		},
		{
			name:        "a nested destination keeps every segment",
			args:        []string{"Stream", "A/B/C"},
			wantPath:    nil,
			wantName:    "Stream",
			wantNewPath: []string{"A", "B", "C"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fc := newFake()
			out, errOut, code := runCLI(fc, "", append([]string{"request", "mv"}, tc.args...)...)
			assertSilent(t, out, errOut, code)

			if len(fc.writes.moveItem) != 1 {
				t.Fatalf("MoveItem called %d time(s), want 1", len(fc.writes.moveItem))
			}
			got := fc.writes.moveItem[0]
			if !slices.Equal(got.GetPath(), tc.wantPath) {
				t.Errorf("path = %v, want %v", got.GetPath(), tc.wantPath)
			}
			if got.GetItemName() != tc.wantName {
				t.Errorf("item_name = %q, want %q", got.GetItemName(), tc.wantName)
			}
			if !slices.Equal(got.GetNewPath(), tc.wantNewPath) {
				t.Errorf("new_path = %v, want %v", got.GetNewPath(), tc.wantNewPath)
			}
			switch {
			case tc.wantBefore == nil && got.Before != nil:
				t.Errorf("before = %q, want unset without --before", got.GetBefore())
			case tc.wantBefore != nil && got.Before == nil:
				t.Errorf("before is unset, want %q", *tc.wantBefore)
			case tc.wantBefore != nil && got.GetBefore() != *tc.wantBefore:
				t.Errorf("before = %q, want %q", got.GetBefore(), *tc.wantBefore)
			}
		})
	}
}

func strptr(s string) *string { return &s }

func scriptsWorkspace() *grpcviewv1.Collection {
	ws := testWorkspace()
	ws.Scripts = []*grpcviewv1.Script{
		{Path: "scripts/seed.ts", Source: "export default () => ({})\n"},
		{Path: "scripts/auth-header.ts", Source: "export default (ctx) => ctx.next()\n"},
		{Path: "scripts/scratch.ts", Source: "1 + 1\n"},
	}
	return ws
}

const scriptsGolden = `scripts/seed.ts
scripts/auth-header.ts
scripts/scratch.ts
`

func TestScriptLs(t *testing.T) {
	fc := newFake()
	fc.snapshot = scriptsWorkspace()

	out, errOut, code := runCLI(fc, "", "script", "ls")

	if out != scriptsGolden {
		t.Errorf("stdout:\n%s\nwant:\n%s\n(got %q)", out, scriptsGolden, out)
	}
	if errOut != "" {
		t.Errorf("stderr = %q, want empty", errOut)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if len(fc.writes.runScript) != 0 {
		t.Errorf("script ls ran %d script(s); it must run none", len(fc.writes.runScript))
	}
}

func TestScriptLsEmpty(t *testing.T) {
	fc := newFake()
	out, errOut, code := runCLI(fc, "", "script", "ls")
	if out != "" || errOut != "" || code != 0 {
		t.Errorf("got (%q, %q, %d), want a successful empty listing", out, errOut, code)
	}
}

func TestScriptRun(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		stdin      string
		fake       func(*fakeClient)
		wantSource string
		wantRuns   int
		wantOut    string
		wantErr    string
		wantErrHas string
		wantCode   int
	}{
		{
			name: "a saved path sends that script's SOURCE",
			args: []string{"script", "run", "scripts/seed.ts"},
			fake: func(fc *fakeClient) {
				fc.writes.script = &grpcviewv1.RunScriptResponse{Value: strptr(`{"id":"u_1"}`)}
			},
			wantSource: "export default () => ({})\n",
			wantRuns:   1,
			wantOut:    "{\"id\":\"u_1\"}\n",
		},
		{
			name:       "an unknown path is exit 2 and runs NOTHING",
			args:       []string{"script", "run", "scripts/nope.ts"},
			wantRuns:   0,
			wantErrHas: `unknown script "scripts/nope.ts"`,
			wantCode:   2,
		},
		{
			name:       "- reads the source from stdin",
			args:       []string{"script", "run", "-"},
			stdin:      "export default (ctx) => ctx.next()",
			wantSource: "export default (ctx) => ctx.next()",
			wantRuns:   1,
		},
		{
			name:       "- runs a scratchpad expression the same way",
			args:       []string{"script", "run", "-"},
			stdin:      "1 + 1",
			wantSource: "1 + 1",
			wantRuns:   1,
		},
		{
			name:       "empty stdin is a failure, not an empty script",
			args:       []string{"script", "run", "-"},
			stdin:      "   \n",
			wantRuns:   0,
			wantErrHas: "no script on stdin",
			wantCode:   2,
		},
		{
			name: "a thrown exception is exit 1 with an EMPTY stdout",
			args: []string{"script", "run", "scripts/seed.ts"},
			fake: func(fc *fakeClient) {
				fc.writes.script = &grpcviewv1.RunScriptResponse{Error: &grpcviewv1.ScriptError{
					Message: "TypeError: x is not a function",
					Stack:   "at <anonymous>\n  at run",
					Line:    7,
				}}
			},
			wantRuns: 1,
			wantOut:  "",
			wantErr:  "grpcview: the script \"scripts/seed.ts\" threw: TypeError: x is not a function (line 7)\n",
			wantCode: 1,
		},
		{
			name: "logs go to stderr with their level; the value still goes to stdout",
			args: []string{"script", "run", "scripts/seed.ts"},
			fake: func(fc *fakeClient) {
				fc.writes.script = &grpcviewv1.RunScriptResponse{
					Value: strptr("42"),
					Logs: []*grpcviewv1.ScriptLog{
						{Level: "log", Message: "starting"},
						{Level: "warn", Message: "slow"},
					},
				}
			},
			wantRuns: 1,
			wantOut:  "42\n",
			wantErr:  "log: starting\nwarn: slow\n",
		},
		{
			name:     "a script that returned undefined prints nothing at all",
			args:     []string{"script", "run", "scripts/seed.ts"},
			fake:     func(fc *fakeClient) { fc.writes.script = &grpcviewv1.RunScriptResponse{} },
			wantRuns: 1,
			wantOut:  "",
		},
		{
			name:       "a Connect error is exit 2 — the run never happened",
			args:       []string{"script", "run", "scripts/seed.ts"},
			fake:       func(fc *fakeClient) { fc.writes.err = connect.NewError(connect.CodeInternal, errNoTarget) },
			wantRuns:   1,
			wantErrHas: `failed to run the script "scripts/seed.ts"`,
			wantCode:   2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fc := newFake()
			fc.snapshot = scriptsWorkspace()
			if tc.fake != nil {
				tc.fake(fc)
			}

			out, errOut, code := runCLI(fc, tc.stdin, tc.args...)

			if out != tc.wantOut {
				t.Errorf("stdout = %q, want %q", out, tc.wantOut)
			}
			if tc.wantErr != "" && errOut != tc.wantErr {
				t.Errorf("stderr = %q, want %q", errOut, tc.wantErr)
			}
			if tc.wantErrHas != "" && !strings.Contains(errOut, tc.wantErrHas) {
				t.Errorf("stderr = %q, want it to contain %q", errOut, tc.wantErrHas)
			}
			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d", code, tc.wantCode)
			}

			if len(fc.writes.runScript) != tc.wantRuns {
				t.Fatalf("RunScript called %d time(s), want %d", len(fc.writes.runScript), tc.wantRuns)
			}
			if tc.wantRuns == 0 {
				return
			}
			got := fc.writes.runScript[0]
			if got.GetCollection() != "." {
				t.Errorf("collection = %q, want %q", got.GetCollection(), ".")
			}
			if tc.wantSource != "" && got.GetSource() != tc.wantSource {
				t.Errorf("source = %q, want %q", got.GetSource(), tc.wantSource)
			}
		})
	}
}

func TestWriteParents(t *testing.T) {
	for _, tc := range []struct {
		args       []string
		wantErrHas string
	}{
		{args: []string{"request"}, wantErrHas: `"grpcview request" needs a subcommand: create, rm, mv`},
		{args: []string{"folder"}, wantErrHas: `"grpcview folder" needs a subcommand: create`},
		{args: []string{"script"}, wantErrHas: `"grpcview script" needs a subcommand: ls, run`},
		{args: []string{"sources"}, wantErrHas: `"grpcview sources" needs a subcommand: ls, add, commit, refresh, rm, reorder`},
		{args: []string{"request", "delete", "A"}, wantErrHas: `unknown command "delete" for "grpcview request"`},
		{args: []string{"folder", "rm", "A"}, wantErrHas: `unknown command "rm" for "grpcview folder"`},
		{args: []string{"script", "create", "A"}, wantErrHas: `unknown command "create" for "grpcview script"`},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			fc := newFake()
			out, errOut, code := runCLI(fc, "", tc.args...)

			if out != "" {
				t.Errorf("stdout = %q, want empty: usage never goes to stdout", out)
			}
			if !strings.Contains(errOut, tc.wantErrHas) {
				t.Errorf("stderr = %q, want it to contain %q", errOut, tc.wantErrHas)
			}
			if !strings.Contains(errOut, "Usage:") {
				t.Errorf("stderr = %q, want the usage dump", errOut)
			}
			if code != 2 {
				t.Errorf("exit code = %d, want 2", code)
			}
			if len(fc.writes.order) != 0 {
				t.Errorf("mutations = %v, want none", fc.writes.order)
			}
		})
	}
}

func TestWritePathErrors(t *testing.T) {
	for _, args := range [][]string{
		{"folder", "create", "A/"},
		{"folder", "create", ""},
		{"request", "rm", "A/"},
		{"request", "mv", "A/", "B"},
		{"request", "create", "A/", "--service", "S", "--method", "M"},
	} {
		fc := newFake()
		out, errOut, code := runCLI(fc, "", args...)

		if code != 2 {
			t.Errorf("%v: exit code = %d, want 2", args, code)
		}
		if out != "" {
			t.Errorf("%v: stdout = %q, want empty", args, out)
		}
		if !strings.Contains(errOut, "invalid path") {
			t.Errorf("%v: stderr = %q, want it to name the invalid path", args, errOut)
		}
		if len(fc.writes.order) != 0 {
			t.Errorf("%v: mutations = %v, want none", args, fc.writes.order)
		}
	}
}

func TestWriteRPCFailures(t *testing.T) {
	for _, args := range [][]string{
		{"sources", "add", "localhost:50055"},
		{"sources", "refresh", "some:id"},
		{"sources", "rm", "some:id"},
		{"sources", "reorder", "a", "b"},
		{"folder", "create", "A"},
		{"request", "create", "A", "--service", "S", "--method", "M"},
		{"request", "rm", "A"},
		{"request", "mv", "A", "B"},
	} {
		fc := newFake()
		fc.writes.err = connect.NewError(connect.CodeFailedPrecondition, errNoTarget)

		out, errOut, code := runCLI(fc, "", args...)

		if code != 2 {
			t.Errorf("%v: exit code = %d, want 2", args, code)
		}
		if out != "" {
			t.Errorf("%v: stdout = %q, want empty", args, out)
		}
		if !strings.HasPrefix(errOut, "grpcview: ") || strings.Count(errOut, "\n") != 1 {
			t.Errorf("%v: stderr = %q, want exactly one prefixed line", args, errOut)
		}
	}
}
