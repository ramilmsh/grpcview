package service

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"net/http"

	"connectrpc.com/connect"
	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	grpcviewv1 "github.com/ramilmsh/grpcview/service/proto/v1"
)

//go:embed index.html
var frontend embed.FS

type Workspace struct{}

func (w *Workspace) Add(ctx context.Context, request *connect.Request[grpcviewv1.AddRequest]) (*connect.Response[grpcviewv1.AddResponse], error) {
	return connect.NewResponse(&grpcviewv1.AddResponse{}), nil
}

func Run(ctx context.Context) error {
	ws := &Workspace{}
	mux := http.NewServeMux()

	mux.Handle(grpcviewv1.NewWorkspaceHandler(ws))

	indexHtmlFile, err := frontend.Open("index.html")
	if err != nil {
		return err
	}
	indexHtml, err := io.ReadAll(indexHtmlFile)
	if err != nil {
		return err
	}
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write(indexHtml)
	}))
	err = http.ListenAndServe(
		"127.0.0.1:54321",
		h2c.NewHandler(mux, &http2.Server{}),
	)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
