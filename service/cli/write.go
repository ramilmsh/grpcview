package cli

// write.go holds the collection and script write verbs — `request`, `folder` and
// `script` — plus the session helper the `sources` mutations in sources.go share.
// One output contract covers every mutation in both files.
//
// SILENCE IS SUCCESS. A mutation that worked prints NOTHING, on either stream, and
// exits 0. That is the unix convention and the only one that composes: a script
// creating fifty requests in a loop wants fifty silent successes, and `set -e` plus
// an empty stdout is the whole interface. In particular the Workspace that every
// mutation RPC returns is deliberately dropped — a caller who wants the resulting
// state pipes `grpcview get` into jq, and printing 26 MB of workspace after a
// one-field patch would make stdout useless for anything else (D8).
//
// A failure is one "grpcview: "-prefixed line on stderr and exit 2. These verbs
// cannot produce exit 1: nothing was invoked in D9's sense, so there is no gRPC
// status of a target call to inherit.
//
// `script run` is the one exception, and a deliberate one — it produces data. See
// renderScriptRun for the mapping and why its failure is exit 1.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/workspace"
)

// withSession opens one session, runs fn inside it, and closes it.
//
// It is readWorkspace's sibling rather than a caller of it, because a mutating
// verb can make more than one call: `request create -f` creates and then patches,
// and `sources refresh` with no id reads the source list and then refreshes each
// source. Those calls have to share one session — reopening the in-process
// binding between two halves of one logical mutation would recompile the QuickJS
// module and reread the store, and readWorkspace opens and closes a session per
// call by design.
//
// --timeout bounds the whole verb rather than each individual call, which is what
// a script means by it: "this command finishes within 30s or fails".
func withSession(ctx context.Context, g *globalFlags, open clientFactory, fn func(context.Context, session) error) error {
	if g.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, g.Timeout)
		defer cancel()
	}

	sess, err := open(ctx, g)
	if err != nil {
		return err
	}
	defer func() { _ = sess.close(ctx) }()
	return fn(ctx, sess)
}

// workspaceSnapshot is the Get a mutating verb makes inside its own session, for
// the two cases that need to read before they write: refreshing every source in
// priority order, and resolving a saved script's name to its source.
func workspaceSnapshot(ctx context.Context, sess session, g *globalFlags) (*grpcviewv1.Workspace, error) {
	resp, err := sess.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{WorkspaceName: g.Workspace}))
	if err != nil {
		return nil, fmt.Errorf("failed to read workspace %q: %w", g.Workspace, err)
	}
	return resp.Msg.GetWorkspace(), nil
}

// splitItemPath parses a <path> argument that addresses one tree item: the last
// segment is the item's own display name and the leading ones are its parent
// folders, which is exactly the `path` + `item_name` pair every item RPC takes.
//
// It is workspace.SplitInvokePath — the parser gv.invoke and `invoke` already use,
// so every surface agrees on what "Auth/Login" addresses — with the error
// rephrased, since SplitInvokePath's own message names gv.invoke.
func splitItemPath(arg string) ([]string, string, error) {
	parent, name, err := workspace.SplitInvokePath(arg)
	if err != nil {
		return nil, "", fmt.Errorf(
			"invalid path %q: the last segment names the item and cannot be empty", arg)
	}
	return parent, name, nil
}

// splitFolderPath parses a DESTINATION folder path, which is the whole segment
// list: unlike an item path there is no trailing name to peel off. The empty path
// and "/" both address the collection root, which is a legitimate destination for
// a move rather than an error — moving something back out to the top level is the
// most ordinary move there is.
func splitFolderPath(arg string) ([]string, error) {
	if arg == "" || arg == "/" {
		return nil, nil
	}
	parent, name, err := workspace.SplitInvokePath(arg)
	if err != nil {
		return nil, fmt.Errorf("invalid folder path %q: no path segment may be empty", arg)
	}
	return append(parent, name), nil
}

// ---------------------------------------------------------------- folder ------

func newFolderCmd(g *globalFlags, open clientFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "folder",
		Short: "Create folders in the collection",
		Long: "Folders group requests in the collection tree, and their nesting is what a\n" +
			"request path spells.\n\n" +
			"There is deliberately no `folder rm` or `folder mv`: `request rm` and\n" +
			"`request mv` both operate on an item of EITHER kind, which is what the\n" +
			"underlying RPCs do, and one verb per operation is less to remember than two.",
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return unknownCommand(cmd, args[0])
			}
			return missingSubcommand(cmd, "create")
		},
	}
	cmd.AddCommand(newFolderCreateCmd(g, open))
	return cmd
}

