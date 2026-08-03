package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
)

// ls's -o shapes (D8). outputJSON is invoke's constant, reused deliberately: the
// two verbs accept disjoint SETS of values, but "json" means the same thing in
// both, and one spelling cannot drift from the other.
const outputText = "text"

func newLsCmd(s Streams, g *globalFlags, open clientFactory) *cobra.Command {
	var output string

	cmd := &cobra.Command{
		Use:   "ls [<folder-path>]",
		Short: "List the collection's folders and requests",
		Long: "List every folder and request in the collection, one per line, with the\n" +
			"method each request calls. Given a folder path, lists that subtree instead.\n\n" +
			"The listing is flat and fully qualified, so every path it prints is exactly\n" +
			"a path `grpcview invoke` accepts — the output is meant to be grepped and\n" +
			"pasted. Items appear in the collection's own order, which the tree treats as\n" +
			"meaningful; ls does not sort.",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			var path string
			if len(args) == 1 {
				path = args[0]
			}
			return runLs(cmd.Context(), s, g, open, output, path)
		},
	}

	// -o is registered here, per verb, with ls's own two values — never as a
	// persistent flag (D8).
	cmd.Flags().StringVarP(&output, "output", "o", outputText, "output shape: text|json")

	return cmd
}

func runLs(ctx context.Context, s Streams, g *globalFlags, open clientFactory, output, path string) error {
	// The -o check runs before the RPC: a typo'd flag must not read a workspace.
	switch output {
	case outputText, outputJSON:
	default:
		return fmt.Errorf("invalid --output %q: want one of %s, %s", output, outputText, outputJSON)
	}

	snapshot, err := readWorkspace(ctx, g, open)
	if err != nil {
		return err
	}
	ws := snapshot.GetWorkspace()

	root, prefix, err := lsRoot(ws, path)
	if err != nil {
		return err
	}

	if output == outputJSON {
		// The Item as the wire carries it, not a shape this verb invents: a script
		// that reads `ls -o json` and one that reads the RPC see the same fields.
		line, err := marshalOneLine(root)
		if err != nil {
			return fmt.Errorf("failed to render workspace %q: %w", g.Workspace, err)
		}
		return writeLine(s.Out, line)
	}

	return renderLs(s.Out, lsRows(root.GetFolder(), prefix))
}

// lsRoot picks the item ls lists — the collection root, or the folder the
// argument names — and the prefix every printed path is joined under. The prefix
// is what keeps a subtree listing pasteable: `ls Auth` prints "Auth/Login", not
// "Login".
//
// A trailing slash is accepted because ls itself prints folders with one, and
// pasting a line of ls output back into ls has to work.
func lsRoot(ws *grpcviewv1.Workspace, path string) (*grpcviewv1.Item, string, error) {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		// The workspace's root Item IS the collection, so no argument lists it whole
		// with no prefix at all.
		return ws.GetItem(), "", nil
	}

	found := lookupSaved(ws, trimmed)
	switch {
	case found.item.GetFolder() != nil:
		return found.item, trimmed + "/", nil
	case found.item.GetRequest() != nil:
		return nil, "", fmt.Errorf(
			"cannot list %q: it is a request, not a folder; list the folder holding it, or run it with `grpcview invoke %s`",
			path, trimmed)
	default:
		return nil, "", fmt.Errorf(
			"unknown folder %q: no folder at that path in workspace %q", path, ws.GetName())
	}
}

// lsRow is one line of the text listing, flattened out of the tree: the
// pasteable path, what the item is, and the middleware note.
type lsRow struct {
	// path carries a trailing slash for a folder. That is the only marker folders
	// get — no colors, no glyphs (D8) — and it doubles as the prefix a reader
	// appends a child name to.
	path string
	// what is "folder" for a folder, and the <service>/<method> the request calls
	// otherwise, spelled exactly as invoke's ad-hoc form takes it.
	what string
	// note is "[N middleware]", empty when the request has none. A request whose
	// body is rewritten by middleware behaves differently from what its stored
	// body says, so the count is worth a column.
	note string
}

// lsRows flattens a folder into its listing, depth first, in stored order. The
// recursion is the point: a flat, fully-qualified listing is what makes the
// output pasteable, and an indented tree would not be.
func lsRows(folder *grpcviewv1.Folder, prefix string) []lsRow {
	var rows []lsRow
	for _, item := range folder.GetItems() {
		path := prefix + item.GetName()
		switch {
		case item.GetFolder() != nil:
			rows = append(rows, lsRow{path: path + "/", what: "folder"})
			rows = append(rows, lsRows(item.GetFolder(), path+"/")...)
		case item.GetRequest() != nil:
			rows = append(rows, lsRow{
				path: path,
				what: requestMethod(item.GetRequest()),
				note: middlewareNote(item.GetRequest()),
			})
		}
	}
	return rows
}

// requestMethod is the method a request calls, spelled as invoke spells it. A
// request with no method picked yet prints "-" rather than a bare slash: an
// incomplete request is a real state, and it still has to list.
func requestMethod(r *grpcviewv1.Request) string {
	if r.GetService() == "" || r.GetMethod() == "" {
		return "-"
	}
	return r.GetService() + "/" + r.GetMethod()
}

func middlewareNote(r *grpcviewv1.Request) string {
	n := len(r.GetMiddleware())
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("[%d middleware]", n)
}

// renderLs writes the aligned listing. Both column widths are computed from the
// widest entry in the WHOLE listing rather than per row, so the same collection
// always produces the same columns and a diff of two listings shows only what
// changed.
func renderLs(w io.Writer, rows []lsRow) error {
	pathWidth, whatWidth := 0, 0
	for _, row := range rows {
		pathWidth = max(pathWidth, len(row.path))
		whatWidth = max(whatWidth, len(row.what))
	}

	for _, row := range rows {
		line := fmt.Sprintf("%-*s  %-*s  %s", pathWidth, row.path, whatWidth, row.what, row.note)
		// A row without a note must not ship the padding that would have preceded
		// one: trailing whitespace breaks a golden diff and a naive line compare.
		if err := writeLine(w, []byte(strings.TrimRight(line, " "))); err != nil {
			return err
		}
	}
	return nil
}
