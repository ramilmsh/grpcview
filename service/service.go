package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/cors"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	_ "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	"connectrpc.com/grpcreflect"

	"codeberg.org/ramilmsh/grpcview/service/daemon"
	"codeberg.org/ramilmsh/grpcview/service/workspace"
	"codeberg.org/ramilmsh/grpcview/service/wsroot"

	connectcors "connectrpc.com/cors"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

type Options struct {
	Port int
	// The port was named on the command line, so a busy one is an error rather than a reason
	// to take another. Unpinned, a second workspace's server falls back to an ephemeral port
	// instead of refusing to start.
	PortPinned bool
	Root       string
	DevOrigins []string
	// Zero never idles out, which is every hand-run server. Only a client that spawned one
	// passes this.
	IdleTimeout time.Duration
	// Publish a registration file so clients of this workspace find this process. False for
	// the dev server, which serves a dummy index page and must stay out of the registry.
	Register    bool
	OpenBrowser bool
	Version     string
	// Where "serving at …" and browser notes go. Defaults to stderr.
	Notes io.Writer
}

const drainTimeout = 30 * time.Second

func Run(
	ctx context.Context,
	indexPage io.ReadCloser,
	opts Options,
) error {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))

	notes := opts.Notes
	if notes == nil {
		notes = os.Stderr
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to resolve the current directory: %w", err)
	}
	root, warn, err := wsroot.Discover(opts.Root, cwd)
	if err != nil {
		return fmt.Errorf("failed to resolve workspace root: %w", err)
	}
	if warn != "" {
		logger.WarnContext(ctx, warn)
	}

	ws, err := workspace.New(ctx, root)
	if err != nil {
		return fmt.Errorf("failed to initialize workspace hander: %w", err)
	}
	defer ws.Close(ctx)

	mux := http.NewServeMux()

	reflector := grpcreflect.NewStaticReflector(
		"grpcview.v1.WorkspaceService",
		"grpcview.v1.ServerService",
	)

	mux.Handle(grpcreflect.NewHandlerV1(reflector))
	mux.Handle(grpcreflect.NewHandlerV1Alpha(reflector))

	mux.Handle(grpcviewv1.NewWorkspaceServiceHandler(
		&ws,
		connect.WithInterceptors(
			loggingInterceptor{logger: logger},
		),
	))

	stop := make(chan struct{})
	var once sync.Once
	stopOnce := func() { once.Do(func() { close(stop) }) }
	// Also releases the idle watcher, which blocks on stop for as long as Run has not returned.
	defer stopOnce()

	indexHtml, err := io.ReadAll(indexPage)
	if err != nil {
		return err
	}
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write(indexHtml)
	}))

	var handler http.Handler = mux
	if len(opts.DevOrigins) > 0 {
		handler = cors.New(cors.Options{
			AllowedOrigins: opts.DevOrigins,
			AllowedMethods: connectcors.AllowedMethods(),
			AllowedHeaders: connectcors.AllowedHeaders(),
			ExposedHeaders: connectcors.ExposedHeaders(),
			MaxAge:         int((2 * time.Hour).Seconds()),
		}).Handler(mux)
	}

	var idle *idleTimer
	if opts.IdleTimeout > 0 {
		idle = newIdleTimer(opts.IdleTimeout)
		handler = idle.wrap(handler)
	}

	// Bind first, publish second: the port a client reads has to be the one that is listening.
	listener, err := listen(opts)
	if err != nil {
		return err
	}
	port := listener.Addr().(*net.TCPAddr).Port

	lifecycle := &serverService{
		root:        root,
		version:     opts.Version,
		idleTimeout: opts.IdleTimeout,
		port:        port,
		pid:         os.Getpid(),
		executable:  daemon.SelfExecutable(),
		stop:        stopOnce,
	}
	mux.Handle(grpcviewv1.NewServerServiceHandler(lifecycle))

	url := fmt.Sprintf("http://127.0.0.1:%d", port)

	if opts.Register {
		reg := daemon.Registration{
			Port:        port,
			Pid:         lifecycle.pid,
			Root:        root,
			Executable:  lifecycle.executable,
			Version:     opts.Version,
			IdleTimeout: int64(opts.IdleTimeout),
			StartedUnix: time.Now().Unix(),
		}
		if err := daemon.Write(reg); err != nil {
			listener.Close()
			return fmt.Errorf("failed to publish the server registration: %w", err)
		}
		defer daemon.Remove(root)
	}

	server := &http.Server{Handler: h2c.NewHandler(handler, &http2.Server{})}

	logger.InfoContext(ctx, "starting server", "address", listener.Addr().String(), "workspace", root)

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()

	if opts.OpenBrowser {
		daemon.Open(notes, url)
	}

	go idle.watch(stop, func() {
		logger.InfoContext(ctx, "idle timeout reached", "after", opts.IdleTimeout.String())
		stopOnce()
	})

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
	case <-stop:
	}

	drainCtx, cancel := context.WithTimeout(context.Background(), drainTimeout)
	defer cancel()
	if err := server.Shutdown(drainCtx); err != nil {
		return err
	}
	if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func listen(opts Options) (net.Listener, error) {
	address := net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: opts.Port}
	listener, err := net.Listen("tcp", address.String())
	if err == nil {
		return listener, nil
	}
	if opts.PortPinned || !errors.Is(err, syscall.EADDRINUSE) {
		return nil, err
	}
	// A sibling workspace's server already holds the default port. The registration carries
	// the real one, so nothing downstream has to guess.
	return net.Listen("tcp", (&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)}).String())
}