func newFolderCreateCmd(g *globalFlags, open clientFactory) *cobra.Command {
	return &cobra.Command{
		Use:   "create <path>",
		Short: "Create a folder",
		Long: "Create a folder at a display-name path: the last segment is the new folder's\n" +
			"name and the leading ones must already exist.\n\n" +
			"Nothing is printed on success. A name that collides with an existing sibling\n" +
			"is an error rather than a merge.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFolderCreate(cmd.Context(), g, open, args[0])
		},
	}
}

func runFolderCreate(ctx context.Context, g *globalFlags, open clientFactory, arg string) error {
	parent, name, err := splitItemPath(arg)
	if err != nil {
		return err
	}
	return withSession(ctx, g, open, func(ctx context.Context, sess session) error {
		_, err := sess.CreateFolder(ctx, connect.NewRequest(&grpcviewv1.CreateFolderRequest{
			WorkspaceName: g.Workspace,
			Path:          parent,
			ItemName:      name,
		}))
		if err != nil {
			return fmt.Errorf("failed to create the folder %q: %w", arg, err)
		}
		return nil
	})
}

// --------------------------------------------------------------- request ------

func newRequestCmd(s Streams, g *globalFlags, open clientFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "request",
		Short: "Create, delete and move saved requests",
		Long: "Manage the collection's saved items. These are the mutations a script has a\n" +
			"reason to make; authoring a request's TypeScript body or metadata is an\n" +
			"editor's job, so there is no verb for it and there are no per-field flags.\n\n" +
			"`request rm` and `request mv` act on an item of either kind, folders included.",
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return unknownCommand(cmd, args[0])
			}
			return missingSubcommand(cmd, "create", "rm", "mv")
		},
	}
	cmd.AddCommand(newRequestCreateCmd(s, g, open))
	cmd.AddCommand(newRequestRmCmd(g, open))
	cmd.AddCommand(newRequestMvCmd(g, open))
	return cmd
}

// requestCreateFlags are `request create`'s own flags. There is deliberately no
// --draft-body or --draft-metadata-script inline-string flag and no per-field
// flag of any kind (D12): structured input arrives as a file.
type requestCreateFlags struct {
	service string
	method  string
	file    string
}

func newRequestCreateCmd(s Streams, g *globalFlags, open clientFactory) *cobra.Command {
	f := &requestCreateFlags{}

	cmd := &cobra.Command{
		Use:   "create <path> --service <service> --method <method>",
		Short: "Create a saved request calling a given method",
		Long: "Create a saved request at a display-name path, calling --service/--method.\n" +
			"Both are required: a request that names no method is not a request the UI can\n" +
			"open, and guessing one from the schema would be a guess.\n\n" +
			"With -f the file's bytes seed the request's body, unchanged — plain protojson\n" +
			"is a valid body, so `request create ... -f body.json` is how a script stamps\n" +
			"out a request it can immediately invoke. The body is a SECOND call\n" +
			"(UpdateRequest), because CreateRequest carries no body field.\n\n" +
			"Nothing is printed on success.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRequestCreate(cmd.Context(), s, g, open, f, args[0])
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&f.service, "service", "", "full name of the service to call (required)")
	flags.StringVar(&f.method, "method", "", "name of the method to call (required)")
	flags.StringVarP(&f.file, "file", "f", "", "seed the request's body from this file; - reads stdin. Without it, stdin is read when piped")
	// A missing required flag is cobra's own error, which the root's
	// SilenceErrors/SilenceUsage turn into one "grpcview: " line on stderr and
	// exit 2 with no usage dump — the same shape as every other exit-2 failure.
	_ = cmd.MarkFlagRequired("service")
	_ = cmd.MarkFlagRequired("method")

	return cmd
}

func runRequestCreate(ctx context.Context, s Streams, g *globalFlags, open clientFactory, f *requestCreateFlags, arg string) error {
	parent, name, err := splitItemPath(arg)
	if err != nil {
		return err
	}

	// The body is read BEFORE anything is created: an unreadable -f must not leave
	// a bodyless request behind for the caller to clean up.
	raw, err := readBody(s, f.file)
	if err != nil {
		return err
	}

	return withSession(ctx, g, open, func(ctx context.Context, sess session) error {
		_, err := sess.CreateRequest(ctx, connect.NewRequest(&grpcviewv1.CreateRequestRequest{
			WorkspaceName: g.Workspace,
			Path:          parent,
			ItemName:      name,
			Service:       f.service,
			Method:        f.method,
		}))
		if err != nil {
			return fmt.Errorf("failed to create the request %q: %w", arg, err)
		}
		if raw == nil {
			return nil
		}

		// The bytes go through unchanged, exactly as -f does for invoke: the
		// backend normalizes protojson and TypeScript at one seam, and reformatting
		// here would fork that contract.
		_, err = sess.UpdateRequest(ctx, connect.NewRequest(&grpcviewv1.UpdateRequestRequest{
			WorkspaceName: g.Workspace,
			Path:          parent,
			ItemName:      name,
			DraftBody:     proto.String(string(raw)),
		}))
		if err != nil {
			return fmt.Errorf("created the request %q, but failed to seed its body: %w", arg, err)
		}
		return nil
	})
}

