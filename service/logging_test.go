package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"connectrpc.com/connect"
)

// fakeStreamConn is a no-op connect.StreamingHandlerConn for exercising the
// logging interceptor's streaming wrapper without a real transport. The wrapper
// only reads Peer()/Spec(); the rest satisfy the interface.
type fakeStreamConn struct{}

func (fakeStreamConn) Spec() connect.Spec            { return connect.Spec{Procedure: "/test.v1.Svc/Proc"} }
func (fakeStreamConn) Peer() connect.Peer            { return connect.Peer{Protocol: "connect", Addr: "test"} }
func (fakeStreamConn) Receive(any) error             { return nil }
func (fakeStreamConn) RequestHeader() http.Header    { return http.Header{} }
func (fakeStreamConn) Send(any) error                { return nil }
func (fakeStreamConn) ResponseHeader() http.Header   { return http.Header{} }
func (fakeStreamConn) ResponseTrailer() http.Header  { return http.Header{} }

// TestLoggingInterceptorWrapsStreaming guards the regression that made the first
// streaming RPC panic: WrapStreamingHandler/WrapStreamingClient returned nil
// (unary-only stubs), so connect wrapped the real handler into nil and then
// called a nil implementation. The wrappers must be non-nil, call through to the
// inner handler, and propagate its result.
func TestLoggingInterceptorWrapsStreaming(t *testing.T) {
	l := loggingInterceptor{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	// Handler wrapper: non-nil, calls through, returns nil on success.
	reached := false
	wrapped := l.WrapStreamingHandler(func(context.Context, connect.StreamingHandlerConn) error {
		reached = true
		return nil
	})
	if wrapped == nil {
		t.Fatal("WrapStreamingHandler returned nil (would make connect call a nil implementation)")
	}
	if err := wrapped(context.Background(), fakeStreamConn{}); err != nil {
		t.Fatalf("wrapped handler returned error: %v", err)
	}
	if !reached {
		t.Fatal("wrapped handler did not call through to the inner handler")
	}

	// Handler wrapper: propagates the inner error unchanged.
	sentinel := connect.NewError(connect.CodeInternal, errors.New("boom"))
	wrappedErr := l.WrapStreamingHandler(func(context.Context, connect.StreamingHandlerConn) error {
		return sentinel
	})
	if err := wrappedErr(context.Background(), fakeStreamConn{}); !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel error propagated, got %v", err)
	}

	// Client wrapper: non-nil for a non-nil handler (pass-through).
	var scf connect.StreamingClientFunc = func(context.Context, connect.Spec) connect.StreamingClientConn {
		return nil
	}
	if l.WrapStreamingClient(scf) == nil {
		t.Fatal("WrapStreamingClient returned nil for a non-nil handler")
	}
}
