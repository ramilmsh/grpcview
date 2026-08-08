// Package wire is the one client surface every non-browser caller of grpcview talks through,
// and the two bindings behind it: a local workspace.Workspace called as a plain Go value, and
// a remote one over Connect. The CLI and the MCP server both take it, so "which process wrote
// my history" is one decision made in one place rather than one per surface.
package wire

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"syscall"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/workspace"
)

// An alias, not a defined type: workspace.Workspace's methods spell the parameter out, and a
// defined type would stop them satisfying this interface.
type SendFunc = func(*grpcviewv1.InvokeStreamingResponse) error

// Workspace is every RPC that operates on a collection. Unary methods need no adapter — the
// handler and the generated client have the same signature, which this interface asserts at
// compile time for both bindings. Only streaming differs: a handler takes a
// *connect.ServerStream and a client returns a *connect.ServerStreamForClient, and connect
// cannot build the former outside a served request, so the callback form is the contract.
type Workspace interface {
	Get(context.Context, *connect.Request[grpcviewv1.GetRequest]) (*connect.Response[grpcviewv1.GetResponse], error)
	ListCollections(context.Context, *connect.Request[grpcviewv1.ListCollectionsRequest]) (*connect.Response[grpcviewv1.ListCollectionsResponse], error)
	CreateCollection(context.Context, *connect.Request[grpcviewv1.CreateCollectionRequest]) (*connect.Response[grpcviewv1.CreateCollectionResponse], error)
	UpdateCollection(context.Context, *connect.Request[grpcviewv1.UpdateCollectionRequest]) (*connect.Response[grpcviewv1.UpdateCollectionResponse], error)
	SetWorkspaceTrust(context.Context, *connect.Request[grpcviewv1.SetWorkspaceTrustRequest]) (*connect.Response[grpcviewv1.SetWorkspaceTrustResponse], error)
	ListBazelTargets(context.Context, *connect.Request[grpcviewv1.ListBazelTargetsRequest]) (*connect.Response[grpcviewv1.ListBazelTargetsResponse], error)

	AddDescriptorSource(context.Context, *connect.Request[grpcviewv1.AddDescriptorSourceRequest]) (*connect.Response[grpcviewv1.AddDescriptorSourceResponse], error)
	RemoveDescriptorSource(context.Context, *connect.Request[grpcviewv1.RemoveDescriptorSourceRequest]) (*connect.Response[grpcviewv1.RemoveDescriptorSourceResponse], error)
	RefreshDescriptorSource(context.Context, *connect.Request[grpcviewv1.RefreshDescriptorSourceRequest]) (*connect.Response[grpcviewv1.RefreshDescriptorSourceResponse], error)
	ReorderDescriptorSources(context.Context, *connect.Request[grpcviewv1.ReorderDescriptorSourcesRequest]) (*connect.Response[grpcviewv1.ReorderDescriptorSourcesResponse], error)
	SetDescriptorSourceCommit(context.Context, *connect.Request[grpcviewv1.SetDescriptorSourceCommitRequest]) (*connect.Response[grpcviewv1.SetDescriptorSourceCommitResponse], error)

	CreateFolder(context.Context, *connect.Request[grpcviewv1.CreateFolderRequest]) (*connect.Response[grpcviewv1.CreateFolderResponse], error)
	UpdateFolder(context.Context, *connect.Request[grpcviewv1.UpdateFolderRequest]) (*connect.Response[grpcviewv1.UpdateFolderResponse], error)
	CreateRequest(context.Context, *connect.Request[grpcviewv1.CreateRequestRequest]) (*connect.Response[grpcviewv1.CreateRequestResponse], error)
	UpdateRequest(context.Context, *connect.Request[grpcviewv1.UpdateRequestRequest]) (*connect.Response[grpcviewv1.UpdateRequestResponse], error)
	DeleteRequest(context.Context, *connect.Request[grpcviewv1.DeleteRequestRequest]) (*connect.Response[grpcviewv1.DeleteRequestResponse], error)
	MoveItem(context.Context, *connect.Request[grpcviewv1.MoveItemRequest]) (*connect.Response[grpcviewv1.MoveItemResponse], error)

	Invoke(context.Context, *connect.Request[grpcviewv1.InvokeRequest]) (*connect.Response[grpcviewv1.InvokeResponse], error)
	InvokeSaved(context.Context, *connect.Request[grpcviewv1.InvokeSavedRequest]) (*connect.Response[grpcviewv1.InvokeSavedResponse], error)
	InvokeStream(context.Context, *grpcviewv1.InvokeStreamRequest, SendFunc) error
	InvokeSavedStream(context.Context, *grpcviewv1.InvokeSavedStreamRequest, SendFunc) error

	DescribeMethod(context.Context, *connect.Request[grpcviewv1.DescribeMethodRequest]) (*connect.Response[grpcviewv1.DescribeMethodResponse], error)

	RunScript(context.Context, *connect.Request[grpcviewv1.RunScriptRequest]) (*connect.Response[grpcviewv1.RunScriptResponse], error)
	CreateScript(context.Context, *connect.Request[grpcviewv1.CreateScriptRequest]) (*connect.Response[grpcviewv1.CreateScriptResponse], error)
	UpdateScript(context.Context, *connect.Request[grpcviewv1.UpdateScriptRequest]) (*connect.Response[grpcviewv1.UpdateScriptResponse], error)
	DeleteScript(context.Context, *connect.Request[grpcviewv1.DeleteScriptRequest]) (*connect.Response[grpcviewv1.DeleteScriptResponse], error)
}

