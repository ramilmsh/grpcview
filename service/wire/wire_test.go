package wire

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/durationpb"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

type stub struct {
	grpcviewv1.UnimplementedWorkspaceServiceHandler
	grpcviewv1.UnimplementedServerServiceHandler
	root    string
	gets    int
	getErr  error
	frames  int
	streams int

	idle  time.Duration
	beats chan struct{}
}

func (s *stub) ServerInfo(
	_ context.Context,
	_ *connect.Request[grpcviewv1.ServerInfoRequest],
) (*connect.Response[grpcviewv1.ServerInfoResponse], error) {
	if s.beats != nil {
		select {
		case s.beats <- struct{}{}:
		default:
		}
	}
	return connect.NewResponse(&grpcviewv1.ServerInfoResponse{
		WorkspaceRoot: s.root,
		IdleTimeout:   durationpb.New(s.idle),
	}), nil
}

func (s *stub) Get(
	_ context.Context,
	_ *connect.Request[grpcviewv1.GetRequest],
) (*connect.Response[grpcviewv1.GetResponse], error) {
	s.gets++
	if s.getErr != nil {
		return nil, s.getErr
	}
	return connect.NewResponse(&grpcviewv1.GetResponse{
		Collection: &grpcviewv1.Collection{Id: s.root},
	}), nil
}

func (s *stub) InvokeStreaming(
	_ context.Context,
	_ *connect.Request[grpcviewv1.InvokeStreamRequest],
	stream *connect.ServerStream[grpcviewv1.InvokeStreamingResponse],
) error {
	s.streams++
	for i := 0; i < s.frames; i++ {
		if err := stream.Send(&grpcviewv1.InvokeStreamingResponse{}); err != nil {
			return err
		}
	}
	return nil
}

func serve(t *testing.T, s *stub) string {
	t.Helper()
	return serveHandler(t, s, nil)
}

// A server that dies with the request in flight: the socket closes before a response, which is
// neither a dial failure nor anything the server answered. kill counts how many requests die
// that way before the rest are handled normally.
type flaky struct {
	inner http.Handler
	kill  int
	seen  int
}

func (f *flaky) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.seen++
	if f.seen <= f.kill {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			conn.Close()
		}
		return
	}
	f.inner.ServeHTTP(w, r)
}

func serveHandler(t *testing.T, s *stub, wrap func(http.Handler) http.Handler) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(grpcviewv1.NewWorkspaceServiceHandler(s))
	mux.Handle(grpcviewv1.NewServerServiceHandler(s))
	var handler http.Handler = mux
	if wrap != nil {
		handler = wrap(mux)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv.URL
}

// A URL nothing listens on. Bound and closed, so the port is very unlikely to be reused.
func deadURL(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	url := "http://" + listener.Addr().String()
	listener.Close()
	return url
}

func TestReconnecting_redialsOnADialFailure(t *testing.T) {
	backend := &stub{root: "live"}
	live := serve(t, backend)

	redials := 0
	client := Reconnecting(deadURL(t), func(context.Context) (string, error) {
		redials++
		return live, nil
	})

	res, err := client.Get(context.Background(), connect.NewRequest(&grpcviewv1.GetRequest{}))
	if err != nil {
		t.Fatalf("Get = %v, want it to reconnect and succeed", err)
	}
	if got := res.Msg.GetCollection().GetId(); got != "live" {
		t.Errorf("collection id = %q, want %q", got, "live")
	}
	if redials != 1 {
		t.Errorf("redials = %d, want 1", redials)
	}
	if backend.gets != 1 {
		t.Errorf("the backend saw %d calls, want 1 — the first attempt never reached a server", backend.gets)
	}

	// The new URL sticks: the next call must not pay for the dead one again.
	if _, err := client.Get(context.Background(), connect.NewRequest(&grpcviewv1.GetRequest{})); err != nil {
		t.Fatal(err)
	}
	if redials != 1 {
		t.Errorf("redials = %d after a second call, want 1", redials)
	}
}

