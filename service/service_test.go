package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/daemon"
)

type stubServerService struct {
	grpcviewv1.UnimplementedServerServiceHandler
	root    string
	stopped chan struct{}
}

func (s *stubServerService) ServerInfo(
	_ context.Context,
	_ *connect.Request[grpcviewv1.ServerInfoRequest],
) (*connect.Response[grpcviewv1.ServerInfoResponse], error) {
	return connect.NewResponse(&grpcviewv1.ServerInfoResponse{
		WorkspaceRoot: s.root,
		Pid:           int32(os.Getpid()),
	}), nil
}

func (s *stubServerService) Shutdown(
	_ context.Context,
	_ *connect.Request[grpcviewv1.ShutdownRequest],
) (*connect.Response[grpcviewv1.ShutdownResponse], error) {
	close(s.stopped)
	return connect.NewResponse(&grpcviewv1.ShutdownResponse{}), nil
}

// Stands up a stub ServerService for `root` and registers it, mirroring
// service/daemon/connect_test.go's serveStub — ListServers/StopServer verify a registration
// the same way Connect does, so the test doubles have to answer the same RPC.
func serveStub(t *testing.T, root string) (*stubServerService, int) {
	t.Helper()
	stub := &stubServerService{root: root, stopped: make(chan struct{})}
	mux := http.NewServeMux()
	mux.Handle(grpcviewv1.NewServerServiceHandler(stub))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	parsed, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil {
		t.Fatal(err)
	}
	if err := daemon.Write(daemon.Registration{Port: port, Pid: os.Getpid(), Root: root}); err != nil {
		t.Fatal(err)
	}
	return stub, port
}

func TestListServers_verifiesEveryRowAndMarksCurrent(t *testing.T) {
	t.Setenv("GRPCVIEW_CONFIG_DIR", t.TempDir())
	rootA := t.TempDir()
	rootB := t.TempDir()
	_, portA := serveStub(t, rootA)
	serveStub(t, rootB)

	svc := &serverService{root: rootA, port: portA}
	res, err := svc.ListServers(context.Background(), connect.NewRequest(&grpcviewv1.ListServersRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	entries := res.Msg.GetServers()
	if len(entries) != 2 {
		t.Fatalf("ListServers returned %d entries, want 2: %+v", len(entries), entries)
	}

	current := 0
	for _, e := range entries {
		if !e.GetRunning() {
			t.Errorf("entry %q reported not running, want running (both stubs answer)", e.GetWorkspaceRoot())
		}
		if e.GetCurrent() {
			current++
			if e.GetWorkspaceRoot() != rootA {
				t.Errorf("current entry root = %q, want %q", e.GetWorkspaceRoot(), rootA)
			}
		}
	}
	if current != 1 {
		t.Errorf("%d entries marked current, want exactly 1", current)
	}
}

func TestStopServer_asksAnotherRootOverTheWire(t *testing.T) {
	t.Setenv("GRPCVIEW_CONFIG_DIR", t.TempDir())
	rootA := t.TempDir()
	rootB := t.TempDir()
	stubB, _ := serveStub(t, rootB)
	_, portA := serveStub(t, rootA)

	svc := &serverService{root: rootA, port: portA, stop: func() {}}
	// The stub's registered pid is this test process, which never exits, so daemon.Stop's
	// wait-for-exit loop would otherwise run to its own 10s deadline and then SIGTERM this very
	// process. A short ctx makes it bail out on ctx.Done() first instead —
	// service/daemon/connect_test.go's TestStop_asksOverTheWire uses the same trick for the same
	// reason. The RPC's own error return is not the assertion here; that the stub actually got
	// asked is, and that happens synchronously before the wait loop is ever entered.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, _ = svc.StopServer(ctx, connect.NewRequest(&grpcviewv1.StopServerRequest{WorkspaceRoot: rootB}))

	select {
	case <-stubB.stopped:
	default:
		t.Fatal("StopServer did not ask the other workspace's server to shut down")
	}
}

func TestStopServer_ownRootCallsLocalStop(t *testing.T) {
	t.Setenv("GRPCVIEW_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	stopped := make(chan struct{})
	svc := &serverService{root: root, stop: func() { close(stopped) }}

	_, err := svc.StopServer(
		context.Background(),
		connect.NewRequest(&grpcviewv1.StopServerRequest{WorkspaceRoot: root}),
	)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("StopServer(own root) did not call the local stop func")
	}
}

// Naming a root nothing is running for is a success — the caller asked for it to be stopped,
// and it already is.
func TestStopServer_absentRegistrationIsSilentSuccess(t *testing.T) {
	t.Setenv("GRPCVIEW_CONFIG_DIR", t.TempDir())
	svc := &serverService{root: t.TempDir()}
	_, err := svc.StopServer(
		context.Background(),
		connect.NewRequest(&grpcviewv1.StopServerRequest{WorkspaceRoot: t.TempDir()}),
	)
	if err != nil {
		t.Fatalf("StopServer against an absent registration = %v, want nil", err)
	}
}

func TestStopServer_requiresAWorkspaceRoot(t *testing.T) {
	svc := &serverService{root: t.TempDir()}
	_, err := svc.StopServer(
		context.Background(),
		connect.NewRequest(&grpcviewv1.StopServerRequest{}),
	)
	if err == nil {
		t.Fatal("StopServer with no workspace_root = nil error, want InvalidArgument")
	}
}
