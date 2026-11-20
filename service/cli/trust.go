package cli

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/grpcview/v1"
)

func newTrustCmd(g *globalFlags, open clientFactory) *cobra.Command {
	var off bool

	cmd := &cobra.Command{
		Use:   "trust",
		Short: "Trust this workspace, allowing definition sources that run bazel",
		Long: "Trust this workspace root, so definition sources that must EXECUTE something to\n" +
			"resolve are allowed to. `--off` revokes it.\n\n" +
			"The threat is concrete. A committed grpcview.json naming a bazel label means that\n" +
			"opening a repository can run `bazel query` and `bazel cquery` on it. Those load the\n" +
			"repository's own BUILD and .bzl files and run its module extensions and repo rules\n" +
			"— arbitrary code, with your privileges, unsandboxed. That is the same class of risk\n" +
			"as VS Code's tasks, which is why VS Code asks the same question, and why this is\n" +
			"one decision you make once per workspace instead of a prompt on every refresh.\n\n" +
			"Untrusted is a working state, not a broken one: every collection still loads, and\n" +
			"reflection and upload sources still resolve normally. Only a source that would\n" +
			"run bazel is refused, and it stays listed with that reason on its row —\n" +
			"`sources ls` shows it.\n\n" +
			"Trust is on the FOLDER, not on its current content. A workspace you trusted\n" +
			"yesterday stays trusted after today's `git pull`, exactly as in VS Code; the\n" +
			"decision is about who you got the repository from. It is remembered in your own\n" +
			"user state and never in the repository, because a `trusted: true` a repository\n" +
			"could commit about itself would say nothing.\n\n" +
			"`--off` un-resolves nothing. Descriptors already acquired stay where they are and\n" +
			"everything keeps loading, describing and invoking from them; only the next bazel\n" +
			"run is refused. Revoking is meant to be a safe thing to do.\n\n" +
			"WHICH workspace this trusts is the one --workspace names, or the one discovered by\n" +
			"walking up from the current directory — the same root every other verb addresses.\n" +
			"`grpcview collections ls` names that root. Whether it is trusted is reported by\n" +
			"`grpcview sources ls`, and only where it matters: a collection listing a bazel\n" +
			"source gets a line naming this verb, and one listing none is never asked for a\n" +
			"permission nothing would use.\n\n" +
			"Nothing is printed on success.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runTrust(cmd.Context(), g, open, !off)
		},
	}
	cmd.Flags().BoolVar(&off, "off", false,
		"revoke trust: keep every descriptor already acquired, and refuse the next bazel run")

	return cmd
}

func runTrust(ctx context.Context, g *globalFlags, open clientFactory, trusted bool) error {
	return withSession(ctx, g, open, func(ctx context.Context, sess session) error {
		_, err := sess.SetWorkspaceTrust(ctx, connect.NewRequest(&grpcviewv1.SetWorkspaceTrustRequest{
			Trusted: trusted,
		}))
		if err != nil {
			verb := "trust"
			if !trusted {
				verb = "un-trust"
			}
			return fmt.Errorf("failed to %s this workspace: %w", verb, err)
		}
		return nil
	})
}
