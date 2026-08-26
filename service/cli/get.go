package cli

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/grpcview/v1"
)

func newGetCmd(s Streams, g *globalFlags, open clientFactory) *cobra.Command {
	return &cobra.Command{
		Use:   "get",
		Short: "Print the whole collection as JSON",
		Long: "Print the collection — the collection tree, the merged services, the\n" +
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
	return withCollection(ctx, g, open, func(ctx context.Context, sess session, collection string) error {
		snapshot, err := readWorkspace(ctx, sess, collection)
		if err != nil {
			return err
		}
		line, err := marshalOneLine(snapshot)
		if err != nil {
			return fmt.Errorf("failed to render collection %q: %w", collection, err)
		}
		return writeLine(s.Out, line)
	})
}

func readWorkspace(ctx context.Context, sess session, collection string) (*grpcviewv1.GetResponse, error) {
	resp, err := sess.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{Collection: collection}))
	if err != nil {
		return nil, fmt.Errorf("failed to read collection %q: %w", collection, err)
	}
	return resp.Msg, nil
}
