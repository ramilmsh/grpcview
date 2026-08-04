package cli

// grpcview init is the one verb that brings a collection into being. Every other RPC
// requires one to already exist: with a collection addressed by its path inside the
// user's repo, a handler that created one on demand would let a typo'd --collection or a
// stale query scatter grpcview.json among project files.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"

	grpcviewv1 "codeberg.org/ramilmsh/grpcview/proto/grpcview/v1"
	"codeberg.org/ramilmsh/grpcview/service/wsroot"
)

func newInitCmd(s Streams, g *globalFlags, open clientFactory) *cobra.Command {
	var name string

	cmd := &cobra.Command{
		Use:   "init [dir]",
		Short: "Create a collection",
		Long: "Create a new, empty collection at a workspace-relative directory — \".\" is\n" +
			"the workspace root itself.\n\n" +
			"With no argument, dir defaults to the CURRENT directory's own path relative to\n" +
			"the workspace root: `cd services/payments/requests && grpcview init` creates\n" +
			"the collection there, and `cd ~/api-requests && grpcview init` (nothing above\n" +
			"it but the repo root) makes the whole repo the collection.\n\n" +
			"Unlike every other verb, a collection that already exists at that address is\n" +
			"an error, not a silent no-op or a silent reuse: this is the one place a\n" +
			"collection is meant to come into being.",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			var dir string
			if len(args) > 0 {
				dir = args[0]
			}
			return runInit(cmd.Context(), s, g, open, dir, name)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "display name; empty defaults to the directory's own base name")
	return cmd
}

func runInit(ctx context.Context, s Streams, g *globalFlags, open clientFactory, dir, name string) error {
	collection, err := resolveInitCollection(g, dir)
	if err != nil {
		return err
	}

	var created *grpcviewv1.Collection
	err = withSession(ctx, g, open, func(ctx context.Context, sess session) error {
		resp, err := sess.CreateCollection(ctx, connect.NewRequest(&grpcviewv1.CreateCollectionRequest{
			Collection: collection,
			Name:       name,
		}))
		if err != nil {
			return fmt.Errorf("failed to create the collection %q: %w", collection, err)
		}
		created = resp.Msg.GetCollection()
		return nil
	})
	if err != nil {
		return err
	}
	return writeLine(s.Out, []byte(fmt.Sprintf("%s  %s", collection, created.GetName())))
}

// resolveInitCollection is dir verbatim when given, or else the current directory's own
// path relative to the discovered workspace root. The warning wsroot.Discover can return
// is deliberately dropped here: openClient's own Discover call (opening the session that
// follows) surfaces it, and printing it twice for one command would be noise.
func resolveInitCollection(g *globalFlags, dir string) (string, error) {
	if dir != "" {
		return dir, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to resolve the current directory: %w", err)
	}
	root, _, err := wsroot.Discover(g.Workspace, cwd)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, cwd)
	if err != nil {
		return "", fmt.Errorf("failed to resolve %q relative to the workspace %q: %w", cwd, root, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("the current directory %q is outside the workspace %q", cwd, root)
	}
	return rel, nil
}
