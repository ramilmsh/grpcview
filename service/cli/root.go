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

type ServeOptions struct {
	Port int
	// Root is the raw --workspace override, passed through unresolved: service.Run does
	// its own wsroot.Discover(Root, cwd) and logs the warning, so serving is the one path
	// that discovers relative to the SERVER's cwd, not the CLI process invoking it.
	Root string
}

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
	// Workspace is the raw --workspace override: the workspace ROOT, not a collection
	// address. Empty means "discover one" — see wsroot.Discover, called from
	// service/cli/client.go's openClient and from service.Run.
	Workspace  string
	Collection string
	Server     string
	Timeout    time.Duration

	// resolved memoizes resolveCollection's answer for this invocation.
	resolved string
}

func registerGlobalFlags(cmd *cobra.Command) *globalFlags {
	g := &globalFlags{}
	f := cmd.PersistentFlags()
	f.StringVar(&g.Workspace, "workspace", "", "workspace root; empty walks up from the current directory to the nearest .git")
	// Empty, not ".": where you stand decides what you address, the same way `git` and
	// `bazel` work — resolveCollection walks up from the cwd. A "." default would instead
	// point every invocation at the workspace root, which in a monorepo holds no collection.
	f.StringVar(&g.Collection, "collection", "", "collection to operate on; empty resolves from the current directory")
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
	// globals is assigned below, before Execute ever runs RunE — registerGlobalFlags needs
	// the *cobra.Command literal to already exist, so it can't run before this closure is
	// written, but it does run before the closure is ever CALLED.
	var globals *globalFlags

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
			return serve(cmd.Context(), ServeOptions{Port: rootPort, Root: globals.Workspace})
		},
	}
	root.Flags().IntVar(&rootPort, "port", defaultPort, "port to serve on")

	globals = registerGlobalFlags(root)

	root.AddCommand(newInvokeCmd(s, globals, open))
	root.AddCommand(newDescribeCmd(s, globals, open))
	root.AddCommand(newLsCmd(s, globals, open))
	root.AddCommand(newGetCmd(s, globals, open))
	root.AddCommand(newSourcesCmd(s, globals, open))
	root.AddCommand(newRequestCmd(s, globals, open))
	root.AddCommand(newFolderCmd(globals, open))
	root.AddCommand(newScriptCmd(s, globals, open))
	root.AddCommand(newInitCmd(s, globals, open))
	root.AddCommand(newCollectionsCmd(s, globals, open))
	root.AddCommand(newServeCmd(serve, globals))
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

func newServeCmd(serve func(context.Context, ServeOptions) error, g *globalFlags) *cobra.Command {
	var port int
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the UI and API",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return serve(cmd.Context(), ServeOptions{Port: port, Root: g.Workspace})
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
	// openClient needs Streams (to surface wsroot.Discover's warning on s.Err), but
	// clientFactory does not carry one — every verb already has its own s — so it is
	// bound here, once, at the production wiring.
	open := func(ctx context.Context, g *globalFlags) (session, error) { return openClient(ctx, g, s) }
	return execute(ctx, newRootCmd(s, serve, open), args, s)
}

func execute(ctx context.Context, root *cobra.Command, args []string, s Streams) int {
	root.SetArgs(args)

	err := root.ExecuteContext(ctx)
	if err != nil {
		fmt.Fprintf(s.Err, "grpcview: %s\n", err)
	}
	return exitCode(err)
}
