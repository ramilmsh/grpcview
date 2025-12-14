package main

import (
	"context"
	"embed"
	"fmt"

	"codeberg.org/ramilmsh/grpcview/service"
)

//go:embed index.html
var ui embed.FS

func run(ctx context.Context) error {
	indexPageFile, err := ui.Open("index.html")
	if err != nil {
		return fmt.Errorf("failed to open index page: %w", err)
	}

	if err := service.Run(ctx, indexPageFile); err != nil {
		return fmt.Errorf("failed to run server: %w", err)
	}

	return nil
}

func main() {
	if err := run(context.Background()); err != nil {
		panic(err)
	}
}
