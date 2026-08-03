package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

// newSourcesCmd builds the `sources` parent: the listing and the four mutations,
// which all address a source by the id `sources ls` prints.
func newSourcesCmd(s Streams, g *globalFlags, open clientFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sources",
		Short: "Inspect and edit the workspace's definition sources",
		Long: "A workspace's services and descriptors are derived from a priority-ordered\n" +
			"list of definition sources — reflection targets and uploaded descriptor sets.\n" +
			"These verbs show that list and change it.\n\n" +
			"Order is precedence and only order: the outcome is a pure function of the\n" +
			"list, never of which source was added or refreshed last. So `add` appends at\n" +
			"lowest priority and `reorder` is the switch that changes who wins.",
		// ArbitraryArgs, not NoArgs: cobra dispatches a known subcommand before RunE
		// ever runs, so anything that reaches RunE is either a typo'd subcommand or
		// no subcommand at all, and both are exit 2 with usage on stderr. Without an
		// explicit RunE, cobra would print help on STDOUT and exit 0 (D8).
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return unknownCommand(cmd, args[0])
			}
			return missingSubcommand(cmd, "ls", "add", "refresh", "rm", "reorder")
		},
	}
	cmd.AddCommand(newSourcesLsCmd(s, g, open))
	cmd.AddCommand(newSourcesAddCmd(g, open))
	cmd.AddCommand(newSourcesRefreshCmd(g, open))
	cmd.AddCommand(newSourcesRmCmd(g, open))
	cmd.AddCommand(newSourcesReorderCmd(g, open))
	return cmd
}

// missingSubcommand is unknownCommand for a parent command invoked bare: usage
// on stderr, never stdout, and exit 2.
func missingSubcommand(cmd *cobra.Command, subcommands ...string) error {
	out := cmd.OutOrStdout()
	cmd.SetOut(cmd.ErrOrStderr())
	defer cmd.SetOut(out)
	_ = cmd.Usage()
	return statusError{code: 2, err: fmt.Errorf(
		"%q needs a subcommand: %s", cmd.CommandPath(), strings.Join(subcommands, ", "))}
}

func newSourcesLsCmd(s Streams, g *globalFlags, open clientFactory) *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List the definition sources in priority order",
		Long: "List every definition source in PRIORITY order, one per line: priority, id,\n" +
			"kind, resolved file count, how many services the source serves, how many it\n" +
			"won, and its status.\n\n" +
			"Serving and winning are different numbers, and the difference is the point.\n" +
			"Walking the list front to back, the first source to define a proto file or\n" +
			"serve a service wins it, so a source can serve five services and win none\n" +
			"because a higher-priority source describes the same protos — that source is\n" +
			"shadowed, not idle, and reordering the list is what changes it.\n\n" +
			"A source whose last resolve failed stays listed with the reason and\n" +
			"contributes nothing. That is a normal state, not a failure of this command.\n\n" +
			"There is no -o: `grpcview get | jq .workspace.sources` is the JSON form, and\n" +
			"it is the same bytes the UI's Sources view reads.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSourcesLs(cmd.Context(), s, g, open)
		},
	}
}

func runSourcesLs(ctx context.Context, s Streams, g *globalFlags, open clientFactory) error {
	snapshot, err := readWorkspace(ctx, g, open)
	if err != nil {
		return err
	}
	// Workspace.sources is already in priority order — the order IS the precedence
	// — so this verb must never sort it.
	return renderSources(s.Out, snapshot.GetWorkspace().GetSources())
}

// The four mutations. Every one of them follows write.go's contract: silence is
// success, and a failure is one stderr line with exit 2.