// Client adds the server's own lifecycle. In-process there is no process to describe and
// nothing to shut down, so the local binding answers the first and rejects the second.
type Client interface {
	Workspace

	ServerInfo(context.Context, *connect.Request[grpcviewv1.ServerInfoRequest]) (*connect.Response[grpcviewv1.ServerInfoResponse], error)
	Shutdown(context.Context, *connect.Request[grpcviewv1.ShutdownRequest]) (*connect.Response[grpcviewv1.ShutdownResponse], error)
}

var _ Workspace = workspace.Workspace{}

type remote struct {
	grpcviewv1.WorkspaceServiceClient
	lifecycle grpcviewv1.ServerServiceClient
}

// Remote binds to a server at baseURL and never reconnects. Use it for --server, where the
// caller named one specific server and moving to another would be a surprise.
func Remote(baseURL string) Client { return newRemote(baseURL) }

func newRemote(baseURL string) remote {
	return remote{
		WorkspaceServiceClient: grpcviewv1.NewWorkspaceServiceClient(http.DefaultClient, baseURL),
		lifecycle:              grpcviewv1.NewServerServiceClient(http.DefaultClient, baseURL),
	}
}

func (r remote) ServerInfo(ctx context.Context, req *connect.Request[grpcviewv1.ServerInfoRequest]) (*connect.Response[grpcviewv1.ServerInfoResponse], error) {
	return r.lifecycle.ServerInfo(ctx, req)
}

func (r remote) Shutdown(ctx context.Context, req *connect.Request[grpcviewv1.ShutdownRequest]) (*connect.Response[grpcviewv1.ShutdownResponse], error) {
	return r.lifecycle.Shutdown(ctx, req)
}

func (r remote) InvokeStream(ctx context.Context, msg *grpcviewv1.InvokeStreamRequest, send SendFunc) error {
	stream, err := r.WorkspaceServiceClient.InvokeStreaming(ctx, connect.NewRequest(msg))
	if err != nil {
		return err
	}
	return drain(stream, send)
}

func (r remote) InvokeSavedStream(ctx context.Context, msg *grpcviewv1.InvokeSavedStreamRequest, send SendFunc) error {
	stream, err := r.WorkspaceServiceClient.InvokeSavedStreaming(ctx, connect.NewRequest(msg))
	if err != nil {
		return err
	}
	return drain(stream, send)
}

func drain(stream *connect.ServerStreamForClient[grpcviewv1.InvokeStreamingResponse], send SendFunc) error {
	defer stream.Close()
	for stream.Receive() {
		if err := send(stream.Msg()); err != nil {
			return err
		}
	}
	return stream.Err()
}

var _ Client = remote{}

// Redial answers "where is the server now" and is allowed to start one.
type Redial func(context.Context) (string, error)

// Reconnecting is the binding for a server whose lifetime the caller does not control: it
// idles out, and a rebuild restarts it. A long-lived MCP session would otherwise die the
// first time either happened, and a CLI verb would lose the close race at exactly the idle
// deadline.
//
// Repairing the connection and replaying the request are two different decisions, and only
// the first is unconditional. A dial failure proves nothing was ever sent, so anything may be
// replayed. A connection that broke mid-flight proves nothing at all — the write may already
// be on disk — so it is repaired and only reads run again. Anything the server itself
// answered, including its own Unavailable, is returned untouched.
func Reconnecting(baseURL string, redial Redial) Client {
	return &reconnecting{current: newRemote(baseURL), url: baseURL, redial: redial}
}

