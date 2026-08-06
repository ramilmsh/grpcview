package main

import (
	"flag"
	"fmt"
	"log"
	"net"

	"google.golang.org/grpc"

	"codeberg.org/ramilmsh/grpcview/service/echo"
)

func main() {
	var port int
	flag.IntVar(&port, "port", 50055, "TCP port to listen on (must not be 10000, which the main app uses)")
	flag.Parse()

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("echo: failed to listen on %s: %v", addr, err)
	}

	srv := grpc.NewServer()
	echo.Register(srv)

	log.Printf("echo server listening on %s (service echo.v1.EchoService, reflection enabled)", lis.Addr().String())
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("echo: server stopped: %v", err)
	}
}
