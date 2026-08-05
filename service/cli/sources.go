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

func newSourcesCmd(s Streams, g *globalFlags, open clientFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sources",
		Short: "Inspect and edit the collection's definition sources",
		Long: "A collection's services and descriptors are derived from a priority-ordered\n" +
			"list of definition sources — reflection targets and uploaded descriptor sets.\n" +
			"These verbs show that list and change it.\n\n" +
			"Order is precedence and only order: the outcome is a pure function of the\n" +
			"list, never of which source was added or refreshed last. So `add` appends at\n" +
			"lowest priority and `reorder` is the switch that changes who wins.\n\n" +
			"Where each source's resolved descriptors are STORED is a separate, per-source\n" +
			"choice: cached in local state by default, or committed to the repo as a\n" +
			"sidecar so a fresh clone resolves with no network — `sources commit`.",
		// ArbitraryArgs plus an explicit RunE: without one, cobra prints help on stdout and exits 0.
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return unknownCommand(cmd, args[0])
			}
			return missingSubcommand(cmd, "ls", "add", "commit", "refresh", "rm", "reorder")
		},
	}
	cmd.AddCommand(newSourcesLsCmd(s, g, open))
	cmd.AddCommand(newSourcesAddCmd(g, open))
	cmd.AddCommand(newSourcesCommitCmd(g, open))
	cmd.AddCommand(newSourcesRefreshCmd(g, open))
	cmd.AddCommand(newSourcesRmCmd(g, open))
	cmd.AddCommand(newSourcesReorderCmd(g, open))
	return cmd
}

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
			"kind, whether its config is this collection's own or a definition shared by\n" +
			"the whole workspace, whether its descriptors are committed to the repo or\n" +
			"cached in local state, resolved file count, how many services the source\n" +
			"serves, how many it won, and its status.\n\n" +
			"A `workspace` source is defined once in grpcview.work.json and referenced from\n" +
			"this collection's list by id. Its priority here, its removal from here and\n" +
			"where its descriptors are stored are all still this collection's own choices;\n" +
			"its address is not, and is edited in that manifest.\n\n" +
			"Serving and winning are different numbers, and the difference is the point.\n" +
			"Walking the list front to back, the first source to define a proto file or\n" +
			"serve a service wins it, so a source can serve five services and win none\n" +
			"because a higher-priority source describes the same protos — that source is\n" +
			"shadowed, not idle, and reordering the list is what changes it.\n\n" +
			"A source whose last resolve failed stays listed with the reason and\n" +
			"contributes nothing. That is a normal state, not a failure of this command.\n\n" +
			"There is no -o: `grpcview get | jq .collection.sources` is the JSON form, and\n" +
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
	return withCollection(ctx, g, open, func(ctx context.Context, sess session, collection string) error {
		ws, err := workspaceSnapshot(ctx, sess, collection)
		if err != nil {
			return err
		}
		// Collection.sources is already in priority order; never sort it.
		return renderSources(s.Out, ws.GetSources())
	})
}

func newSourcesAddCmd(g *globalFlags, open clientFactory) *cobra.Command {
	var tls, commit bool

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
			return runSourcesAdd(cmd.Context(), g, open, args[0], tls, commit)
		},
	}
	cmd.Flags().BoolVar(&tls, "tls", false,
		"dial the reflection target over TLS; it is part of the source's identity, so the same address with and without it are two sources")
	cmd.Flags().BoolVar(&commit, "commit-descriptors", false,
		"commit what this source resolves to, as a protojson sidecar in the collection, instead of caching it in local state; `sources commit` toggles it afterwards")

	return cmd
}

func runSourcesAdd(ctx context.Context, g *globalFlags, open clientFactory, arg string, tls, commit bool) error {
	// Built before any session opens, so a bad argument — or an unreadable file — fails
	// without touching the workspace; only the address it carries is filled in later.
	msg, err := buildAddSource(arg, tls, commit)
	if err != nil {
		return err
	}
	return withCollection(ctx, g, open, func(ctx context.Context, sess session, collection string) error {
		msg.Collection = collection
		if _, err := sess.AddDescriptorSource(ctx, connect.NewRequest(msg)); err != nil {
			return fmt.Errorf("failed to add the definition source %q: %w", arg, err)
		}
		return nil
	})
}

