// Command echo runs the standalone gRPC EchoService test server. It exposes
// one method of each streaming kind plus server reflection, so the grpcview
// app can drive unary/server/client/bidi invokes against a real server.
//
// Run it with: bazel run //service/echo/cmd -- -port 50055
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
