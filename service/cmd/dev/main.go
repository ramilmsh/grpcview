package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"codeberg.org/ramilmsh/grpcview/service"
)

func run(ctx context.Context) error {
	var port int
	workspace := os.Getenv("BUILD_WORKSPACE_DIRECTORY")
	flag.IntVar(&port, "port", 10000, "port to start the server at")
	flag.StringVar(&workspace, "workspace", workspace, "workspace root; empty walks up from the current directory to the nearest .git")
	flag.Parse()

	b := bytes.Buffer{}
	b.WriteString("<h1>dummy</h1>")

	devOrigins := []string{"http://localhost:5173", "http://127.0.0.1:5173"}

	if err := service.Run(ctx, io.NopCloser(&b), service.Options{Port: port, Root: workspace, DevOrigins: devOrigins}); err != nil {
		return fmt.Errorf("failed to run server: %w", err)
	}

	return nil
}

func main() {
	if err := run(context.Background()); err != nil {
		panic(err)
	}
}
