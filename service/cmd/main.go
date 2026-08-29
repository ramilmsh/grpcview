package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"

	"codeberg.org/ramilmsh/grpcview/service"
	"codeberg.org/ramilmsh/grpcview/service/cli"
)

//go:embed dist
var distFS embed.FS

func serve(ctx context.Context, opts cli.ServeOptions) error {
	dist, err := fs.Sub(distFS, "dist")
	if err != nil {
		return fmt.Errorf("failed to open built UI: %w", err)
	}

	if err := service.Run(ctx, dist, service.Options{
		Port:        opts.Port,
		PortPinned:  opts.PortPinned,
		Root:        opts.Root,
		IdleTimeout: opts.IdleTimeout,
		OpenBrowser: opts.OpenBrowser,
		Version:     opts.Version,
		Register:    true,
	}); err != nil {
		return fmt.Errorf("failed to run server: %w", err)
	}

	return nil
}

func main() {
	streams := cli.Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}
	os.Exit(cli.Main(context.Background(), os.Args[1:], streams, serve))
}