func buildAddSource(arg string, tls, commit bool) (*grpcviewv1.AddDescriptorSourceRequest, error) {
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
			Source: &grpcviewv1.AddDescriptorSourceRequest_DescriptorSet{DescriptorSet: raw},
			// The basename, deliberately: file_name is the source's identity.
			FileName:          filepath.Base(arg),
			CommitDescriptors: commit,
		}, nil

	default:
		server := &grpcviewv1.Server{Address: arg}
		if tls {
			server.Tls = &grpcviewv1.Server_TLS{}
		}
		return &grpcviewv1.AddDescriptorSourceRequest{
			Source:            &grpcviewv1.AddDescriptorSourceRequest_Reflection{Reflection: server},
			CommitDescriptors: commit,
		}, nil
	}
}

func newSourcesCommitCmd(g *globalFlags, open clientFactory) *cobra.Command {
	var off bool

	cmd := &cobra.Command{
		Use:   "commit <id>",
		Short: "Commit a definition source's descriptors to the repo, or stop committing them",
		Long: "Commit what this source resolved to as a protojson sidecar inside the\n" +
			"collection — descriptors/<slug>-<hash of the id>.json — instead of caching it in\n" +
			"the workspace's local state. `--off` moves it back to the local cache and\n" +
			"deletes the sidecar. `sources ls` shows which sources are committed.\n\n" +
			"This changes only WHERE the descriptors are stored. It never dials and never\n" +
			"builds: committing writes the bytes the store already holds, so a source that\n" +
			"has never resolved is refused — refresh it first — rather than resolved as a\n" +
			"side effect of a config change.\n\n" +
			"The point of committing is a fresh clone that resolves with no local state and\n" +
			"no network: a colleague who checks the repo out can describe and invoke\n" +
			"immediately. The cost is a large file in git history, so it is a per-source\n" +
			"choice. An UPLOAD is the case where it matters most — it has no address to\n" +
			"re-fetch from, so an uncommitted upload's only copy is in local state, and a\n" +
			"clone has no schema for it until someone uploads the file again.\n\n" +
			"Nothing is printed on success.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSourcesCommit(cmd.Context(), g, open, args[0], !off)
		},
	}
	cmd.Flags().BoolVar(&off, "off", false,
		"stop committing this source's descriptors: delete the sidecar and keep the bytes in the local cache")

	return cmd
}

func runSourcesCommit(ctx context.Context, g *globalFlags, open clientFactory, id string, commit bool) error {
	return withCollection(ctx, g, open, func(ctx context.Context, sess session, collection string) error {
		_, err := sess.SetDescriptorSourceCommit(ctx, connect.NewRequest(&grpcviewv1.SetDescriptorSourceCommitRequest{
			Collection: collection,
			Id:         id,
			Commit:     commit,
		}))
		if err != nil {
			verb := "commit"
			if !commit {
				verb = "stop committing"
			}
			return fmt.Errorf("failed to %s the descriptors of the definition source %q: %w", verb, id, err)
		}
		return nil
	})
}

