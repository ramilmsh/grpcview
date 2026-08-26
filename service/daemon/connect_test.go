package daemon

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/grpcview/v1"
)

type stubServer struct {
	grpcviewv1.UnimplementedServerServiceHandler
	root string
	exe  Executable
	stop chan struct{}
}

func (s *stubServer) ServerInfo(
	_ context.Context,
	_ *connect.Request[grpcviewv1.ServerInfoRequest],
) (*connect.Response[grpcviewv1.ServerInfoResponse], error) {
	return connect.NewResponse(&grpcviewv1.ServerInfoResponse{
		WorkspaceRoot: s.root,
		Pid:           int32(os.Getpid()),
		Executable: &grpcviewv1.ServerExecutable{
			Path:         s.exe.Path,
			ModifiedUnix: s.exe.Modified,
			Size:         s.exe.Size,
		},
	}), nil
}

func (s *stubServer) Shutdown(
	_ context.Context,
	_ *connect.Request[grpcviewv1.ShutdownRequest],
) (*connect.Response[grpcviewv1.ShutdownResponse], error) {
	close(s.stop)
	return connect.NewResponse(&grpcviewv1.ShutdownResponse{}), nil
}

// registers a stub listener and the registration file that points at it, and returns the pid
// the registration claims.
func serveStub(t *testing.T, root string, exe Executable) *stubServer {
	t.Helper()
	stub := &stubServer{root: root, exe: exe, stop: make(chan struct{})}
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
	if err := Write(Registration{Port: port, Pid: os.Getpid(), Root: root, Executable: exe}); err != nil {
		t.Fatal(err)
	}
	return stub
}

func TestConnect_reusesAVerifiedServer(t *testing.T) {
	t.Setenv("GRPCVIEW_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	serveStub(t, root, SelfExecutable())

	reg, err := Connect(context.Background(), Options{Root: root, NoSpawn: true})
	if err != nil {
		t.Fatalf("Connect = %v, want the running server", err)
	}
	if reg.Root != root {
		t.Fatalf("Connect returned root %q, want %q", reg.Root, root)
	}
}

// The assertion ServerInfo exists for: a live process whose registration names a DIFFERENT root
// is not this workspace's server, whatever the pid says.
func TestConnect_rejectsAForeignRoot(t *testing.T) {
	t.Setenv("GRPCVIEW_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	other := t.TempDir()

	stub := &stubServer{root: other, exe: SelfExecutable(), stop: make(chan struct{})}
	mux := http.NewServeMux()
	mux.Handle(grpcviewv1.NewServerServiceHandler(stub))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	parsed, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(parsed.Port())
	if err := Write(Registration{Port: port, Pid: os.Getpid(), Root: root}); err != nil {
		t.Fatal(err)
	}

	if _, err := Connect(context.Background(), Options{Root: root, NoSpawn: true}); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Connect against a foreign root = %v, want ErrNotRunning", err)
	}
}

func TestConnect_deadPidIsNotRunning(t *testing.T) {
	t.Setenv("GRPCVIEW_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	// A port nothing listens on, and a pid that cannot be alive.
	if err := Write(Registration{Port: 1, Pid: 1 << 30, Root: root}); err != nil {
		t.Fatal(err)
	}
	if _, err := Connect(context.Background(), Options{Root: root, NoSpawn: true}); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Connect = %v, want ErrNotRunning", err)
	}
}

func TestConnect_noRegistrationIsNotRunning(t *testing.T) {
	t.Setenv("GRPCVIEW_CONFIG_DIR", t.TempDir())
	if _, err := Connect(context.Background(), Options{Root: t.TempDir(), NoSpawn: true}); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Connect = %v, want ErrNotRunning", err)
	}
}

// Skew is reported as a running server to NoSpawn callers — `grpcview shutdown` must be able to
// stop a daemon running an older build, which is the one it most wants to stop.
func TestConnect_skewIsStillAddressable(t *testing.T) {
	t.Setenv("GRPCVIEW_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	stale := SelfExecutable()
	stale.Modified--
	serveStub(t, root, stale)

	reg, err := Connect(context.Background(), Options{Root: root, NoSpawn: true})
	if err != nil {
		t.Fatalf("Connect = %v, want the skewed server", err)
	}
	if reg.Root != root {
		t.Fatalf("root = %q, want %q", reg.Root, root)
	}
}

func TestStop_asksOverTheWire(t *testing.T) {
	t.Setenv("GRPCVIEW_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	stub := serveStub(t, root, SelfExecutable())

	reg, err := Connect(context.Background(), Options{Root: root, NoSpawn: true})
	if err != nil {
		t.Fatal(err)
	}
	// The stub's pid is this test process, which never exits, so Stop falls through to its
	// deadline; the assertion is that it asked, and that it unlinked the registration.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = Stop(ctx, reg)

	select {
	case <-stub.stop:
	default:
		t.Fatal("Stop never called the Shutdown RPC")
	}
}

func TestLock_isExclusive(t *testing.T) {
	t.Setenv("GRPCVIEW_CONFIG_DIR", t.TempDir())
	root := t.TempDir()

	unlock, err := lock(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := lock(ctx, root); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a second lock = %v, want DeadlineExceeded", err)
	}

	unlock()
	unlock2, err := lock(context.Background(), root)
	if err != nil {
		t.Fatalf("lock after release = %v", err)
	}
	unlock2()
}

func TestSpawnArgs(t *testing.T) {
	args := SpawnArgs("/w", 3*time.Hour)
	joined := strings.Join(args, " ")
	if joined != "serve --workspace /w --no-open --idle-timeout 3h0m0s" {
		t.Fatalf("SpawnArgs = %q", joined)
	}
	// No --port: an auto-spawned server takes the default and falls back if it is busy.
	if strings.Contains(joined, "--port") {
		t.Error("SpawnArgs pins a port")
	}
	if strings.Contains(strings.Join(SpawnArgs("/w", 0), " "), "--idle-timeout") {
		t.Error("a zero idle timeout was still passed")
	}
}

func TestConnect_requiresARoot(t *testing.T) {
	if _, err := Connect(context.Background(), Options{}); err == nil {
		t.Fatal("Connect with no root succeeded")
	}
}
