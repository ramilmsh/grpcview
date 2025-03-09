package main

import (
	"embed"
	"fmt"
	"io"
)

//go:embed index.html
var frontend embed.FS

func main() {
	f, err := frontend.Open("index.html")
	if err != nil {
		panic(err)
	}

	data, err := io.ReadAll(f)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(data))
}
