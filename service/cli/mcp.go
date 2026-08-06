package cli

import (
	"github.com/spf13/cobra"

	"codeberg.org/ramilmsh/grpcview/service/mcp"
)

func newMcpCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Serve the Model Context Protocol over stdio",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return mcp.Run(cmd.Context(), mcp.Options{
				Root:       g.Workspace,
				Collection: g.Collection,
				Version:    releaseVersion(),
			})
		},
	}
}
