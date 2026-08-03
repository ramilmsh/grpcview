package cli

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

func newGetCmd(s Streams, g *globalFlags, open clientFactory) *cobra.Command {
	return &cobra.Command{
		Use:   "get",
		Short: "Print the whole workspace as JSON",
		Long: "Print the workspace — the collection tree, the merged services, the\n" +
			"definition sources and the scripts — as one line of protojson on stdout, so\n" +
			"it pipes into jq.\n\n" +
			"One line, not indented: protojson randomizes its own whitespace between\n" +
			"runs, so a stable byte stream is the only diffable one.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGet(cmd.Context(), s, g, open)
		},
	}
}

func runGet(ctx context.Context, s Streams, g *globalFlags, open clientFactory) error {
	snapshot, err := readWorkspace(ctx, g, open)
	if err != nil {
		return err
	}
	// The whole GetResponse, so `get` output and the RPC read the same paths.
	line, err := marshalOneLine(snapshot)
	if err != nil {
		return fmt.Errorf("failed to render workspace %q: %w", g.Workspace, err)
	}
	return writeLine(s.Out, line)
}

// readWorkspace is the one snapshot read ls, get and `sources ls` share.
func readWorkspace(ctx context.Context, g *globalFlags, open clientFactory) (*grpcviewv1.GetResponse, error) {
	if g.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, g.Timeout)
		defer cancel()
	}

	sess, err := open(ctx, g)
	if err != nil {
		return nil, err
	}
	defer func() { _ = sess.close(ctx) }()

	resp, err := sess.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{WorkspaceName: g.Workspace}))
	if err != nil {
		return nil, fmt.Errorf("failed to read workspace %q: %w", g.Workspace, err)
	}
	return resp.Msg, nil
}
