// Package cli is the argv surface of the grpcview binary: the cobra command
// tree, the exit-code contract, and the two client bindings.
//
// It deliberately does not import //service. The UI embed (26.9 MB of
// embedsrcs) lives in //service/cmd, and a cli -> service edge would drag it
// into every CLI test. Serving is injected into [Main] as a closure instead.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"
)

// version is stamped at link time by the go_binary's x_defs. It keeps this
// default on an unstamped build: rules_go resolves {VAR} against the stamp map
// and, finding no key, omits the -X flag entirely rather than embedding the
// literal placeholder (measured both ways, and with a fake status command
// proving the x_defs symbol is wired to this variable).
var version = "dev"

// defaultPort is the port both the bare invocation and `serve` listen on.
const defaultPort = 10000

// Streams are the process's stdio, injected so every verb is table-testable
// without touching os.Stdout.
type Streams struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// ServeOptions is what the serve verb (and the bare, subcommand-less invocation)
// hands back to the caller that owns the HTTP server and the UI embed.
type ServeOptions struct{ Port int }

// statusError carries an explicit process exit code out of a verb's RunE. A
// non-OK gRPC status from the workspace is a statusError with code 1; every
// other failure — including cobra's own flag-parse errors — maps to 2.
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

// exitCode maps an error returned by the command tree to a process exit code:
// nil is 0, a statusError carries its own, anything else is 2.
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

// globalFlags holds the root's persistent flags. They are bound exactly once,
// by registerGlobalFlags, and verbs read this struct instead of re-declaring
// flag names or defaults of their own.
type globalFlags struct {
	// Workspace names the collection to operate on.
	Workspace string
	// Server, when non-empty, is the base URL of a running grpcview server to
	// talk to instead of doing the work in-process.
	Server string
	// Timeout bounds every RPC; verbs apply it as a context.WithTimeout.
	Timeout time.Duration
}

func registerGlobalFlags(cmd *cobra.Command) *globalFlags {
	g := &globalFlags{}
	f := cmd.PersistentFlags()
	f.StringVar(&g.Workspace, "workspace", "default", "workspace (collection) to operate on")
	f.StringVar(&g.Server, "server", "", "base URL of a running grpcview server; empty does the work in-process")
	f.DurationVar(&g.Timeout, "timeout", 30*time.Second, "per-request timeout")
	return g
}

// releaseVersion is the string the version verb prints. The empty check is the
// load-bearing one: this repo has no v* tags yet, so tools/workspace_status.sh
// emits an empty STABLE_VERSION_TAG and a --stamp build links -X …version= with
// nothing after it. Once a tag exists, a stamped build prints it.
func releaseVersion() string {
	if version == "" {
		return "dev"
	}
	return version
}

// newRootCmd builds the whole command tree. serve is the closure that owns the
// HTTP server and the UI embed. open is the client factory every verb closes
// over — a factory, not a live client, so unit tests never construct a
// workspace (which compiles the ~660 KiB QuickJS module).
func newRootCmd(
	s Streams,
	serve func(context.Context, ServeOptions) error,
	open clientFactory,
) *cobra.Command {
	var rootPort int

	root := &cobra.Command{
		Use:   "grpcview",
		Short: "grpcview — a gRPC request client",
		Long: "grpcview serves its own UI and API, and exposes the same workspace as\n" +
			"command-line verbs. Invoked with no subcommand, it serves.",
		// The root both dispatches subcommands and has its own RunE, so cobra
		// has to be told leftover args are acceptable. ArbitraryArgs on its own
		// would make `grpcview typoe` serve the UI, hence the explicit
		// unknown-verb check in RunE.
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

	// Persistent flags are declared here and nowhere else, and verbs read the
	// returned struct rather than re-declaring names or defaults. `-o` is
	// deliberately not among them: each verb registers its own, with a disjoint
	// set of accepted values.
	globals := registerGlobalFlags(root)

	root.AddCommand(newInvokeCmd(s, globals, open))
	root.AddCommand(newServeCmd(serve))
	root.AddCommand(newVersionCmd())

	root.SetIn(s.In)
	root.SetOut(s.Out)
	root.SetErr(s.Err)

	return root
}

// unknownCommand dumps cobra's usage on stderr — never stdout — and reports
// exit 2.
func unknownCommand(cmd *cobra.Command, arg string) error {
	// Usage writes to OutOrStderr, which is stdout here because Streams.Out is
	// set; point it at stderr for the duration of the dump.
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

// execute runs an already-built tree and applies the error contract: one line
// on stderr, always prefixed "grpcview: ", and the mapped exit code. Tests call
// it with a tree carrying a fake verb to exercise the mapping.
func execute(ctx context.Context, root *cobra.Command, args []string, s Streams) int {
	root.SetArgs(args)

	err := root.ExecuteContext(ctx)
	if err != nil {
		fmt.Fprintf(s.Err, "grpcview: %s\n", err)
	}
	return exitCode(err)
}
