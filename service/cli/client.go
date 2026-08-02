package cli

import (
	"context"
	"fmt"
	"net/http"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/workspace"
)

// Client is the slice of WorkspaceService the CLI verbs use. Its methods are
// spelled with the Connect handler signature, which is also the generated
// Connect *client* signature for every unary RPC — so the in-process handler
// and the wire client satisfy one interface with no adapter. Only the streaming
// RPC differs between the two shapes, and no C0 verb needs it.
//
// Methods are added as verbs need them; C0 declares just enough to prove the
// two bindings line up.
type Client interface {
	Get(context.Context, *connect.Request[grpcviewv1.GetRequest]) (*connect.Response[grpcviewv1.GetResponse], error)
	Invoke(context.Context, *connect.Request[grpcviewv1.InvokeRequest]) (*connect.Response[grpcviewv1.InvokeResponse], error)
}

// The two bindings, asserted at compile time. This is the whole point of C0:
// if either signature drifts, the build breaks here rather than in a verb.
var (
	// in-process: a plain Go struct over the store and the scripting engine,
	// no server and no port. Value receivers, so the value satisfies Client.
	_ Client = workspace.Workspace{}
	// remote: the generated Connect client over HTTP. Asserted against the
	// interface NewWorkspaceServiceClient returns, so the check costs nothing at
	// init (the constructor does real reflection work).
	_ Client = (grpcviewv1.WorkspaceServiceClient)(nil)
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
			Client: grpcviewv1.NewWorkspaceServiceClient(http.DefaultClient, g.Server),
			close:  func(context.Context) error { return nil },
		}, nil
	}

	ws, err := workspace.New(ctx)
	if err != nil {
		return session{}, fmt.Errorf("failed to open workspace: %w", err)
	}
	return session{Client: ws, close: ws.Close}, nil
}
