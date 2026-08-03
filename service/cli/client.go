package cli

import (
	"context"
	"fmt"
	"net/http"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/workspace"
)

// Client is the slice of WorkspaceService the CLI verbs use. Unary methods are
// spelled with the Connect handler signature, which is also the generated
// Connect *client* signature — so the in-process handler and the wire client
// satisfy those with no adapter at all.
//
// The streaming methods are the exception, and the reason both bindings are
// wrapped below: a handler receives a *connect.ServerStream and a client
// returns a *connect.ServerStreamForClient, and connect exposes no way to build
// the former outside a served request. Neither shape works for both, so Client
// declares the one shape a CLI actually wants — frames delivered to a callback —
// and each binding adapts its own side onto it.
//
// Methods are added as verbs need them.
type Client interface {
	Get(context.Context, *connect.Request[grpcviewv1.GetRequest]) (*connect.Response[grpcviewv1.GetResponse], error)
	Invoke(context.Context, *connect.Request[grpcviewv1.InvokeRequest]) (*connect.Response[grpcviewv1.InvokeResponse], error)
	InvokeSaved(context.Context, *connect.Request[grpcviewv1.InvokeSavedRequest]) (*connect.Response[grpcviewv1.InvokeSavedResponse], error)

	// InvokeStream runs an ad-hoc streaming invoke, handing every frame to send
	// in arrival order. It returns when the terminal frame has been delivered,
	// or early with the first error send or the transport reports.
	InvokeStream(ctx context.Context, msg *grpcviewv1.InvokeStreamRequest, send func(*grpcviewv1.InvokeStreamResponse) error) error
	// InvokeSavedStream is InvokeStream for a saved request, addressed by path.
	InvokeSavedStream(ctx context.Context, msg *grpcviewv1.InvokeSavedRequest, send func(*grpcviewv1.InvokeStreamResponse) error) error

	// The mutations the write verbs use. Every one returns the whole Workspace,
	// which the verbs deliberately discard (see write.go's contract), so the
	// response type appears here only because the signature is the generated one.
	AddDescriptorSource(context.Context, *connect.Request[grpcviewv1.AddDescriptorSourceRequest]) (*connect.Response[grpcviewv1.AddDescriptorSourceResponse], error)
	RefreshDescriptorSource(context.Context, *connect.Request[grpcviewv1.RefreshDescriptorSourceRequest]) (*connect.Response[grpcviewv1.RefreshDescriptorSourceResponse], error)
	RemoveDescriptorSource(context.Context, *connect.Request[grpcviewv1.RemoveDescriptorSourceRequest]) (*connect.Response[grpcviewv1.RemoveDescriptorSourceResponse], error)
	ReorderDescriptorSources(context.Context, *connect.Request[grpcviewv1.ReorderDescriptorSourcesRequest]) (*connect.Response[grpcviewv1.ReorderDescriptorSourcesResponse], error)

	CreateFolder(context.Context, *connect.Request[grpcviewv1.CreateFolderRequest]) (*connect.Response[grpcviewv1.CreateFolderResponse], error)
	CreateRequest(context.Context, *connect.Request[grpcviewv1.CreateRequestRequest]) (*connect.Response[grpcviewv1.CreateRequestResponse], error)
	UpdateRequest(context.Context, *connect.Request[grpcviewv1.UpdateRequestRequest]) (*connect.Response[grpcviewv1.UpdateRequestResponse], error)
	DeleteRequest(context.Context, *connect.Request[grpcviewv1.DeleteRequestRequest]) (*connect.Response[grpcviewv1.DeleteRequestResponse], error)
	MoveItem(context.Context, *connect.Request[grpcviewv1.MoveItemRequest]) (*connect.Response[grpcviewv1.MoveItemResponse], error)

	// RunScript evaluates an INLINE source: the engine takes a buffer, not a
	// saved script's name, so `script run <name>` resolves the name against a Get
	// snapshot before calling this.
	RunScript(context.Context, *connect.Request[grpcviewv1.RunScriptRequest]) (*connect.Response[grpcviewv1.RunScriptResponse], error)
}

// inProcess binds Client to the handler called directly as a Go value: no
// server, no port, no HTTP. Every method is the handler's own — including the
// two streaming ones, which workspace exports in send-func form precisely
// because there is no *connect.ServerStream to hand it here.
type inProcess struct {
	workspace.Workspace
}

// remote binds Client to the generated Connect client over HTTP. Only the
// streaming methods need adapting: the generated client returns a stream to
// pull from, so the pull loop lives here and the CLI above it never sees the
// difference.
type remote struct {
	grpcviewv1.WorkspaceServiceClient
}

func (r remote) InvokeStream(ctx context.Context, msg *grpcviewv1.InvokeStreamRequest, send func(*grpcviewv1.InvokeStreamResponse) error) error {
	stream, err := r.WorkspaceServiceClient.InvokeStreaming(ctx, connect.NewRequest(msg))
	if err != nil {
		return err
	}
	return drain(stream, send)
}

func (r remote) InvokeSavedStream(ctx context.Context, msg *grpcviewv1.InvokeSavedRequest, send func(*grpcviewv1.InvokeStreamResponse) error) error {
	stream, err := r.WorkspaceServiceClient.InvokeSavedStreaming(ctx, connect.NewRequest(msg))
	if err != nil {
		return err
	}
	return drain(stream, send)
}

// drain pumps a client-side stream into a send callback and closes it. A send
// failure stops the pump and wins over the stream's own error: the caller
// (a broken pipe, a render error) is the more specific failure.
func drain(stream *connect.ServerStreamForClient[grpcviewv1.InvokeStreamResponse], send func(*grpcviewv1.InvokeStreamResponse) error) error {
	defer stream.Close()
	for stream.Receive() {
		if err := send(stream.Msg()); err != nil {
			return err
		}
	}
	return stream.Err()
}

// The two bindings, asserted at compile time. This is the whole point of C0:
// if either signature drifts, the build breaks here rather than in a verb.
var (
	_ Client = inProcess{}
	_ Client = remote{}
)

// session is a Client together with the teardown its binding needs. The
// in-process binding holds a store and a QuickJS runtime and must be closed;
// the remote binding has nothing to release.
type session struct {
	Client
	// close releases the binding's resources. Never nil.
	close func(context.Context) error
}

// clientFactory opens a session on demand. Verbs close over a factory rather
// than a live client so that building the command tree — which every unit test
// does — never constructs a workspace.
type clientFactory func(ctx context.Context, g *globalFlags) (session, error)

// openClient is the production factory.
//
// There are exactly two modes and no autodetection. "Dial the local server if
// one happens to be listening" was rejected deliberately: which process wrote
// my history must not depend on whether a server was up when I ran the command.
// In-process is the default; --server addr is the explicit opt-in to the wire.
func openClient(ctx context.Context, g *globalFlags) (session, error) {
	if g != nil && g.Server != "" {
		return session{
			Client: remote{grpcviewv1.NewWorkspaceServiceClient(http.DefaultClient, g.Server)},
			close:  func(context.Context) error { return nil },
		}, nil
	}

	ws, err := workspace.New(ctx)
	if err != nil {
		return session{}, fmt.Errorf("failed to open workspace: %w", err)
	}
	return session{Client: inProcess{ws}, close: ws.Close}, nil
}
