// Package cli is the argv surface of the grpcview binary: the cobra command
// tree, the exit-code contract, and the two client bindings.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"
)

// version is stamped at link time by the go_binary's x_defs; an untagged --stamp build links it empty.
var version = "dev"

const defaultPort = 10000

// Streams are the process's stdio, injected so every verb is table-testable.
type Streams struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

type ServeOptions struct{ Port int }

// statusError carries an explicit process exit code out of a verb's RunE.
type statusError struct {
	code int
	err  error
}

func (e statusError) Error() string {
	if e.err == nil {
		return fmt.Sprintf("exit status %d", e.code)
	}
	return e.err.Error()
}

func (e statusError) Unwrap() error { return e.err }

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var se statusError
	if errors.As(err, &se) {
		return se.code
	}
	return 2
}

type globalFlags struct {
	Collection string
	Server     string
	Timeout    time.Duration
}

func registerGlobalFlags(cmd *cobra.Command) *globalFlags {
	g := &globalFlags{}
	f := cmd.PersistentFlags()
	f.StringVar(&g.Collection, "collection", "default", "collection to operate on")
	f.StringVar(&g.Server, "server", "", "base URL of a running grpcview server; empty does the work in-process")
	f.DurationVar(&g.Timeout, "timeout", 30*time.Second, "per-request timeout")
	return g
}

func releaseVersion() string {
	if version == "" {
		return "dev"
	}
	return version
}

func newRootCmd(
	s Streams,
	serve func(context.Context, ServeOptions) error,
	open clientFactory,
) *cobra.Command {
	var rootPort int

	root := &cobra.Command{
		Use:   "grpcview",
		Short: "grpcview — a gRPC request client",
		Long: "grpcview serves its own UI and API, and exposes the same collection as\n" +
			"command-line verbs. Invoked with no subcommand, it serves.",
		// ArbitraryArgs alone would make `grpcview typoe` serve the UI, hence RunE's check.
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return unknownCommand(cmd, args[0])
			}
			return serve(cmd.Context(), ServeOptions{Port: rootPort})
		},
	}
	root.Flags().IntVar(&rootPort, "port", defaultPort, "port to serve on")

	globals := registerGlobalFlags(root)

	root.AddCommand(newInvokeCmd(s, globals, open))
	root.AddCommand(newDescribeCmd(s, globals, open))
	root.AddCommand(newLsCmd(s, globals, open))
	root.AddCommand(newGetCmd(s, globals, open))
	root.AddCommand(newSourcesCmd(s, globals, open))
	root.AddCommand(newRequestCmd(s, globals, open))
	root.AddCommand(newFolderCmd(globals, open))
	root.AddCommand(newScriptCmd(s, globals, open))
	root.AddCommand(newServeCmd(serve))
	root.AddCommand(newVersionCmd())

	root.SetIn(s.In)
	root.SetOut(s.Out)
	root.SetErr(s.Err)

	return root
}

func unknownCommand(cmd *cobra.Command, arg string) error {
	// cobra's Usage writes to OutOrStdout, which is stdout here.
	out := cmd.OutOrStdout()
	cmd.SetOut(cmd.ErrOrStderr())
	defer cmd.SetOut(out)
	_ = cmd.Usage()
	return statusError{code: 2, err: fmt.Errorf("unknown command %q for %q", arg, cmd.CommandPath())}
}

func newServeCmd(serve func(context.Context, ServeOptions) error) *cobra.Command {
	var port int
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the UI and API",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return serve(cmd.Context(), ServeOptions{Port: port})
		},
	}
	cmd.Flags().IntVar(&port, "port", defaultPort, "port to serve on")
	return cmd
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the grpcview version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(cmd.OutOrStdout(), releaseVersion())
			return nil
		},
	}
}

// Main builds the command tree, executes it, and returns the process exit code.
func Main(ctx context.Context, args []string, s Streams, serve func(context.Context, ServeOptions) error) int {
	return execute(ctx, newRootCmd(s, serve, openClient), args, s)
}

func execute(ctx context.Context, root *cobra.Command, args []string, s Streams) int {
	root.SetArgs(args)

	err := root.ExecuteContext(ctx)
	if err != nil {
		fmt.Fprintf(s.Err, "grpcview: %s\n", err)
	}
	return exitCode(err)
}
