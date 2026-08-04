package cli

// The collection and script write verbs. A mutation that succeeds prints
// nothing on either stream and exits 0; `script run` is the one that prints.

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

// withSession opens one session for the whole verb; a verb making several calls must share one.
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

func workspaceSnapshot(ctx context.Context, sess session, g *globalFlags) (*grpcviewv1.Collection, error) {
	resp, err := sess.Get(ctx, connect.NewRequest(&grpcviewv1.GetRequest{Collection: g.Collection}))
	if err != nil {
		return nil, fmt.Errorf("failed to read collection %q: %w", g.Collection, err)
	}
	return resp.Msg.GetCollection(), nil
}

func splitItemPath(arg string) ([]string, string, error) {
	parent, name, err := workspace.SplitInvokePath(arg)
	if err != nil {
		return nil, "", fmt.Errorf(
			"invalid path %q: the last segment names the item and cannot be empty", arg)
	}
	return parent, name, nil
}

// splitFolderPath parses a destination folder path; "" and "/" are the collection root.
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
			Collection: g.Collection,
			Path:       parent,
			ItemName:   name,
		}))
		if err != nil {
			return fmt.Errorf("failed to create the folder %q: %w", arg, err)
		}
		return nil
	})
}

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
	_ = cmd.MarkFlagRequired("service")
	_ = cmd.MarkFlagRequired("method")

	return cmd
}

func runRequestCreate(ctx context.Context, s Streams, g *globalFlags, open clientFactory, f *requestCreateFlags, arg string) error {
	parent, name, err := splitItemPath(arg)
	if err != nil {
		return err
	}

	// Read the body first: an unreadable -f must not leave a bodyless request behind.
	raw, err := readBody(s, f.file)
	if err != nil {
		return err
	}

	return withSession(ctx, g, open, func(ctx context.Context, sess session) error {
		_, err := sess.CreateRequest(ctx, connect.NewRequest(&grpcviewv1.CreateRequestRequest{
			Collection: g.Collection,
			Path:       parent,
			ItemName:   name,
			Service:    f.service,
			Method:     f.method,
		}))
		if err != nil {
			return fmt.Errorf("failed to create the request %q: %w", arg, err)
		}
		if raw == nil {
			return nil
		}

		_, err = sess.UpdateRequest(ctx, connect.NewRequest(&grpcviewv1.UpdateRequestRequest{
			Collection: g.Collection,
			Path:       parent,
			ItemName:   name,
			DraftBody:  proto.String(string(raw)),
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
			Collection: g.Collection,
			Path:       parent,
			ItemName:   name,
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
			// Changed, not the empty string: unset means append.
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
			Collection: g.Collection,
			Path:       parent,
			ItemName:   name,
			NewPath:    newPath,
			Before:     before,
		}))
		if err != nil {
			return fmt.Errorf("failed to move %q: %w", arg, err)
		}
		return nil
	})
}

func newScriptCmd(s Streams, g *globalFlags, open clientFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "script",
		Short: "List the collection's scripts and run one",
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
			"could not be one line per script, and `grpcview get | jq .collection.scripts`\n" +
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
			return renderScripts(s.Out, snapshot.GetCollection().GetScripts())
		},
	}
}

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
		return "unspecified"
	}
}

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
			"with no capabilities granted and no collection state touched.\n\n" +
			"The engine runs a SOURCE, not a name: it knows nothing about the collection.\n" +
			"So a <name> argument is resolved here, against the collection snapshot — that\n" +
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
				return fmt.Errorf(
					"unknown script %q: collection %q has %d saved script(s), and `grpcview script ls` lists them",
					arg, g.Collection, len(ws.GetScripts()))
			}
			source = script.GetSource()
			runKind = script.GetKind().Enum()
			label = fmt.Sprintf("the script %q", arg)
		}

		resp, err := sess.RunScript(ctx, connect.NewRequest(&grpcviewv1.RunScriptRequest{
			Collection: g.Collection,
			Source:     source,
			Kind:       runKind,
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

// renderScriptRun writes the value on stdout and the logs on stderr; a script that threw is exit 1.
func renderScriptRun(s Streams, label string, resp *grpcviewv1.RunScriptResponse) error {
	// Logs first, whatever the outcome: they were emitted before the run ended.
	for _, log := range resp.GetLogs() {
		fmt.Fprintf(s.Err, "%s: %s\n", log.GetLevel(), log.GetMessage())
	}

	if failure := resp.GetError(); failure != nil {
		message := oneLine(failure.GetMessage())
		if line := failure.GetLine(); line > 0 {
			message = fmt.Sprintf("%s (line %d)", message, line)
		}
		return statusError{code: 1, err: fmt.Errorf("%s threw: %s", label, message)}
	}

	// An unset value is a script that returned undefined, not the JSON null.
	if resp.Value == nil {
		return nil
	}
	return writeLine(s.Out, []byte(resp.GetValue()))
}