func newRequestRmCmd(g *globalFlags, open clientFactory) *cobra.Command {
	return &cobra.Command{
		Use:   "rm <path>",
		Short: "Delete an item — a request, or a folder and everything under it",
		Long: "Delete the item at a display-name path. Two properties of the underlying RPC\n" +
			"are worth knowing before scripting it:\n\n" +
			"  * It deletes items of EITHER kind, and a folder goes with its whole subtree.\n" +
			"    There is no confirmation and no --recursive flag to withhold.\n" +
			"  * A path that names nothing is a SILENT SUCCESS, not an error. Deleting what\n" +
			"    is already gone is a benign repeat of the caller's intent, which is what\n" +
			"    makes a cleanup loop idempotent — but it also means a typo'd path exits 0\n" +
			"    having removed nothing.\n\n" +
			"Nothing is printed on success.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRequestRm(cmd.Context(), g, open, args[0])
		},
	}
}

func runRequestRm(ctx context.Context, g *globalFlags, open clientFactory, arg string) error {
	parent, name, err := splitItemPath(arg)
	if err != nil {
		return err
	}
	return withSession(ctx, g, open, func(ctx context.Context, sess session) error {
		_, err := sess.DeleteRequest(ctx, connect.NewRequest(&grpcviewv1.DeleteRequestRequest{
			WorkspaceName: g.Workspace,
			Path:          parent,
			ItemName:      name,
		}))
		if err != nil {
			return fmt.Errorf("failed to delete %q: %w", arg, err)
		}
		return nil
	})
}

func newRequestMvCmd(g *globalFlags, open clientFactory) *cobra.Command {
	var before string

	cmd := &cobra.Command{
		Use:   "mv <path> <new-parent>",
		Short: "Move an item to another folder, or reorder it within its own",
		Long: "Move the item at <path> into the folder <new-parent>, which is a folder path\n" +
			"— an empty string or \"/\" is the collection root.\n\n" +
			"Reparenting and reordering are the same operation: a <new-parent> that resolves\n" +
			"to the item's CURRENT parent is a pure reorder, leaving the item where it is on\n" +
			"disk and changing only the recorded sibling order. --before names a sibling in\n" +
			"the destination to insert ahead of; without it the item is appended, and a\n" +
			"--before that names no child of the destination appends too rather than failing.\n\n" +
			"Moving a folder into itself or into one of its own descendants is refused, as\n" +
			"is a move into a folder that already holds an item of the same name — a move\n" +
			"never silently renames what it moves.\n\n" +
			"Nothing is printed on success.",
		Args:          cobra.ExactArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Changed, not the empty string: "unset" means append, and an item
			// literally named "" is not a thing the flag should be unable to say.
			var beforeArg *string
			if cmd.Flags().Changed("before") {
				beforeArg = proto.String(before)
			}
			return runRequestMv(cmd.Context(), g, open, args[0], args[1], beforeArg)
		},
	}
	cmd.Flags().StringVar(&before, "before", "", "name of a sibling in the destination to insert ahead of; unset appends")

	return cmd
}

func runRequestMv(ctx context.Context, g *globalFlags, open clientFactory, arg, dest string, before *string) error {
	parent, name, err := splitItemPath(arg)
	if err != nil {
		return err
	}
	newPath, err := splitFolderPath(dest)
	if err != nil {
		return err
	}
	return withSession(ctx, g, open, func(ctx context.Context, sess session) error {
		_, err := sess.MoveItem(ctx, connect.NewRequest(&grpcviewv1.MoveItemRequest{
			WorkspaceName: g.Workspace,
			Path:          parent,
			ItemName:      name,
			NewPath:       newPath,
			Before:        before,
		}))
		if err != nil {
			return fmt.Errorf("failed to move %q: %w", arg, err)
		}
		return nil
	})
}

// ---------------------------------------------------------------- script ------

