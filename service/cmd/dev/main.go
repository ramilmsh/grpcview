package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"

	"codeberg.org/ramilmsh/grpcview/service"
)

func run(ctx context.Context) error {
	var port int
	flag.IntVar(&port, "port", 10000, "port to start the server at")
	flag.Parse()

	b := bytes.Buffer{}
	b.WriteString("<h1>dummy</h1>")

	// The vite dev server is the only cross-origin caller grpcview has; the release
	// binary serves its UI same-origin and installs no CORS handler.
	devOrigins := []string{"http://localhost:5173", "http://127.0.0.1:5173"}

	if err := service.Run(ctx, io.NopCloser(&b), service.Options{Port: port, DevOrigins: devOrigins}); err != nil {
		return fmt.Errorf("failed to run server: %w", err)
	}

	return nil
}

func main() {
	if err := run(context.Background()); err != nil {
		panic(err)
	}
}
