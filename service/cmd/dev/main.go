package main

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/ramilmsh/grpcview/service"
)

func run(ctx context.Context) error {
	b := bytes.Buffer{}
	b.WriteString("<h1>dummy</h1>")

	if err := service.Run(ctx, io.NopCloser(&b)); err != nil {
		return fmt.Errorf("failed to run server: %w", err)
	}

	return nil
}

func main() {
	if err := run(context.Background()); err != nil {
		panic(err)
	}
}