func newSourcesAddCmd(g *globalFlags, open clientFactory) *cobra.Command {
	var tls bool

	cmd := &cobra.Command{
		Use:   "add <host:port>|<file.binpb>",
		Short: "Add a reflection target or a descriptor-set file as a definition source",
		Long: "Add one definition source at LOWEST priority, so adding never moves an\n" +
			"existing service to a different source — `sources reorder` is what does that.\n\n" +
			"The argument decides the kind, and the test is the filesystem: an argument that\n" +
			"stats as a file is uploaded as a FileDescriptorSet, and anything else is dialed\n" +
			"as a reflection target. So a relative path only works from the directory that\n" +
			"holds the file, which is the same rule every other tool's file argument follows.\n\n" +
			"A file's BASENAME is the upload's identity, not its path: re-adding a rebuilt\n" +
			"image of the same name REFRESHES that source in place at its existing priority\n" +
			"instead of adding a second, indistinguishable row. A reflection source is\n" +
			"identified by its address (and whether it is dialed with --tls) the same way.\n\n" +
			"Adding resolves the new source immediately — it is the one operation here that\n" +
			"touches the network — so a target that cannot be dialed, or bytes that do not\n" +
			"parse, FAIL the add and nothing is added. That is deliberate: a listed source\n" +
			"that silently provides nothing is worse than an error. A source that resolved\n" +
			"once and whose target later goes away is the different case — it stays listed\n" +
			"with the reason, contributing nothing, and `sources ls` shows it.\n\n" +
			"Nothing is printed on success.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSourcesAdd(cmd.Context(), g, open, args[0], tls)
		},
	}
	cmd.Flags().BoolVar(&tls, "tls", false,
		"dial the reflection target over TLS; it is part of the source's identity, so the same address with and without it are two sources")

	return cmd
}

func runSourcesAdd(ctx context.Context, g *globalFlags, open clientFactory, arg string, tls bool) error {
	msg, err := buildAddSource(g.Workspace, arg, tls)
	if err != nil {
		return err
	}
	return withSession(ctx, g, open, func(ctx context.Context, sess session) error {
		if _, err := sess.AddDescriptorSource(ctx, connect.NewRequest(msg)); err != nil {
			return fmt.Errorf("failed to add the definition source %q: %w", arg, err)
		}
		return nil
	})
}

// buildAddSource discriminates `sources add`'s two forms, which is the whole of
// the verb's logic and therefore the whole of what its test needs to reach.
//
// The discriminator is os.Stat, not a host:port pattern match. A pattern would
// have to decide what "localhost", "example.com:443" and "./echo.binpb" are by
// guessing at their shape; the filesystem answers the only question that matters
// without guessing, and an argument that IS a file is never a plausible dial
// address.
func buildAddSource(workspaceName, arg string, tls bool) (*grpcviewv1.AddDescriptorSourceRequest, error) {
	if arg == "" {
		return nil, errors.New("no definition source given: `sources add` takes a reflection address or the path of a descriptor-set file")
	}

	info, statErr := os.Stat(arg)
	switch {
	case statErr == nil && info.IsDir():
		return nil, fmt.Errorf(
			"cannot add %q as a definition source: it is a directory, and an upload is one FileDescriptorSet file (`buf build -o image.binpb`)", arg)

	case statErr == nil:
		if tls {
			return nil, fmt.Errorf(
				"--tls does not apply to %q: it names a file, and TLS selects how a reflection TARGET is dialed", arg)
		}
		raw, err := os.ReadFile(arg)
		if err != nil {
			return nil, fmt.Errorf("failed to read the descriptor set: %w", err)
		}
		return &grpcviewv1.AddDescriptorSourceRequest{
			WorkspaceName: workspaceName,
			Source:        &grpcviewv1.AddDescriptorSourceRequest_DescriptorSet{DescriptorSet: raw},
			// The BASENAME, deliberately: file_name is the source's identity, so the
			// same image rebuilt into a different directory has to be the same source.
			FileName: filepath.Base(arg),
		}, nil

	default:
		server := &grpcviewv1.Server{Address: arg}
		if tls {
			// Server.TLS is an empty message, so the flag is one bool mapped by hand
			// — the same mapping invoke's buildTarget does.
			server.Tls = &grpcviewv1.Server_TLS{}
		}
		return &grpcviewv1.AddDescriptorSourceRequest{
			WorkspaceName: workspaceName,
			Source:        &grpcviewv1.AddDescriptorSourceRequest_Reflection{Reflection: server},
		}, nil
	}
}

