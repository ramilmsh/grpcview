// Package cli is the argv surface: the cobra command tree, exit codes, and client bindings.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"
)

var version = "dev"

const defaultPort = 10000

const (
	defaultTimeout = 30 * time.Second
	buildTimeout   = 10 * time.Minute
)

type Streams struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

type ServeOptions struct {
	Port int
	// --port was passed, so a busy port is an error rather than a reason to take another one.
	PortPinned  bool
	Root        string
	IdleTimeout time.Duration
	OpenBrowser bool
	Version     string
}

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
	Workspace  string
	Collection string
	Server     string
	InProcess  bool
	Timeout    time.Duration

	resolved string
}

func registerGlobalFlags(cmd *cobra.Command) *globalFlags {
	g := &globalFlags{}
	f := cmd.PersistentFlags()
	f.StringVar(&g.Workspace, "workspace", "", "workspace root; empty takes $BUILD_WORKSPACE_DIRECTORY, else walks up from the current directory to the nearest .git")
	f.StringVar(&g.Collection, "collection", "", "collection to operate on; empty resolves from the current directory")
	f.StringVar(&g.Server, "server", "", "base URL of a specific grpcview server; empty uses this workspace's, starting one if none is running")
	f.BoolVar(&g.InProcess, "in-process", false, "do the work in this process and start no server; the escape hatch for CI, a read-only checkout and debugging")
	f.DurationVar(&g.Timeout, "timeout", defaultTimeout,
		"per-request timeout; a verb that may run a build — `sources refresh`, or `sources add` of a bazel label — defaults to "+buildTimeout.String()+" instead")
	return g
}

func useBuildTimeout(cmd *cobra.Command, g *globalFlags) {
	if flag := cmd.Flags().Lookup("timeout"); flag != nil && flag.Changed {
		return
	}
	g.Timeout = buildTimeout
}

func releaseVersion() string {
	if version == "" {
		return "dev"
	}
	return version
}

// The flags every way of starting a server shares. `grpcview` and `grpcview serve` are the
// same command with a different spelling, and a divergence between them is a bug.
type serveFlags struct {
	port        int
	idleTimeout time.Duration
	noOpen      bool
}

func registerServeFlags(cmd *cobra.Command) *serveFlags {
	f := &serveFlags{}
	cmd.Flags().IntVar(&f.port, "port", defaultPort,
		"port to serve on; a busy default falls back to an ephemeral port, a busy --port is an error")
	cmd.Flags().DurationVar(&f.idleTimeout, "idle-timeout", 0,
		"exit after this long with nothing in flight; zero never exits, which is what a hand-run server wants")
	cmd.Flags().BoolVar(&f.noOpen, "no-open", false, "do not open a browser on launch")
	return f
}

func (f *serveFlags) options(cmd *cobra.Command, g *globalFlags) ServeOptions {
	return ServeOptions{
		Port:        f.port,
		PortPinned:  cmd.Flags().Changed("port"),
		Root:        g.Workspace,
		IdleTimeout: f.idleTimeout,
		OpenBrowser: !f.noOpen,
		Version:     releaseVersion(),
	}
}

func newRootCmd(
	s Streams,
	serve func(context.Context, ServeOptions) error,
	open clientFactory,
) *cobra.Command {
	var rootServe *serveFlags
	var globals *globalFlags

	root := &cobra.Command{
		Use:   "grpcview",
		Short: "grpcview — a gRPC request client",
		Long: "grpcview serves its own UI and API, and exposes the same collection as\n" +
			"command-line verbs. Invoked with no subcommand, it serves.",
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return unknownCommand(cmd, args[0])
			}
			return serve(cmd.Context(), rootServe.options(cmd, globals))
		},
	}
	rootServe = registerServeFlags(root)

	root.Version = releaseVersion()
	root.SetVersionTemplate("{{.Version}}\n")

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
	root.AddCommand(newTrustCmd(globals, open))
	root.AddCommand(newServeCmd(serve, globals))
	root.AddCommand(newUrlCmd(s, globals))
	root.AddCommand(newOpenCmd(s, globals))
	root.AddCommand(newShutdownCmd(s, globals))
	root.AddCommand(newMcpCmd(globals, open))
	root.AddCommand(newVersionCmd())
	root.AddCommand(newUninstallCmd(s))

	root.SetIn(s.In)
	root.SetOut(s.Out)
	root.SetErr(s.Err)

	return root
}

func unknownCommand(cmd *cobra.Command, arg string) error {
	out := cmd.OutOrStdout()
	cmd.SetOut(cmd.ErrOrStderr())
	defer cmd.SetOut(out)
	_ = cmd.Usage()
	return statusError{code: 2, err: fmt.Errorf("unknown command %q for %q", arg, cmd.CommandPath())}
}

func newServeCmd(serve func(context.Context, ServeOptions) error, g *globalFlags) *cobra.Command {
	var flags *serveFlags
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the UI and API",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return serve(cmd.Context(), flags.options(cmd, g))
		},
	}
	flags = registerServeFlags(cmd)
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

func Main(ctx context.Context, args []string, s Streams, serve func(context.Context, ServeOptions) error) int {
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
