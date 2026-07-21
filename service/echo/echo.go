// Package echo provides a standard grpc-go EchoService implementation that
// exposes one method of each streaming kind (unary, server-streaming,
// client-streaming, bidi) plus server reflection. It exists to drive the
// grpcview app's streaming invokes end-to-end against a real server.
package echo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	echov1 "codeberg.org/ramilmsh/grpcview/proto/echo/v1"
)

// streamDelay is the pause between streamed sends so streaming is visibly
// observable in a browser client.
const streamDelay = 120 * time.Millisecond

// Server implements echov1.EchoServiceServer with simple, observable echo
// semantics. The embedded UnimplementedEchoServiceServer (by value, per the
// generated code's guidance) keeps it forward-compatible if new methods are
// added to the service.
type Server struct {
	echov1.UnimplementedEchoServiceServer
}

// Compile-time assertion that *Server satisfies the generated server interface.
var _ echov1.EchoServiceServer = (*Server)(nil)

// NewServer returns a ready-to-register EchoService implementation.
func NewServer() echov1.EchoServiceServer {
	return &Server{}
}

// Register registers the echo service and gRPC server reflection on s, so a
// caller can stand up a fully-reflectable echo server with a single call.
func Register(s *grpc.Server) {
	echov1.RegisterEchoServiceServer(s, NewServer())
	reflection.Register(s)
}

// Unary echoes the request in a single response.
func (*Server) Unary(_ context.Context, req *echov1.EchoRequest) (*echov1.EchoResponse, error) {
	return &echov1.EchoResponse{
		Message: "echo: " + req.GetMessage(),
		Index:   0,
	}, nil
}

// ServerStream emits N responses (N = req.Count when > 0, else 3), one every
// streamDelay, so the stream is observable. It respects ctx cancellation.
func (*Server) ServerStream(req *echov1.EchoRequest, stream grpc.ServerStreamingServer[echov1.EchoResponse]) error {
	ctx := stream.Context()

	n := req.GetCount()
	if n <= 0 {
		n = 3
	}

	for i := int32(0); i < n; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if i > 0 {
			if err := sleep(ctx, streamDelay); err != nil {
				return err
			}
		}
		if err := stream.Send(&echov1.EchoResponse{
			Message: fmt.Sprintf("echo #%d: %s", i, req.GetMessage()),
			Index:   i,
		}); err != nil {
			return err
		}
	}

	return nil
}

// ClientStream drains every request until EOF, then replies once with a
// summary of what it received.
func (*Server) ClientStream(stream grpc.ClientStreamingServer[echov1.EchoRequest, echov1.EchoResponse]) error {
	var msgs []string
	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		msgs = append(msgs, req.GetMessage())
	}

	k := len(msgs)
	return stream.SendAndClose(&echov1.EchoResponse{
		Message: fmt.Sprintf("received %d messages: %s", k, strings.Join(msgs, ", ")),
		Index:   int32(k),
	})
}

// BidiStream echoes each received request back with an incrementing index,
// pausing streamDelay between sends. It respects ctx cancellation.
func (*Server) BidiStream(stream grpc.BidiStreamingServer[echov1.EchoRequest, echov1.EchoResponse]) error {
	ctx := stream.Context()

	for i := int32(0); ; i++ {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if i > 0 {
			if err := sleep(ctx, streamDelay); err != nil {
				return err
			}
		}
		if err := stream.Send(&echov1.EchoResponse{
			Message: "echo: " + req.GetMessage(),
			Index:   i,
		}); err != nil {
			return err
		}
	}
}

// sleep waits for d, returning early with ctx.Err() if ctx is cancelled first.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
