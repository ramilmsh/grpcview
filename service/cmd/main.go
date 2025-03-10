package main

import (
	"embed"
	"fmt"
	"io"

	"github.com/ramilmsh/grpcview/service"
	grpcviewv1 "github.com/ramilmsh/grpcview/service/proto/v1"
)

//go:embed index.html
var frontend embed.FS

func main() {
	if err := service.Run(nil); err != nil {
		panic(err)
	}
	fmt.Println(grpcviewv1.Hello{})
	f, err := frontend.Open("index.html")
	if err != nil {
		panic(err)
	}

	data, err := io.ReadAll(f)
	if err != nil {
		panic(err)
	}
	fmt.Println(len(data))
}