func newScriptCmd(s Streams, g *globalFlags, open clientFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "script",
		Short: "List the workspace's scripts and run one",
		Long: "List the saved scripts and evaluate one through the scripting engine.\n\n" +
			"There is no create, update or delete here: writing TypeScript is an editor's\n" +
			"job, and a CLI flag is the wrong place to put a module.",
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return unknownCommand(cmd, args[0])
			}
			return missingSubcommand(cmd, "ls", "run")
		},
	}
	cmd.AddCommand(newScriptLsCmd(s, g, open))
	cmd.AddCommand(newScriptRunCmd(s, g, open))
	return cmd
}

func newScriptLsCmd(s Streams, g *globalFlags, open clientFactory) *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List the saved scripts, one per line: name and kind",
		Long: "List every saved script, one per line: its display name and its kind.\n\n" +
			"The source is deliberately not listed — a listing that printed whole modules\n" +
			"could not be one line per script, and `grpcview get | jq .workspace.scripts`\n" +
			"is the form that carries everything.\n\n" +
			"Scripts appear in the collection's own order; ls does not sort.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			snapshot, err := readWorkspace(cmd.Context(), g, open)
			if err != nil {
				return err
			}
			return renderScripts(s.Out, snapshot.GetWorkspace().GetScripts())
		},
	}
}

// renderScripts writes one line per script, the name column padded to the widest
// name so the same script list always produces the same columns.
func renderScripts(w io.Writer, scripts []*grpcviewv1.Script) error {
	var nameWidth int
	for _, script := range scripts {
		nameWidth = max(nameWidth, len(script.GetName()))
	}
	for _, script := range scripts {
		line := fmt.Sprintf("%-*s  %s", nameWidth, script.GetName(), scriptKindName(script.GetKind()))
		if err := writeLine(w, []byte(strings.TrimRight(line, " "))); err != nil {
			return err
		}
	}
	return nil
}

// The --kind values, which are also the words `script ls` prints: one vocabulary,
// so a name read out of a listing can be typed back into a flag.
const (
	kindGenerator  = "generator"
	kindMiddleware = "middleware"
	kindScenario   = "scenario"
)

func scriptKindName(kind grpcviewv1.ScriptKind) string {
	switch kind {
	case grpcviewv1.ScriptKind_SCRIPT_KIND_GENERATOR:
		return kindGenerator
	case grpcviewv1.ScriptKind_SCRIPT_KIND_MIDDLEWARE:
		return kindMiddleware
	case grpcviewv1.ScriptKind_SCRIPT_KIND_SCENARIO:
		return kindScenario
	default:
		// A kind a manifest edit can produce, or one a newer grpcview wrote. It is
		// listed rather than hidden.
		return "unspecified"
	}
}

// parseScriptKind maps the flag to the enum. An empty flag is "unset", which the
// engine reads as the scratchpad profile — not a fourth value to spell.
func parseScriptKind(flag string) (*grpcviewv1.ScriptKind, error) {
	switch flag {
	case "":
		return nil, nil
	case kindGenerator:
		return grpcviewv1.ScriptKind_SCRIPT_KIND_GENERATOR.Enum(), nil
	case kindMiddleware:
		return grpcviewv1.ScriptKind_SCRIPT_KIND_MIDDLEWARE.Enum(), nil
	case kindScenario:
		return grpcviewv1.ScriptKind_SCRIPT_KIND_SCENARIO.Enum(), nil
	default:
		return nil, fmt.Errorf("invalid --kind %q: want one of %s, %s, %s",
			flag, kindGenerator, kindMiddleware, kindScenario)
	}
}

func newScriptRunCmd(s Streams, g *globalFlags, open clientFactory) *cobra.Command {
	var kind string

	cmd := &cobra.Command{
		Use:   "run <name>|-",
		Short: "Run a saved script, or a script read from stdin",
		Long: "Evaluate a script through the scripting engine — a fresh isolated instance\n" +
			"with no capabilities granted and no workspace state touched.\n\n" +
			"The engine runs a SOURCE, not a name: it knows nothing about the collection.\n" +
			"So a <name> argument is resolved here, against the workspace snapshot — that\n" +
			"script's source and its own kind are what get sent — and an unknown name fails\n" +
			"before anything is evaluated. `-` reads the source from stdin instead, and\n" +
			"--kind selects the profile it runs under; unset evaluates the buffer as a\n" +
			"scratchpad and reports its last expression.\n\n" +
			"Unlike every other write verb this one produces data, so it prints: the script's\n" +
			"return value goes to stdout as one line of JSON (nothing at all when it returned\n" +
			"undefined), and its console.* output goes to stderr, prefixed with the level.\n\n" +
			"A script that throws exits 1 — it ran, and its outcome failed. Exit 2 means the\n" +
			"run never happened: an unknown name, or an engine that would not start.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runScriptRun(cmd.Context(), s, g, open, args[0], kind)
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "",
		"execution profile for a script read from stdin: generator|middleware|scenario; unset is the scratchpad")

	return cmd
}

