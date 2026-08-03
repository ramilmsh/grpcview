package cli

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

// newGetCmd builds `grpcview get`.
//
// There is no -o (D8): a whole workspace has exactly one shape, and the point of
// the verb is that `grpcview get | jq` reaches every field the UI reads —
// including the ones ls and `sources ls` deliberately summarize.
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
	// The whole GetResponse, not just its workspace: the envelope is what the RPC
	// returns, so a script written against `get` output and one written against
	// the RPC read the same paths.
	line, err := marshalOneLine(snapshot)
	if err != nil {
		return fmt.Errorf("failed to render workspace %q: %w", g.Workspace, err)
	}
	return writeLine(s.Out, line)
}

// readWorkspace is the one snapshot read the three read verbs share: ls, get and
// `sources ls` all render fields of a single Get and mutate nothing, so none of
// them needs the session to outlive the call.
//
// --timeout bounds the Get, exactly as it bounds an invoke: a hung snapshot read
// is the only way these verbs can hang.
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
		// A Connect error is grpcview's own failure, and nothing was invoked: exit
		// 2 by the default mapping (D9). A read verb cannot produce exit 1.
		return nil, fmt.Errorf("failed to read workspace %q: %w", g.Workspace, err)
	}
	return resp.Msg, nil
}