func newSourcesRefreshCmd(g *globalFlags, open clientFactory) *cobra.Command {
	return &cobra.Command{
		Use:   "refresh [<id>]",
		Short: "Re-resolve one definition source, or every source in priority order",
		Long: "Re-resolve a definition source: re-dial a reflection target to pick up a\n" +
			"redeployed schema, or re-link an upload's committed bytes.\n\n" +
			"Given no id, every source is refreshed in PRIORITY order — the order\n" +
			"`sources ls` prints, which is also the order the merge walks — one RPC per\n" +
			"source, since the RPC refreshes exactly one. The run stops at the first\n" +
			"failure and reports it, so a later source is never quietly skipped.\n\n" +
			"An unreachable target fails this command, unlike the passive listing: you\n" +
			"named the source and asked for it to resolve.\n\n" +
			"Nothing is printed on success.",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			var id string
			if len(args) == 1 {
				id = args[0]
			}
			return runSourcesRefresh(cmd.Context(), g, open, id)
		},
	}
}

func runSourcesRefresh(ctx context.Context, g *globalFlags, open clientFactory, id string) error {
	return withSession(ctx, g, open, func(ctx context.Context, sess session) error {
		ids := []string{id}
		if id == "" {
			// Read the list rather than inventing an order: Workspace.sources IS the
			// priority order, and refreshing in it means a caller watching the
			// merged view see the same intermediate states the UI would.
			ws, err := workspaceSnapshot(ctx, sess, g)
			if err != nil {
				return err
			}
			ids = make([]string, 0, len(ws.GetSources()))
			for _, src := range ws.GetSources() {
				ids = append(ids, src.GetId())
			}
		}

		for _, each := range ids {
			_, err := sess.RefreshDescriptorSource(ctx, connect.NewRequest(&grpcviewv1.RefreshDescriptorSourceRequest{
				WorkspaceName: g.Workspace,
				Id:            each,
			}))
			if err != nil {
				return fmt.Errorf("failed to refresh the definition source %q: %w", each, err)
			}
		}
		return nil
	})
}

func newSourcesRmCmd(g *globalFlags, open clientFactory) *cobra.Command {
	return &cobra.Command{
		Use:   "rm <id>",
		Short: "Remove a definition source",
		Long: "Remove the definition source with this id — `sources ls` prints the ids.\n\n" +
			"The sources that remain keep their relative order and the merged view is\n" +
			"re-derived from their caches, so removing reaches no network and an\n" +
			"unreachable sibling cannot block it.\n\n" +
			"Nothing is printed on success.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSourcesRm(cmd.Context(), g, open, args[0])
		},
	}
}

func runSourcesRm(ctx context.Context, g *globalFlags, open clientFactory, id string) error {
	return withSession(ctx, g, open, func(ctx context.Context, sess session) error {
		_, err := sess.RemoveDescriptorSource(ctx, connect.NewRequest(&grpcviewv1.RemoveDescriptorSourceRequest{
			WorkspaceName: g.Workspace,
			Id:            id,
		}))
		if err != nil {
			return fmt.Errorf("failed to remove the definition source %q: %w", id, err)
		}
		return nil
	})
}

func newSourcesReorderCmd(g *globalFlags, open clientFactory) *cobra.Command {
	return &cobra.Command{
		Use:   "reorder <id>...",
		Short: "Set the full priority order of the definition sources",
		Long: "Set the priority order of the definition sources: earlier wins. This is the\n" +
			"switch that decides which source's definitions a service resolves from when\n" +
			"several describe the same protos — which is how a `buf build` upload's doc\n" +
			"comments beat a reflection target that cannot carry them.\n\n" +
			"The ids are passed through exactly as given, and must be the FULL set. A\n" +
			"missing or unknown id is rejected rather than applied as a partial reorder, so\n" +
			"a caller working from a stale listing can never silently drop a source.\n" +
			"`sources ls` prints the current order and the ids.\n\n" +
			"Nothing is printed on success.",
		Args:          cobra.MinimumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSourcesReorder(cmd.Context(), g, open, args)
		},
	}
}