const stdinScript = "-"

func runScriptRun(ctx context.Context, s Streams, g *globalFlags, open clientFactory, arg, kindFlag string) error {
	kind, err := parseScriptKind(kindFlag)
	if err != nil {
		return err
	}

	var stdinSource string
	if arg == stdinScript {
		raw, err := readBody(s, stdinScript)
		if err != nil {
			return err
		}
		if raw == nil {
			return errors.New("no script on stdin: `script run -` evaluates the source it reads there, and it read nothing")
		}
		stdinSource = string(raw)
	} else if kind != nil {
		// Silently ignoring the flag would be worse than refusing it: a saved
		// script carries the kind it was authored for, and running it under
		// another profile calls it by a different convention, which is not
		// "running that script" in any sense the caller meant.
		return fmt.Errorf(
			"--kind does not apply to the saved script %q: it carries its own kind, and the flag selects the profile for a script read from stdin",
			arg)
	}

	return withSession(ctx, g, open, func(ctx context.Context, sess session) error {
		source, runKind, label := stdinSource, kind, "the script on stdin"

		if arg != stdinScript {
			ws, err := workspaceSnapshot(ctx, sess, g)
			if err != nil {
				return err
			}
			script := scriptNamed(ws.GetScripts(), arg)
			if script == nil {
				// Returning here is the point: RunScript is never called, so a
				// typo'd name cannot evaluate anything at all.
				return fmt.Errorf(
					"unknown script %q: workspace %q has %d saved script(s), and `grpcview script ls` lists them",
					arg, g.Workspace, len(ws.GetScripts()))
			}
			source = script.GetSource()
			runKind = script.GetKind().Enum()
			label = fmt.Sprintf("the script %q", arg)
		}

		resp, err := sess.RunScript(ctx, connect.NewRequest(&grpcviewv1.RunScriptRequest{
			WorkspaceName: g.Workspace,
			Source:        source,
			Kind:          runKind,
		}))
		if err != nil {
			return fmt.Errorf("failed to run %s: %w", label, err)
		}
		return renderScriptRun(s, label, resp.Msg)
	})
}

func scriptNamed(scripts []*grpcviewv1.Script, name string) *grpcviewv1.Script {
	for _, script := range scripts {
		if script.GetName() == name {
			return script
		}
	}
	return nil
}

// renderScriptRun applies D8 and D9 to a completed run. It is the one write verb
// that prints, because it is the one that produces data: the returned value is the
// entire reason to run a script.
//
// The value is stdout, as the one line of JSON text the engine produced, so
// `script run x | jq` works. The captured console.* calls are stderr, prefixed
// with their level, because a log is a diagnostic no matter how interesting.
//
// A ScriptError is exit 1, not 2, and that is the same line D9 already draws
// everywhere else: a script that threw RAN, and its outcome failed — grpcview
// itself worked. That is structurally identical to a target returning a non-OK
// gRPC status, and the backend agrees, deliberately: a thrown exception comes back
// inside RunScriptResponse.error with a nil Connect error, while grpcview's own
// inability to run the engine is a Connect error. Exit 2 stays reserved for the
// run not happening at all.
func renderScriptRun(s Streams, label string, resp *grpcviewv1.RunScriptResponse) error {
	// Logs first, and whatever the outcome: they were emitted before the thing that
	// ended the run, and a console.log is often the only clue why it ended.
	for _, log := range resp.GetLogs() {
		fmt.Fprintf(s.Err, "%s: %s\n", log.GetLevel(), log.GetMessage())
	}

	if failure := resp.GetError(); failure != nil {
		// stdout stays EMPTY: a script that threw produced no value, and a caller
		// piping into jq must not receive one. The stack is dropped from the line
		// and the parsed line number kept — one line per failure is the contract a
		// script's error handling depends on, and `get`-style JSON is not on offer
		// here because a run leaves nothing behind to read.
		message := oneLine(failure.GetMessage())
		if line := failure.GetLine(); line > 0 {
			message = fmt.Sprintf("%s (line %d)", message, line)
		}
		return statusError{code: 1, err: fmt.Errorf("%s threw: %s", label, message)}
	}

	// An unset value is a script that returned undefined. Printing nothing is the
	// honest rendering: "null" would claim it returned the JSON null, which the
	// response distinguishes from undefined on purpose.
	if resp.Value == nil {
		return nil
	}
	return writeLine(s.Out, []byte(resp.GetValue()))
}
