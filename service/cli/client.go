package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"connectrpc.com/connect"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/daemon"
	"codeberg.org/ramilmsh/grpcview/service/wire"
	"codeberg.org/ramilmsh/grpcview/service/workspace"
	"codeberg.org/ramilmsh/grpcview/service/wsroot"
)

type Client = wire.Client

type inProcess struct {
	workspace.Workspace
	root string
}

func (p inProcess) ServerInfo(
	_ context.Context,
	_ *connect.Request[grpcviewv1.ServerInfoRequest],
) (*connect.Response[grpcviewv1.ServerInfoResponse], error) {
	return connect.NewResponse(&grpcviewv1.ServerInfoResponse{
		WorkspaceRoot: p.root,
		Pid:           int32(os.Getpid()),
		Version:       releaseVersion(),
		Executable:    daemon.SelfExecutable().Proto(),
	}), nil
}

func (inProcess) Shutdown(
	_ context.Context,
	_ *connect.Request[grpcviewv1.ShutdownRequest],
) (*connect.Response[grpcviewv1.ShutdownResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented,
		errors.New("--in-process runs no server, so there is nothing to shut down"))
}

var _ Client = inProcess{}

type session struct {
	Client
	close func(context.Context) error
}

type clientFactory func(ctx context.Context, g *globalFlags) (session, error)

func noClose(context.Context) error { return nil }

// resolveRoot answers the one question every binding starts from: which workspace is this.
func resolveRoot(g *globalFlags, s Streams) (string, error) {
	var override string
	if g != nil {
		override = g.Workspace
	}
	cwd, err := wsroot.InvocationDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve the current directory: %w", err)
	}
	root, warn, err := wsroot.Discover(override, cwd)
	if err != nil {
		return "", err
	}
	if warn != "" {
		fmt.Fprintln(s.Err, warn)
	}
	return root, nil
}

// The binding rule, in order: an explicitly pinned server, then the explicit in-process escape
// hatch, then this workspace's daemon — connected to if one is running and started if not.
//
// "Dial whatever happens to be listening" is still rejected; this is not that. A registration
// keyed by workspace root and re-verified over the wire means the answer is never ambient, and
// starting one when none exists removes the conditional the old rule objected to.
func openClient(ctx context.Context, g *globalFlags, s Streams) (session, error) {
	if g != nil && g.Server != "" {
		client := wire.Remote(g.Server)
		warnRootMismatch(ctx, client, g, s)
		return session{Client: client, close: noClose}, nil
	}

	root, err := resolveRoot(g, s)
	if err != nil {
		return session{}, err
	}

	if g != nil && g.InProcess {
		ws, err := workspace.New(ctx, root)
		if err != nil {
			return session{}, fmt.Errorf("failed to open workspace: %w", err)
		}
		return session{Client: inProcess{Workspace: ws, root: root}, close: ws.Close}, nil
	}

	reg, err := connectDaemon(ctx, root, s.Err, false)
	if err != nil {
		return session{}, err
	}
	// Reconnecting, not pinned: the daemon idles out and a rebuild restarts it, and an MCP
	// session outlives both. A dial failure re-runs connect-or-spawn once.
	return session{Client: wire.Reconnecting(reg.URL(), dialer{root: root, notes: s.Err}.redial), close: noClose}, nil
}

// A struct rather than a closure: the redial outlives an MCP session, and capturing Streams
// would hold the CLI's stdio open for its whole lifetime.
type dialer struct {
	root  string
	notes io.Writer
}

func (d dialer) redial(ctx context.Context) (string, error) {
	reg, err := connectDaemon(ctx, d.root, d.notes, false)
	if err != nil {
		return "", err
	}
	return reg.URL(), nil
}

// A pinned server is the caller's decision, so a different root is a warning rather than a
// refusal — but it is never silent: a collection id resolved here would otherwise be
// interpreted against a tree the caller never named.
//
// Bounded on its own: `mcp` opens its client with no deadline, and a pinned server that does
// not answer must not hold the session's startup on a warning nobody asked for.
func warnRootMismatch(ctx context.Context, client Client, g *globalFlags, s Streams) {
	root, err := resolveRoot(g, s)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	res, err := client.ServerInfo(ctx, connect.NewRequest(&grpcviewv1.ServerInfoRequest{}))
	if err != nil {
		return
	}
	if got := res.Msg.GetWorkspaceRoot(); got != "" && got != root {
		fmt.Fprintf(s.Err, "grpcview: %s serves workspace %s, not %s\n", g.Server, got, root)
	}
}
