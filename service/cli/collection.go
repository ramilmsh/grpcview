package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/store"
	"codeberg.org/ramilmsh/grpcview/service/wsroot"
)

func resolveCollection(ctx context.Context, sess session, g *globalFlags) (string, error) {
	if g.Collection != "" {
		return g.Collection, nil
	}
	if g.resolved != "" {
		return g.resolved, nil
	}

	listing, err := listCollections(ctx, sess)
	if err != nil {
		return "", err
	}
	id, err := collectionForCwd(listing)
	if err != nil {
		return "", err
	}
	g.resolved = id
	return id, nil
}

func listCollections(ctx context.Context, sess session) (*grpcviewv1.ListCollectionsResponse, error) {
	resp, err := sess.ListCollections(ctx, connect.NewRequest(&grpcviewv1.ListCollectionsRequest{}))
	if err != nil {
		return nil, fmt.Errorf("failed to list the workspace's collections: %w", err)
	}
	return resp.Msg, nil
}

func collectionForCwd(listing *grpcviewv1.ListCollectionsResponse) (string, error) {
	cwd, err := wsroot.InvocationDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve the current directory: %w", err)
	}
	if id, ok := nearestCollection(listing.GetRoot(), cwd); ok {
		return id, nil
	}

	root := listing.GetRoot()
	collections := listing.GetCollections()
	switch len(collections) {
	case 1:
		return collections[0].GetId(), nil
	case 0:
		return "", statusError{code: 2, err: fmt.Errorf(
			"the workspace %s holds no collection: `grpcview init` creates one", root)}
	}

	var candidates strings.Builder
	for _, summary := range collections {
		fmt.Fprintf(&candidates, "\n  %s", summary.GetId())
	}
	return "", statusError{code: 2, err: fmt.Errorf(
		"the workspace %s holds %d collections and the current directory is inside none of them; pass --collection with one of:%s",
		root, len(collections), candidates.String())}
}

func nearestCollection(root, cwd string) (string, bool) {
	if root == "" {
		return "", false
	}
	root, cwd = realPath(root), realPath(cwd)
	rel, err := filepath.Rel(root, cwd)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}

	dir := filepath.Join(root, rel)
	for {
		if _, err := os.Stat(filepath.Join(dir, store.CollectionFileName)); err == nil {
			id, err := filepath.Rel(root, dir)
			if err != nil {
				return "", false
			}
			return filepath.ToSlash(id), true
		}
		if dir == root {
			return "", false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func realPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

func newCollectionsCmd(s Streams, g *globalFlags, open clientFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "collections",
		Short: "Inspect the collections the workspace holds",
		Long: "A workspace is a repository, and one repository may hold several collections —\n" +
			"typically one per service directory. These verbs report what is in it.\n\n" +
			"There is no create here: `grpcview init` is the one verb that brings a\n" +
			"collection into being.",
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
	cmd.AddCommand(newCollectionsLsCmd(s, g, open))
	return cmd
}

func newCollectionsLsCmd(s Streams, g *globalFlags, open clientFactory) *cobra.Command {
	var output string

	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List the workspace's collections, marking the one this directory addresses",
		Long: "List every collection in the workspace, one per line: its id — the path\n" +
			"relative to the workspace root that every other verb's --collection takes — its\n" +
			"display name, and how many definition sources it has.\n\n" +
			"A leading `*` marks the collection THIS directory addresses, which is the whole\n" +
			"point of the listing: it is the id the other verbs resolve to here with no\n" +
			"--collection at all. Nothing is marked when the answer is ambiguous, which is\n" +
			"exactly when those verbs would refuse to guess.\n\n" +
			"A collection whose manifest cannot be read is still listed, carrying the reason:\n" +
			"one broken grpcview.json must not hide the rest of the repository. Every call\n" +
			"rescans the tree, so a collection someone just added is listed immediately.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCollectionsLs(cmd.Context(), s, g, open, output)
		},
	}

	cmd.Flags().StringVarP(&output, "output", "o", outputText, "output shape: text|json")

	return cmd
}

func runCollectionsLs(ctx context.Context, s Streams, g *globalFlags, open clientFactory, output string) error {
	switch output {
	case outputText, outputJSON:
	default:
		return fmt.Errorf("invalid --output %q: want one of %s, %s", output, outputText, outputJSON)
	}

	return withSession(ctx, g, open, func(ctx context.Context, sess session) error {
		listing, err := listCollections(ctx, sess)
		if err != nil {
			return err
		}

		if output == outputJSON {
			line, err := marshalOneLine(listing)
			if err != nil {
				return fmt.Errorf("failed to render the collection listing: %w", err)
			}
			return writeLine(s.Out, line)
		}

		current := g.Collection
		if current == "" {
			current, _ = collectionForCwd(listing)
		}
		return renderCollections(s.Out, listing.GetCollections(), current)
	})
}

type collectionRow struct {
	marker  string
	id      string
	name    string
	sources string
	status  string
}

func collectionRows(collections []*grpcviewv1.CollectionSummary, current string) []collectionRow {
	rows := make([]collectionRow, 0, len(collections))
	for _, summary := range collections {
		marker := " "
		if summary.GetId() == current {
			marker = "*"
		}
		rows = append(rows, collectionRow{
			marker:  marker,
			id:      summary.GetId(),
			name:    summary.GetName(),
			sources: sourceCount(summary.GetSourceCount()),
			status:  collectionStatus(summary),
		})
	}
	return rows
}

func sourceCount(n int32) string {
	if n == 1 {
		return "1 source"
	}
	return fmt.Sprintf("%d sources", n)
}

func collectionStatus(summary *grpcviewv1.CollectionSummary) string {
	if err := summary.GetError(); err != "" {
		return "error: " + oneLine(err)
	}
	return ""
}

func renderCollections(w io.Writer, collections []*grpcviewv1.CollectionSummary, current string) error {
	rows := collectionRows(collections, current)

	var idWidth, nameWidth, sourcesWidth int
	for _, row := range rows {
		idWidth = max(idWidth, len(row.id))
		nameWidth = max(nameWidth, len(row.name))
		sourcesWidth = max(sourcesWidth, len(row.sources))
	}

	for _, row := range rows {
		line := fmt.Sprintf("%s %-*s  %-*s  %-*s  %s",
			row.marker,
			idWidth, row.id,
			nameWidth, row.name,
			sourcesWidth, row.sources,
			row.status)
		if err := writeLine(w, []byte(strings.TrimRight(line, " "))); err != nil {
			return err
		}
	}
	return nil
}
