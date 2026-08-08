package cli

import (
	"github.com/spf13/cobra"

	"codeberg.org/ramilmsh/grpcview/service/mcp"
	"codeberg.org/ramilmsh/grpcview/service/wire"
)

func newMcpCmd(g *globalFlags, open clientFactory) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Serve the Model Context Protocol over stdio",
		Long: "Tools are grpcview's own RPCs. The session talks to this workspace's daemon like\n" +
			"every other verb, so an agent's writes, the UI's and the CLI's all serialize on one\n" +
			"process; --in-process opts out and takes the collection directory on its own.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Deliberately not withSession: an MCP session is long-lived and the global 30s
			// --timeout default would kill it mid-conversation.
			sess, err := open(cmd.Context(), g)
			if err != nil {
				return err
			}
			defer func() { _ = sess.close(cmd.Context()) }()

			// A conversation can go quiet for longer than the daemon's idle window while the
			// session is still very much alive. The heartbeat stops when this process does, so
			// the daemon outlives the agent by one idle window and no longer.
			go wire.Keepalive(cmd.Context(), sess.Client)

			return mcp.Run(cmd.Context(), mcp.Options{
				Collection: g.Collection,
				Version:    releaseVersion(),
			}, sess.Client)
		},
	}
}
