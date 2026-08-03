package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/grpc"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/echo"
)

const echoService = "echo.v1.EchoService"

func startEchoServer(t *testing.T) int {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	echo.Register(srv)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return lis.Addr().(*net.TCPAddr).Port
}

// echoTarget starts an echo server AND registers it as w's descriptor source. Invoke resolves the
// method descriptor from the workspace's definitions, never by reflecting on the target, so a bare
// server with nothing pointing at it is not enough to invoke against.
func echoTarget(t *testing.T, w Workspace, ctx context.Context, start func(*testing.T) int) int {
	t.Helper()
	port := start(t)
	if _, err := w.AddDescriptorSource(ctx, connect.NewRequest(reflectionAddReq(port))); err != nil {
		t.Fatalf("AddDescriptorSource (echo reflection on :%d): %v", port, err)
	}
	return port
}

func echoStreamReq(port int, method string, messages ...string) *grpcviewv1.InvokeStreamRequest {
	wrapped := make([]string, len(messages))
	for i, m := range messages {
		wrapped[i] = tsBody(m)
	}
	return &grpcviewv1.InvokeStreamRequest{
		Spec: &grpcviewv1.InvokeSpec{
			WorkspaceName: testWorkspace,
			Service:       echoService,
			Method:        method,
			Target:        &grpcviewv1.Server{Address: fmt.Sprintf("127.0.0.1:%d", port)},
		},
		Messages: wrapped,
	}
}

func collectStream(ctx context.Context, w Workspace, msg *grpcviewv1.InvokeStreamRequest) ([]*grpcviewv1.InvokeStreamingResponse, error) {
	var frames []*grpcviewv1.InvokeStreamingResponse
	send := func(resp *grpcviewv1.InvokeStreamingResponse) error {
		frames = append(frames, resp)
		return nil
	}
	spec := specFrom(msg.GetSpec())
	spec.recordHistory = true
	return frames, w.streamInvoke(ctx, spec, msg.GetMessages(), send)
}

func splitFrames(t *testing.T, frames []*grpcviewv1.InvokeStreamingResponse) (msgs [][]byte, result *grpcviewv1.Request_Response) {
	t.Helper()
	for i, f := range frames {
		switch ev := f.GetEvent().(type) {
		case *grpcviewv1.InvokeStreamingResponse_Message:
			if result != nil {
				t.Fatalf("message frame at index %d appears after the terminal result frame", i)
			}
			msgs = append(msgs, ev.Message)
		case *grpcviewv1.InvokeStreamingResponse_Result:
			if result != nil {
				t.Fatalf("more than one terminal result frame")
			}
			if i != len(frames)-1 {
				t.Fatalf("terminal result frame at index %d is not the last of %d frames", i, len(frames))
			}
			result = ev.Result
		default:
			t.Fatalf("unexpected frame type %T at index %d", ev, i)
		}
	}
	if result == nil {
		t.Fatalf("no terminal result frame emitted")
	}
	return msgs, result
}

func TestStreamInvokeKinds(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	port := echoTarget(t, w, context.Background(), startEchoServer)

	cases := []struct {
		name     string
		method   string
		messages []string
		wantMsgs int
	}{
		{"unary", "Unary", []string{`{"message":"hi"}`}, 1},
		{"server_stream_count3", "ServerStream", []string{`{"message":"hi","count":3}`}, 3},
		{"server_stream_count5", "ServerStream", []string{`{"message":"hi","count":5}`}, 5},
		{"client_stream", "ClientStream", []string{`{"message":"a"}`, `{"message":"b"}`}, 1},
		{"bidi_stream", "BidiStream", []string{`{"message":"a"}`, `{"message":"b"}`, `{"message":"c"}`}, 3},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			frames, err := collectStream(context.Background(), w, echoStreamReq(port, tc.method, tc.messages...))
			if err != nil {
				t.Fatalf("streamInvoke: %v", err)
			}

			msgs, result := splitFrames(t, frames)
			if len(msgs) != tc.wantMsgs {
				t.Fatalf("want %d message frames, got %d", tc.wantMsgs, len(msgs))
			}
			if got := result.GetStatus().GetCode(); got != int32(codeOK) {
				t.Fatalf("terminal status: want OK (0), got %d (%q)", got, result.GetStatus().GetMessage())
			}
			if len(result.GetResponse()) != 0 {
				t.Fatalf("terminal frame Response should be empty, got %d bytes", len(result.GetResponse()))
			}

			for i, m := range msgs {
				if len(m) == 0 {
					t.Fatalf("message frame %d is empty", i)
				}
				var payload map[string]any
				if err := json.Unmarshal(m, &payload); err != nil {
					t.Fatalf("message frame %d is not valid JSON: %v (%s)", i, err, m)
				}
				if _, ok := payload["message"]; !ok {
					t.Fatalf("message frame %d missing echoed \"message\" field: %s", i, m)
				}
			}
		})
	}
}

func TestStreamInvokeDefaultsEmptyMessages(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	port := echoTarget(t, w, context.Background(), startEchoServer)

	frames, err := collectStream(context.Background(), w, echoStreamReq(port, "Unary"))
	if err != nil {
		t.Fatalf("streamInvoke: %v", err)
	}
	msgs, result := splitFrames(t, frames)
	if len(msgs) != 1 {
		t.Fatalf("want 1 message frame for defaulted empty request, got %d", len(msgs))
	}
	if got := result.GetStatus().GetCode(); got != int32(codeOK) {
		t.Fatalf("terminal status: want OK (0), got %d (%q)", got, result.GetStatus().GetMessage())
	}
}

func TestStreamInvokePreflightErrors(t *testing.T) {
	w := newTestWorkspace(t)
	port := echoTarget(t, w, context.Background(), startEchoServer)

	t.Run("unknown_method", func(t *testing.T) {
		frames, err := collectStream(context.Background(), w, echoStreamReq(port, "NoSuchMethod", `{}`))
		if err == nil {
			t.Fatalf("want an error for an unknown method, got nil")
		}
		if got := connect.CodeOf(err); got != connect.CodeNotFound {
			t.Fatalf("want NotFound, got %v (%v)", got, err)
		}
		if len(frames) != 0 {
			t.Fatalf("want no frames on pre-flight failure, got %d", len(frames))
		}
	})

	t.Run("unreachable_target", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		msg := echoStreamReq(port, "Unary", `{}`)
		msg.Spec.Target = &grpcviewv1.Server{Address: "127.0.0.1:1"}

		frames, err := collectStream(ctx, w, msg)
		if err == nil {
			t.Fatalf("want an error for an unreachable target, got nil")
		}
		if connect.CodeOf(err) == connect.CodeUnknown {
			t.Fatalf("want a typed Connect error, got %v", err)
		}
		if len(frames) != 0 {
			t.Fatalf("want no frames on pre-flight failure, got %d", len(frames))
		}
	})
}