// The mutex is not decoration: an MCP session serves tool calls concurrently, so two of them
// can find the daemon gone at once. Both racing to redial is harmless — daemon.Connect takes
// the spawn lock, so the loser finds the winner's server rather than starting a second.
type reconnecting struct {
	mu      sync.RWMutex
	current remote
	url     string

	redial Redial
}

// retry runs call and, if the failure was the connection's rather than the server's,
// re-resolves the server before deciding whether to run it again.
//
// replayable is the caller's promise that running the request twice is indistinguishable from
// running it once — true only for reads, which is already enough to stop a stream being
// replayed. sent is the belt to that pair of braces: a stream that has delivered a frame
// reached a server whatever the error says, so it never runs again.
func (c *reconnecting) retry(ctx context.Context, call func(remote) error, sent func() bool, replayable bool) error {
	err := call(c.snapshot())
	if err == nil {
		return nil
	}
	state := classify(ctx, err)
	if state == answered {
		return err
	}

	// Repair regardless of whether this call can be replayed: the point is that the NEXT one
	// does not find the same dead server. A failed redial is reported alongside what it was
	// trying to recover from, since either half alone reads as the wrong problem.
	url, rerr := c.redial(ctx)
	if rerr != nil {
		return errors.Join(err, rerr)
	}
	repaired := c.replace(url)

	if state == broken && !replayable {
		return err
	}
	if sent != nil && sent() {
		return err
	}
	return call(repaired)
}

// What happened to a failed request, and it is the difference between the second and third
// that keeps a retry from duplicating a write.
type outcome int

const (
	answered outcome = iota // a server produced this; the request arrived and was handled
	unsent                  // the dial failed: nothing was ever written to a socket
	broken                  // the connection died in flight: it may or may not have been applied
)

func classify(ctx context.Context, err error) outcome {
	// A caller who gave up is not a server that went away. Without this, every `--timeout`
	// expiry would redial, and a redial is allowed to start a process.
	if ctx.Err() != nil ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, os.ErrDeadlineExceeded) {
		return answered
	}
	switch {
	case isDialFailure(err):
		return unsent
	case isConnectionBroken(err):
		return broken
	default:
		return answered
	}
}

// A dial that never connected surfaces as a *net.OpError with Op "dial", wrapped by
// net/http and then by connect. ECONNREFUSED is checked too, in case a future transport
// reports it without the OpError.
func isDialFailure(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return true
	}
	return errors.Is(err, syscall.ECONNREFUSED)
}

// A server killed mid-request, as opposed to one that was already gone when the client dialed.
// The transport is HTTP/1.1 (http.DefaultClient against a plaintext base URL), so this is a
// truncated response or a reset socket rather than a GOAWAY, and connect wraps it without
// hiding it from errors.Is.
func isConnectionBroken(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	var opErr *net.OpError
	return errors.As(err, &opErr)
}

func (c *reconnecting) snapshot() remote {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.current
}

func (c *reconnecting) replace(url string) remote {
	c.mu.Lock()
	defer c.mu.Unlock()
	if url != c.url {
		c.url, c.current = url, newRemote(url)
	}
	return c.current
}

// URL is where this client is currently pointed, which changes when it reconnects.
func (c *reconnecting) URL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.url
}

func (c *reconnecting) InvokeStream(ctx context.Context, msg *grpcviewv1.InvokeStreamRequest, send SendFunc) error {
	var delivered bool
	wrapped := func(frame *grpcviewv1.InvokeStreamingResponse) error {
		delivered = true
		return send(frame)
	}
	return c.retry(ctx,
		func(r remote) error { return r.InvokeStream(ctx, msg, wrapped) },
		func() bool { return delivered }, false)
}

func (c *reconnecting) InvokeSavedStream(ctx context.Context, msg *grpcviewv1.InvokeSavedStreamRequest, send SendFunc) error {
	var delivered bool
	wrapped := func(frame *grpcviewv1.InvokeStreamingResponse) error {
		delivered = true
		return send(frame)
	}
	return c.retry(ctx,
		func(r remote) error { return r.InvokeSavedStream(ctx, msg, wrapped) },
		func() bool { return delivered }, false)
}

var _ Client = (*reconnecting)(nil)