func newSourcesRefreshCmd(g *globalFlags, open clientFactory) *cobra.Command {
	return &cobra.Command{
		Use:   "refresh [<id>]",
		Short: "Re-resolve one definition source, or every source in priority order",
		Long: "Re-resolve a definition source from its pointer: re-dial a reflection target to\n" +
			"pick up a redeployed schema.\n\n" +
			"An UPLOAD cannot be refreshed. It has no address or path to re-read — the file\n" +
			"name is only its identity — so the way to update one is `sources add` with the\n" +
			"rebuilt file, which refreshes that source in place. Asking to refresh one fails\n" +
			"rather than reporting a refresh that fetched nothing.\n\n" +
			"Given no id, every REFRESHABLE source is refreshed in PRIORITY order — the order\n" +
			"`sources ls` prints, which is also the order the merge walks — one RPC per\n" +
			"source, since the RPC refreshes exactly one. Uploads are skipped there, because\n" +
			"'refresh everything that can be re-fetched' is the useful reading of a bare\n" +
			"refresh; naming an upload's id is the request that fails. The run stops at the\n" +
			"first failure and reports it, so a later source is never quietly skipped.\n\n" +
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
	return withCollection(ctx, g, open, func(ctx context.Context, sess session, collection string) error {
		ids := []string{id}
		if id == "" {
			ws, err := workspaceSnapshot(ctx, sess, collection)
			if err != nil {
				return err
			}
			ids = make([]string, 0, len(ws.GetSources()))
			for _, src := range ws.GetSources() {
				// An upload has no pointer to re-read, so a bare refresh passes over it instead
				// of failing the whole run on a source nothing could have re-fetched.
				if src.GetUpload() != nil {
					continue
				}
				ids = append(ids, src.GetId())
			}
		}

		for _, each := range ids {
			_, err := sess.RefreshDescriptorSource(ctx, connect.NewRequest(&grpcviewv1.RefreshDescriptorSourceRequest{
				Collection: collection,
				Id:         each,
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
			"Removing a `workspace` source removes THIS collection's reference to it and\n" +
			"never the shared definition in grpcview.work.json: every other collection\n" +
			"referencing it is untouched, and re-adding it here restores the reference.\n\n" +
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
	return withCollection(ctx, g, open, func(ctx context.Context, sess session, collection string) error {
		_, err := sess.RemoveDescriptorSource(ctx, connect.NewRequest(&grpcviewv1.RemoveDescriptorSourceRequest{
			Collection: collection,
			Id:         id,
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
	return withCollection(ctx, g, open, func(ctx context.Context, sess session, collection string) error {
		// Verbatim: the RPC's permutation check is what protects a stale caller.
		_, err := sess.ReorderDescriptorSources(ctx, connect.NewRequest(&grpcviewv1.ReorderDescriptorSourcesRequest{
			Collection: collection,
			Ids:        ids,
		}))
		if err != nil {
			return fmt.Errorf("failed to reorder the definition sources: %w", err)
		}
		return nil
	})
}

type sourceRow struct {
	priority string
	id       string
	kind     string
	origin   string
	stored   string
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
			origin:   sourceOrigin(src),
			stored:   sourceStorage(src),
			files:    fileCount(resolved.GetFileCount()),
			serves:   fmt.Sprintf("serves %d", len(resolved.GetServiceNames())),
			wins:     fmt.Sprintf("wins %d", len(resolved.GetWonServiceNames())),
			status:   sourceStatus(resolved),
		})
	}
	return rows
}

// sourceOrigin names where this row's CONFIG lives, spelled out for the same reason the stored
// column is: a shared definition and a collection's own are equally normal answers, so neither
// gets a blank. A workspace source's priority, its presence in this list and where its descriptors
// are stored are all still this collection's own; only its address is edited elsewhere.
func sourceOrigin(src *grpcviewv1.DescriptorSource) string {
	if src.GetOrigin() == grpcviewv1.SourceOrigin_SOURCE_ORIGIN_WORKSPACE {
		return "workspace"
	}
	return "collection"
}

// sourceStorage names where this source's descriptors live. Both values are spelled out rather
// than one being a blank marker: "where the bytes are" is the question the column answers, and a
// committed source and an uncommitted one are equally normal answers to it.
func sourceStorage(src *grpcviewv1.DescriptorSource) string {
	if src.GetCommitDescriptors() {
		return "committed"
	}
	return "cached"
}

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

// sourceStatus distinguishes shadowed — resolved, but outranked — from serving nothing.
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

// oneLine collapses whitespace runs: a resolve error is often multi-line.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func renderSources(w io.Writer, sources []*grpcviewv1.DescriptorSource) error {
	rows := sourceRows(sources)

	var priorityWidth, idWidth, kindWidth, originWidth, storedWidth, filesWidth, servesWidth, winsWidth int
	for _, row := range rows {
		priorityWidth = max(priorityWidth, len(row.priority))
		idWidth = max(idWidth, len(row.id))
		kindWidth = max(kindWidth, len(row.kind))
		originWidth = max(originWidth, len(row.origin))
		storedWidth = max(storedWidth, len(row.stored))
		filesWidth = max(filesWidth, len(row.files))
		servesWidth = max(servesWidth, len(row.serves))
		winsWidth = max(winsWidth, len(row.wins))
	}

	for _, row := range rows {
		line := fmt.Sprintf("%-*s  %-*s  %-*s  %-*s  %-*s  %-*s  %-*s  %-*s  %s",
			priorityWidth, row.priority,
			idWidth, row.id,
			kindWidth, row.kind,
			originWidth, row.origin,
			storedWidth, row.stored,
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
