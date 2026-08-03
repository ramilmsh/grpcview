package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

// newSourcesCmd builds the `sources` parent. It has one subcommand today; the
// mutating ones (add, rm, refresh, reorder) hang off the same parent later.
func newSourcesCmd(s Streams, g *globalFlags, open clientFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sources",
		Short: "Inspect the workspace's definition sources",
		Long: "A workspace's services and descriptors are derived from a priority-ordered\n" +
			"list of definition sources — reflection targets and uploaded descriptor sets.\n" +
			"These verbs show that list.",
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
			return missingSubcommand(cmd, "ls")
		},
	}
	cmd.AddCommand(newSourcesLsCmd(s, g, open))
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
