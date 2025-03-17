package main

import (
	"context"

	"github.com/ramilmsh/grpcview/service"
)

func main() {
	if err := service.Run(context.Background()); err != nil {
		panic(err)
	}
}
