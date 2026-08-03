package main

import (
	"context"
	"embed"
	"fmt"
	"os"

	"codeberg.org/ramilmsh/grpcview/service"
	"codeberg.org/ramilmsh/grpcview/service/cli"
)

//go:embed index.html
var ui embed.FS

// serve owns the UI embed, which is why //service/cli must not import //service.
func serve(ctx context.Context, opts cli.ServeOptions) error {
	indexPageFile, err := ui.Open("index.html")
	if err != nil {
		return fmt.Errorf("failed to open index page: %w", err)
	}
	defer indexPageFile.Close()

	if err := service.Run(ctx, indexPageFile, service.Options{Port: opts.Port}); err != nil {
		return fmt.Errorf("failed to run server: %w", err)
	}

	return nil
}

func main() {
	streams := cli.Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}
	os.Exit(cli.Main(context.Background(), os.Args[1:], streams, serve))
}
