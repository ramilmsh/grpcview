package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/grpcview/v1"
)

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

	cmd.Flags().StringVarP(&output, "output", "o", outputText, "output shape: text|json")

	return cmd
}

func runLs(ctx context.Context, s Streams, g *globalFlags, open clientFactory, output, path string) error {
	switch output {
	case outputText, outputJSON:
	default:
		return fmt.Errorf("invalid --output %q: want one of %s, %s", output, outputText, outputJSON)
	}

	return withCollection(ctx, g, open, func(ctx context.Context, sess session, collection string) error {
		ws, err := workspaceSnapshot(ctx, sess, collection)
		if err != nil {
			return err
		}

		root, prefix, err := lsRoot(ws, path)
		if err != nil {
			return err
		}

		if output == outputJSON {
			line, err := marshalOneLine(root)
			if err != nil {
				return fmt.Errorf("failed to render collection %q: %w", collection, err)
			}
			return writeLine(s.Out, line)
		}

		return renderLs(s.Out, lsRows(root.GetFolder(), prefix, streamingNotes(ws)))
	})
}

func lsRoot(ws *grpcviewv1.Collection, path string) (*grpcviewv1.Item, string, error) {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
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
			"unknown folder %q: no folder at that path in collection %q", path, ws.GetName())
	}
}

type lsRow struct {
	path string
	what string
	note string
}

func lsRows(folder *grpcviewv1.Folder, prefix string, streaming map[string]string) []lsRow {
	var rows []lsRow
	for _, item := range folder.GetItems() {
		path := prefix + item.GetName()
		switch {
		case item.GetFolder() != nil:
			rows = append(rows, lsRow{path: path + "/", what: "folder"})
			rows = append(rows, lsRows(item.GetFolder(), path+"/", streaming)...)
		case item.GetRequest() != nil:
			what := requestMethod(item.GetRequest())
			rows = append(rows, lsRow{
				path: path,
				what: what,
				note: joinNotes(streaming[what], middlewareNote(item.GetRequest())),
			})
		}
	}
	return rows
}

// A streaming request lists identically to a unary one otherwise. The label says only what
// the method is; why a call would fail belongs to describe.
func streamingNotes(ws *grpcviewv1.Collection) map[string]string {
	out := map[string]string{}
	for _, svc := range ws.GetServices() {
		full := svc.GetName()
		if pkg := svc.GetPackage(); pkg != "" {
			full = pkg + "." + full
		}
		for _, m := range svc.GetMethods() {
			if note := streamingNote(m.GetClientStreaming(), m.GetServerStreaming()); note != "" {
				out[full+"/"+m.GetName()] = note
			}
		}
	}
	return out
}

func streamingNote(client, server bool) string {
	switch {
	case client && server:
		return "[bidi-streaming]"
	case client:
		return "[client-streaming]"
	case server:
		return "[server-streaming]"
	}
	return ""
}

func joinNotes(notes ...string) string {
	var out []string
	for _, n := range notes {
		if n != "" {
			out = append(out, n)
		}
	}
	return strings.Join(out, " ")
}

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

func renderLs(w io.Writer, rows []lsRow) error {
	pathWidth, whatWidth := 0, 0
	for _, row := range rows {
		pathWidth = max(pathWidth, len(row.path))
		whatWidth = max(whatWidth, len(row.what))
	}

	for _, row := range rows {
		line := fmt.Sprintf("%-*s  %-*s  %s", pathWidth, row.path, whatWidth, row.what, row.note)
		if err := writeLine(w, []byte(strings.TrimRight(line, " "))); err != nil {
			return err
		}
	}
	return nil
}
