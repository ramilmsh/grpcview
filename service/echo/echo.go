// Package echo is an EchoService with one method of each streaming kind, to invoke against.
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

const streamDelay = 120 * time.Millisecond

type Server struct {
	echov1.UnimplementedEchoServiceServer
}

var _ echov1.EchoServiceServer = (*Server)(nil)

func NewServer() echov1.EchoServiceServer {
	return &Server{}
}

func Register(s *grpc.Server) {
	echov1.RegisterEchoServiceServer(s, NewServer())
	reflection.Register(s)
}

func (*Server) Unary(_ context.Context, req *echov1.EchoRequest) (*echov1.EchoResponse, error) {
	return &echov1.EchoResponse{
		Message: "echo: " + req.GetMessage(),
		Index:   0,
	}, nil
}

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
