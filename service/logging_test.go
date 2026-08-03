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

type fakeStreamConn struct{}

func (fakeStreamConn) Spec() connect.Spec           { return connect.Spec{Procedure: "/test.v1.Svc/Proc"} }
func (fakeStreamConn) Peer() connect.Peer           { return connect.Peer{Protocol: "connect", Addr: "test"} }
func (fakeStreamConn) Receive(any) error            { return nil }
func (fakeStreamConn) RequestHeader() http.Header   { return http.Header{} }
func (fakeStreamConn) Send(any) error               { return nil }
func (fakeStreamConn) ResponseHeader() http.Header  { return http.Header{} }
func (fakeStreamConn) ResponseTrailer() http.Header { return http.Header{} }

// captureHandler records every attr by key so a duplicated one is visible as such.
type captureHandler struct{ attrs map[string][]slog.Value }

func (*captureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	r.Attrs(func(a slog.Attr) bool {
		h.attrs[a.Key] = append(h.attrs[a.Key], a.Value)
		return true
	})
	return nil
}
func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func TestUnaryLogEmitsTheRealStatusOnce(t *testing.T) {
	h := &captureHandler{attrs: map[string][]slog.Value{}}
	l := loggingInterceptor{logger: slog.New(h)}

	sentinel := connect.NewError(connect.CodePermissionDenied, errors.New("nope"))
	wrapped := l.WrapUnary(func(context.Context, connect.AnyRequest) (connect.AnyResponse, error) {
		return nil, sentinel
	})
	if _, err := wrapped(context.Background(), connect.NewRequest(&struct{}{})); !errors.Is(err, sentinel) {
		t.Fatalf("want the sentinel error propagated, got %v", err)
	}

	got := h.attrs["status"]
	if len(got) != 1 {
		t.Fatalf("status attrs = %d %v, want exactly 1: a duplicate key lets the later value "+
			"win, masking the typed error's real code", len(got), got)
	}
	if want := connect.CodePermissionDenied.String(); got[0].String() != want {
		t.Fatalf("status = %q, want %q", got[0].String(), want)
	}
}

func TestLoggingInterceptorWrapsStreaming(t *testing.T) {
	l := loggingInterceptor{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

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

	sentinel := connect.NewError(connect.CodeInternal, errors.New("boom"))
	wrappedErr := l.WrapStreamingHandler(func(context.Context, connect.StreamingHandlerConn) error {
		return sentinel
	})
	if err := wrappedErr(context.Background(), fakeStreamConn{}); !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel error propagated, got %v", err)
	}

	var scf connect.StreamingClientFunc = func(context.Context, connect.Spec) connect.StreamingClientConn {
		return nil
	}
	if l.WrapStreamingClient(scf) == nil {
		t.Fatal("WrapStreamingClient returned nil for a non-nil handler")
	}
}
