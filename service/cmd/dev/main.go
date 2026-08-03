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

	if err := service.Run(ctx, io.NopCloser(&b), service.Options{Port: port}); err != nil {
		return fmt.Errorf("failed to run server: %w", err)
	}

	return nil
}

func main() {
	if err := run(context.Background()); err != nil {
		panic(err)
	}
}