func runSourcesReorder(ctx context.Context, g *globalFlags, open clientFactory, ids []string) error {
	return withSession(ctx, g, open, func(ctx context.Context, sess session) error {
		// The ids go through verbatim — no dedupe, no sort, no completion from the
		// snapshot. The RPC validates that they are a permutation of the current
		// list, and a CLI that "helpfully" completed a partial order would defeat
		// exactly the check that protects a stale caller.
		_, err := sess.ReorderDescriptorSources(ctx, connect.NewRequest(&grpcviewv1.ReorderDescriptorSourcesRequest{
			WorkspaceName: g.Workspace,
			Ids:           ids,
		}))
		if err != nil {
			return fmt.Errorf("failed to reorder the definition sources: %w", err)
		}
		return nil
	})
}

// sourceRow is one line of `sources ls`, every column already rendered as the
// string it prints. The numeric columns carry their own labels instead of relying
// on a header row: one self-describing line per source is what survives a grep.
type sourceRow struct {
	priority string
	id       string
	kind     string
	files    string
	serves   string
	wins     string
	status   string
}

func sourceRows(sources []*grpcviewv1.DescriptorSource) []sourceRow {
	rows := make([]sourceRow, 0, len(sources))
	for i, src := range sources {
		resolved := src.GetResolved()
		rows = append(rows, sourceRow{
			priority: strconv.Itoa(i + 1),
			id:       src.GetId(),
			kind:     sourceKind(src),
			files:    fileCount(resolved.GetFileCount()),
			serves:   fmt.Sprintf("serves %d", len(resolved.GetServiceNames())),
			wins:     fmt.Sprintf("wins %d", len(resolved.GetWonServiceNames())),
			status:   sourceStatus(resolved),
		})
	}
	return rows
}

// sourceKind reads the oneof through its getters rather than a type switch: an
// unset oneof is a source a manifest edit can produce, and "unknown" listing is
// better than a row that omits its kind.
func sourceKind(src *grpcviewv1.DescriptorSource) string {
	switch {
	case src.GetReflection() != nil:
		return "reflection"
	case src.GetUpload() != nil:
		return "upload"
	default:
		return "unknown"
	}
}

// sourceStatus is the load-bearing column, and the reason this verb is not a
// `jq` one-liner over `get`.
//
// A source that serves services and wins none is SHADOWED: it resolved fine, and
// higher-priority sources simply describe the same protos. A source that serves
// nothing at all is empty. Printing only the won count would render the two
// identically, which is exactly what the UI's Sources view does not do. A partial
// shadow gets its count, since "won 1 of 3" is the state that explains why one
// request shows doc comments and its neighbour does not.
func sourceStatus(resolved *grpcviewv1.Resolved) string {
	if err := resolved.GetError(); err != "" {
		return "error: " + oneLine(err)
	}
	served, won := len(resolved.GetServiceNames()), len(resolved.GetWonServiceNames())
	switch {
	case served == 0:
		return "no services"
	case won == 0:
		return "shadowed"
	case won < served:
		return fmt.Sprintf("%d shadowed", served-won)
	default:
		return ""
	}
}

func fileCount(n int32) string {
	if n == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", n)
}

// oneLine collapses whitespace runs. A resolve error is often multi-line — a
// failed link check reports one line per conflicting file — and one line per
// source is the contract a grep depends on.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// renderSources writes the aligned listing, every column width computed from the
// widest entry so the same source list always produces the same columns.
func renderSources(w io.Writer, sources []*grpcviewv1.DescriptorSource) error {
	rows := sourceRows(sources)

	var priorityWidth, idWidth, kindWidth, filesWidth, servesWidth, winsWidth int
	for _, row := range rows {
		priorityWidth = max(priorityWidth, len(row.priority))
		idWidth = max(idWidth, len(row.id))
		kindWidth = max(kindWidth, len(row.kind))
		filesWidth = max(filesWidth, len(row.files))
		servesWidth = max(servesWidth, len(row.serves))
		winsWidth = max(winsWidth, len(row.wins))
	}

	for _, row := range rows {
		line := fmt.Sprintf("%-*s  %-*s  %-*s  %-*s  %-*s  %-*s  %s",
			priorityWidth, row.priority,
			idWidth, row.id,
			kindWidth, row.kind,
			filesWidth, row.files,
			servesWidth, row.serves,
			winsWidth, row.wins,
			row.status)
		if err := writeLine(w, []byte(strings.TrimRight(line, " "))); err != nil {
			return err
		}
	}
	return nil
}
