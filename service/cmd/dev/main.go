package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"

	"codeberg.org/ramilmsh/grpcview/service"
)

// The dev binary stays serve-only — no cobra, no verbs. It used to inherit
// -port from service.Run's own flag.Parse; now that argv belongs to the callers,
// it parses its own so the Vite workflow keeps working.
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
