package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"testing/fstest"

	"codeberg.org/ramilmsh/grpcview/service"
)

func run(ctx context.Context) error {
	var port int
	var workspace string
	flag.IntVar(&port, "port", 10000, "port to start the server at")
	flag.StringVar(&workspace, "workspace", "", "workspace root; empty takes $BUILD_WORKSPACE_DIRECTORY, else walks up from the current directory to the nearest .git")
	flag.Parse()

	var dummy fs.FS = fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<h1>dummy</h1>")},
	}

	devOrigins := []string{"http://localhost:5173", "http://127.0.0.1:5173"}

	// Register is false and the port is pinned on purpose: the dev server serves a dummy index
	// page and is a different binary from the one the CLI would restart it as, so it stays out
	// of the registration path entirely, and `ui/src/lib/client.ts` hardcodes this port.
	if err := service.Run(ctx, dummy, service.Options{
		Port:       port,
		PortPinned: true,
		Root:       workspace,
		DevOrigins: devOrigins,
	}); err != nil {
		return fmt.Errorf("failed to run server: %w", err)
	}

	return nil
}

func main() {
	if err := run(context.Background()); err != nil {
		panic(err)
	}
}
