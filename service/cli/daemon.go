package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"codeberg.org/ramilmsh/grpcview/service/daemon"
)

func newUrlCmd(s Streams, g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "url",
		Short: "Print this workspace's server URL, starting one if none is running",
		Long: "stdout, so it stays scriptable: `open \"$(grpcview url)\"`.\n\n" +
			"A server is started if none is running, because a URL nothing answers is not an answer.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			reg, err := addressDaemon(cmd.Context(), g, s, false)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), reg.URL())
			return nil
		},
	}
}

func newOpenCmd(s Streams, g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "open",
		Short: "Open a browser on this workspace's server, starting one if none is running",
		Long: "The launch is the action and the URL is not its output, so what was launched is\n" +
			"named on stderr. A headless box, an SSH session or no DISPLAY prints the URL and\n" +
			"carries on rather than failing.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			reg, err := addressDaemon(cmd.Context(), g, s, false)
			if err != nil {
				return err
			}
			daemon.Open(cmd.ErrOrStderr(), reg.URL())
			return nil
		},
	}
}

func newShutdownCmd(s Streams, g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "shutdown",
		Short: "Stop this workspace's server",
		Long: "Asks over the wire, after the server has proved it is this workspace's. A server\n" +
			"that is not running is a silent success. Prints nothing on success.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			reg, err := addressDaemon(cmd.Context(), g, s, true)
			if errors.Is(err, daemon.ErrNotRunning) {
				return nil
			}
			if err != nil {
				return err
			}
			return daemon.Stop(cmd.Context(), reg)
		},
	}
}

// The connect-or-spawn policy every binding shares: what a spawned server is told to do with
// itself, and where a restart note goes.
func connectDaemon(ctx context.Context, root string, notes io.Writer, noSpawn bool) (daemon.Registration, error) {
	return daemon.Connect(ctx, daemon.Options{
		Root:        root,
		IdleTimeout: daemon.DefaultIdleTimeout,
		Notes:       notes,
		NoSpawn:     noSpawn,
	})
}

// The lifecycle verbs address a server rather than call one, so they resolve the workspace
// themselves instead of going through withSession.
func addressDaemon(ctx context.Context, g *globalFlags, s Streams, noSpawn bool) (daemon.Registration, error) {
	if g.InProcess {
		return daemon.Registration{}, errors.New("--in-process names no server, so there is none to address")
	}
	if g.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, g.Timeout)
		defer cancel()
	}
	root, err := resolveRoot(g, s)
	if err != nil {
		return daemon.Registration{}, err
	}
	return connectDaemon(ctx, root, s.Err, noSpawn)
}