// serverService answers the two lifecycle RPCs. Deliberately not on workspace.Workspace: the
// answers are properties of this process, and in-process there is no process to describe.
type serverService struct {
	grpcviewv1.UnimplementedServerServiceHandler

	root        string
	version     string
	idleTimeout time.Duration
	port        int
	pid         int
	executable  daemon.Executable
	stop        func()
}

func (s *serverService) ServerInfo(
	_ context.Context,
	_ *connect.Request[grpcviewv1.ServerInfoRequest],
) (*connect.Response[grpcviewv1.ServerInfoResponse], error) {
	return connect.NewResponse(&grpcviewv1.ServerInfoResponse{
		WorkspaceRoot: s.root,
		Pid:           int32(s.pid),
		Port:          int32(s.port),
		Version:       s.version,
		Executable:    s.executable.Proto(),
		IdleTimeout:   durationpb.New(s.idleTimeout),
	}), nil
}

// Returns before it stops: the caller's own connection has to drain, and Shutdown waits for it.
func (s *serverService) Shutdown(
	_ context.Context,
	_ *connect.Request[grpcviewv1.ShutdownRequest],
) (*connect.Response[grpcviewv1.ShutdownResponse], error) {
	go s.stop()
	return connect.NewResponse(&grpcviewv1.ShutdownResponse{}), nil
}

// ListServers answers for the machine, not for this workspace: the registry is per user, and a
// client that can only see the daemon it is already talking to cannot manage the others.
//
// Each row is verified in parallel because a dead registration costs a full probe timeout, and
// a machine with a handful of stale files would otherwise serialize into seconds.
func (s *serverService) ListServers(
	ctx context.Context,
	_ *connect.Request[grpcviewv1.ListServersRequest],
) (*connect.Response[grpcviewv1.ListServersResponse], error) {
	regs, err := daemon.List()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	entries := make([]*grpcviewv1.ServerEntry, len(regs))
	var wg sync.WaitGroup
	for i, reg := range regs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			verified, state := daemon.Verify(ctx, reg)
			entries[i] = &grpcviewv1.ServerEntry{
				WorkspaceRoot: verified.Root,
				Port:          int32(verified.Port),
				Pid:           int32(verified.Pid),
				Version:       verified.Version,
				Executable:    verified.Executable.Proto(),
				IdleTimeout:   durationpb.New(time.Duration(verified.IdleTimeout)),
				StartedUnix:   verified.StartedUnix,
				Running:       state != daemon.StateNone,
				Skewed:        state == daemon.StateSkew,
				Current:       verified.Root == s.root && verified.Port == s.port,
			}
		}()
	}
	wg.Wait()

	return connect.NewResponse(&grpcviewv1.ListServersResponse{Servers: entries}), nil
}

// StopServer is Shutdown pointed at somebody else, and it goes through the same verified path a
// CLI takes: the registration names a port, the server on it has to claim the same root, and
// only then is it asked to exit. A root nothing is running for is a success — the caller asked
// for it to be stopped, and it is.
func (s *serverService) StopServer(
	ctx context.Context,
	req *connect.Request[grpcviewv1.StopServerRequest],
) (*connect.Response[grpcviewv1.StopServerResponse], error) {
	root := req.Msg.GetWorkspaceRoot()
	if root == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("a workspace root is required"))
	}
	if root == s.root {
		go s.stop()
		return connect.NewResponse(&grpcviewv1.StopServerResponse{}), nil
	}

	reg, err := daemon.Read(root)
	if errors.Is(err, os.ErrNotExist) {
		return connect.NewResponse(&grpcviewv1.StopServerResponse{}), nil
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	verified, state := daemon.Verify(ctx, reg)
	if state == daemon.StateNone {
		return connect.NewResponse(&grpcviewv1.StopServerResponse{}), nil
	}
	if err := daemon.Stop(ctx, verified); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&grpcviewv1.StopServerResponse{}), nil
}
