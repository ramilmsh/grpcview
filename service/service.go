package service

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"connectrpc.com/connect"
	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/cors"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	_ "google.golang.org/genproto/googleapis/rpc/status"

	"connectrpc.com/grpcreflect"

	workspace "codeberg.org/ramilmsh/grpcview/service/workspace"

	connectcors "connectrpc.com/cors"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

func Run(
	ctx context.Context,
	indexPage io.ReadCloser,
) error {
	var port int
	flag.IntVar(&port, "port", 10000, "port to start the server at")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))

	ws, err := workspace.New(ctx)
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

	corsPolicy := cors.New(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: connectcors.AllowedMethods(),
		AllowedHeaders: connectcors.AllowedHeaders(),
		ExposedHeaders: connectcors.ExposedHeaders(),
		MaxAge:         int((2 * time.Hour).Seconds()),
	})

	address := net.TCPAddr{IP: net.IPv4zero, Port: port}
	logger.InfoContext(ctx, "starting server", "address", address.String())
	err = http.ListenAndServe(
		address.String(),
		h2c.NewHandler(corsPolicy.Handler(mux), &server),
	)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