// The narrow part of the retry, and the one that matters for writes: an error the server
// ANSWERED means the request arrived, so replaying it could duplicate a mutation.
func TestReconnecting_doesNotRetryAServerError(t *testing.T) {
	backend := &stub{getErr: connect.NewError(connect.CodeNotFound, errors.New("nope"))}
	live := serve(t, backend)

	redials := 0
	client := Reconnecting(live, func(context.Context) (string, error) {
		redials++
		return live, nil
	})

	_, err := client.Get(context.Background(), connect.NewRequest(&grpcviewv1.GetRequest{}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("Get = %v, want NotFound", err)
	}
	if redials != 0 {
		t.Errorf("redials = %d, want 0", redials)
	}
	if backend.gets != 1 {
		t.Errorf("the backend saw %d calls, want exactly 1", backend.gets)
	}
}

func TestReconnecting_surfacesAFailedRedial(t *testing.T) {
	client := Reconnecting(deadURL(t), func(context.Context) (string, error) {
		return "", errors.New("no server could be started")
	})

	_, err := client.Get(context.Background(), connect.NewRequest(&grpcviewv1.GetRequest{}))
	if err == nil {
		t.Fatal("Get succeeded with no server")
	}
	// Both halves: what failed first, and why recovery failed too.
	if got := err.Error(); !strings.Contains(got, "no server could be started") {
		t.Errorf("error = %q, want it to carry the redial failure", got)
	}
}

func TestReconnecting_streamsReconnect(t *testing.T) {
	backend := &stub{frames: 3}
	live := serve(t, backend)

	client := Reconnecting(deadURL(t), func(context.Context) (string, error) { return live, nil })

	got := 0
	err := client.InvokeStream(context.Background(), &grpcviewv1.InvokeStreamRequest{},
		func(*grpcviewv1.InvokeStreamingResponse) error { got++; return nil })
	if err != nil {
		t.Fatalf("InvokeStream = %v", err)
	}
	if got != 3 {
		t.Errorf("delivered %d frames, want 3", got)
	}
	if backend.streams != 1 {
		t.Errorf("the backend saw %d streams, want 1", backend.streams)
	}
}

// A connection that dies in flight proves nothing about whether the request was applied, so a
// read runs again and a write does not. Both repair, because the next call must not find the
// same dead server.
func TestReconnecting_replaysAReadOverABrokenConnection(t *testing.T) {
	backend := &stub{root: "live"}
	var kill *flaky
	url := serveHandler(t, backend, func(inner http.Handler) http.Handler {
		kill = &flaky{inner: inner, kill: 1}
		return kill
	})

	redials := 0
	client := Reconnecting(url, func(context.Context) (string, error) {
		redials++
		return url, nil
	})

	res, err := client.Get(context.Background(), connect.NewRequest(&grpcviewv1.GetRequest{}))
	if err != nil {
		t.Fatalf("Get = %v, want it to survive one broken connection", err)
	}
	if got := res.Msg.GetCollection().GetId(); got != "live" {
		t.Errorf("collection id = %q, want %q", got, "live")
	}
	if redials != 1 {
		t.Errorf("redials = %d, want 1", redials)
	}
	if kill.seen != 2 {
		t.Errorf("the server saw %d requests, want 2 — one killed, one served", kill.seen)
	}
}

func TestReconnecting_repairsButDoesNotReplayAWrite(t *testing.T) {
	backend := &stub{}
	var kill *flaky
	url := serveHandler(t, backend, func(inner http.Handler) http.Handler {
		kill = &flaky{inner: inner, kill: 1}
		return kill
	})

	redials := 0
	client := Reconnecting(url, func(context.Context) (string, error) {
		redials++
		return url, nil
	})

	_, err := client.CreateRequest(context.Background(), connect.NewRequest(&grpcviewv1.CreateRequestRequest{}))
	if err == nil {
		t.Fatal("CreateRequest succeeded over a connection that died in flight")
	}
	// The repair still happens: this is the half that matters for the NEXT call.
	if redials != 1 {
		t.Errorf("redials = %d, want 1 — the connection is repaired either way", redials)
	}
	if kill.seen != 1 {
		t.Errorf("the server saw %d requests, want exactly 1 — a write is never replayed", kill.seen)
	}
}

// A caller who ran out of time is not a server that went away. Redialing here would spawn a
// daemon on every `--timeout` expiry.
func TestReconnecting_doesNotRedialOnAClientTimeout(t *testing.T) {
	backend := &stub{}
	url := serveHandler(t, backend, func(inner http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		})
	})

	redials := 0
	client := Reconnecting(url, func(context.Context) (string, error) {
		redials++
		return url, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := client.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{})); err == nil {
		t.Fatal("Get succeeded against a server that never answers")
	}
	if redials != 0 {
		t.Errorf("redials = %d, want 0", redials)
	}
}

func TestIsDialFailure(t *testing.T) {
	client := Remote(deadURL(t))
	_, err := client.Get(context.Background(), connect.NewRequest(&grpcviewv1.GetRequest{}))
	if err == nil {
		t.Fatal("a call to a dead port succeeded")
	}
	if !isDialFailure(err) {
		t.Errorf("isDialFailure(%v) = false, want true", err)
	}
	if isDialFailure(connect.NewError(connect.CodeUnavailable, errors.New("server says so"))) {
		t.Error("a server-sent Unavailable was taken for a dial failure")
	}
}
