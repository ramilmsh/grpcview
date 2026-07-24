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

// startEchoServer stands up an in-process echo gRPC server (EchoService + server
// reflection) on a loopback port and returns the port. streamInvoke reflects the
// target to resolve the method, so reflection must be registered; echo.Register
// wires both the service and reflection in one call.
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

// echoStreamReq builds a streaming invoke request aimed at the loopback echo
// server. Target is set explicitly so no workspace store setup is needed.
func echoStreamReq(port int, method string, messages ...string) *grpcviewv1.InvokeStreamRequest {
	// Every message is wrapped as a canonical TS module: the invoke path evaluates each body as
	// TypeScript now (like the frontend's migrated bodies), so a raw JSON literal would misparse.
	wrapped := make([]string, len(messages))
	for i, m := range messages {
		wrapped[i] = tsBody(m)
	}
	return &grpcviewv1.InvokeStreamRequest{
		WorkspaceName: testWorkspace,
		Service:       echoService,
		Method:        method,
		Messages:      wrapped,
		Target:        &grpcviewv1.Server{Address: fmt.Sprintf("127.0.0.1:%d", port)},
	}
}

// collectStream runs streamInvoke against msg and returns every frame it emitted
// (message frames then the terminal result frame) plus any handler error, using
// an in-memory send func in place of a real connect.ServerStream.
func collectStream(ctx context.Context, w Workspace, msg *grpcviewv1.InvokeStreamRequest) ([]*grpcviewv1.InvokeStreamResponse, error) {
	var frames []*grpcviewv1.InvokeStreamResponse
	send := func(resp *grpcviewv1.InvokeStreamResponse) error {
		frames = append(frames, resp)
		return nil
	}
	return frames, w.streamInvoke(ctx, msg, send)
}

// splitFrames separates collected frames into the message payloads and the single
// terminal result, asserting the frame protocol: message frames come first and
// exactly one result frame closes the stream, last.
func splitFrames(t *testing.T, frames []*grpcviewv1.InvokeStreamResponse) (msgs [][]byte, result *grpcviewv1.Request_Response) {
	t.Helper()
	for i, f := range frames {
		switch ev := f.GetEvent().(type) {
		case *grpcviewv1.InvokeStreamResponse_Message:
			if result != nil {
				t.Fatalf("message frame at index %d appears after the terminal result frame", i)
			}
			msgs = append(msgs, ev.Message)
		case *grpcviewv1.InvokeStreamResponse_Result:
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

// TestStreamInvokeKinds drives one method of every streaming kind against a real
// echo server and asserts the frame count matches the kind, the terminal frame is
// last and reports OK, and each message frame is non-empty JSON carrying the
// echoed message. The two ServerStream rows (count 3 and 5) prove the emitted
// frame count tracks the number of responses the server actually streams.
func TestStreamInvokeKinds(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	port := startEchoServer(t)

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
			// Streamed payloads travel as message frames; the terminal frame's
			// Response bytes stay empty.
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

// TestStreamInvokeDefaultsEmptyMessages checks that an empty Messages list is
// treated as a single "{}" request, so a unary target still produces one message
// frame and an OK terminal frame.
func TestStreamInvokeDefaultsEmptyMessages(t *testing.T) {
	w := newTestWorkspaceWithEngine(t)
	port := startEchoServer(t)

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

// TestStreamInvokePreflightErrors asserts that failures grpcview can't get past
// (an unknown method, an unreachable target) surface as Connect errors with no
// frames emitted — the pre-flight contract.
func TestStreamInvokePreflightErrors(t *testing.T) {
	w := newTestWorkspace(t)
	port := startEchoServer(t)

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
		// Point at a port nobody is listening on; reflection can't resolve the
		// schema, so the call fails pre-flight before any frame is sent. Bound by a
		// timeout so a stuck dial can't hang the test.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		msg := echoStreamReq(port, "Unary", `{}`)
		msg.Target = &grpcviewv1.Server{Address: "127.0.0.1:1"}

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
