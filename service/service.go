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
	"time"

	"connectrpc.com/connect"
	"github.com/rs/cors"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	_ "google.golang.org/genproto/googleapis/rpc/status"

	"connectrpc.com/grpcreflect"

	"codeberg.org/ramilmsh/grpcview/service/workspace"

	connectcors "connectrpc.com/cors"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

// Options configures the server. argv parsing lives in the callers, never here.
type Options struct {
	Port int
	// Root is the workspace root the server serves — the repository whose collections and
	// local state this instance owns. Empty falls back to the process's current directory;
	// real --workspace discovery (service/wsroot.Discover) is wired in a later step, so
	// this is deliberately not doing any of that yet.
	Root string
	// DevOrigins are the cross-origin callers allowed to reach the API. The production
	// binary serves its UI same-origin and needs none; only //service/cmd/dev, talking to
	// the vite dev server, sets this. Empty installs no CORS handler at all.
	DevOrigins []string
}

func Run(
	ctx context.Context,
	indexPage io.ReadCloser,
	opts Options,
) error {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))

	root := opts.Root
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to resolve workspace root: %w", err)
		}
	}
	ws, err := workspace.New(ctx, root)
	if err != nil {
		return fmt.Errorf("failed to initialize workspace hander: %w", err)
	}
	defer ws.Close(ctx)

	mux := http.NewServeMux()

	reflector := grpcreflect.NewStaticReflector("grpcview.v1.WorkspaceService")

	mux.Handle(grpcreflect.NewHandlerV1(reflector))
	mux.Handle(grpcreflect.NewHandlerV1Alpha(reflector))

	mux.Handle(grpcviewv1.NewWorkspaceServiceHandler(
		&ws,
		connect.WithInterceptors(
			loggingInterceptor{logger: logger},
		),
	))

	indexHtml, err := io.ReadAll(indexPage)
	if err != nil {
		return err
	}
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write(indexHtml)
	}))

	server := http2.Server{}

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

	// Loopback only: nothing off-machine ever needs to connect, and a LAN-reachable
	// grpcview hands strangers your internal services. There is deliberately no --host.
	address := net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: opts.Port}
	logger.InfoContext(ctx, "starting server", "address", address.String())
	err = http.ListenAndServe(
		address.String(),
		h2c.NewHandler(handler, &server),
	)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
