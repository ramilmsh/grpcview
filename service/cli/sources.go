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
			"When the workspace is UNTRUSTED and one of these rows is a bazel source, a line\n" +
			"after the table says so — this listing is where that fact is actionable, and a\n" +
			"workspace with no bazel source anywhere is never nagged about a capability\n" +
			"nothing is using.\n\n" +
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
		if err := renderSources(s.Out, ws.GetSources()); err != nil {
			return err
		}
		return renderSourceTrust(ctx, s.Out, sess, ws.GetSources())
	})
}

func renderSourceTrust(ctx context.Context, w io.Writer, sess session, sources []*grpcviewv1.DescriptorSource) error {
	building := 0
	for _, src := range sources {
		if src.GetBazel() != nil {
			building++
		}
	}
	if building == 0 {
		return nil
	}

	listing, err := listCollections(ctx, sess, false)
	if err != nil {
		return err
	}
	if listing.GetTrusted() {
		return nil
	}
	return writeLine(w, []byte(fmt.Sprintf(
		"! %s is not trusted: %s above cannot build here — `grpcview trust` allows it",
		listing.GetRoot(), bazelSourceCount(building))))
}

func bazelSourceCount(n int) string {
	if n == 1 {
		return "1 bazel source"
	}
	return fmt.Sprintf("%d bazel sources", n)
}

func newSourcesAddCmd(g *globalFlags, open clientFactory) *cobra.Command {
	var tls, commit bool

	cmd := &cobra.Command{
		Use:   "add <host:port>|<file.binpb>|//pkg:target",
		Short: "Add a reflection target, a descriptor-set file, or a bazel label as a definition source",
		Long: "Add one definition source at LOWEST priority, so adding never moves an\n" +
			"existing service to a different source — `sources reorder` is what does that.\n\n" +
			"The argument decides the kind, and the first test is the filesystem: an argument\n" +
			"that stats as a file is uploaded as a FileDescriptorSet, one that does not and\n" +
			"begins with `//` or `@` is a bazel LABEL, and anything else is dialed as a\n" +
			"reflection target. So a relative path only works from the directory that holds\n" +
			"the file, which is the same rule every other tool's file argument follows, and a\n" +
			"label must be written in FULL — `//pkg:target`, never bazel's `pkg:target`\n" +
			"shorthand, because `localhost:8080` is indistinguishable from that shorthand and\n" +
			"guessing wrong would dial a label or try to build an address.\n\n" +
			"A file's BASENAME is the upload's identity, not its path: re-adding a rebuilt\n" +
			"image of the same name REFRESHES that source in place at its existing priority\n" +
			"instead of adding a second, indistinguishable row. The path is recorded next to\n" +
			"it as a refresh recipe — that is all it is — so `sources refresh` re-reads the\n" +
			"file instead of asking to be handed it again. A reflection source is identified\n" +
			"by its address (and whether it is dialed with --tls), and a bazel source by its\n" +
			"canonical label, the same way.\n\n" +
			"A BAZEL source is a label whose DEFAULT OUTPUTS are descriptor sets. Unlike an\n" +
			"upload it knows how to produce its own bytes, so refreshing it runs a build —\n" +
			"and a build runs arbitrary code out of this repository's BUILD files, so it is\n" +
			"refused unless the workspace is TRUSTED. `grpcview trust` grants that, once, per\n" +
			"workspace root. --commit-descriptors applies to a bazel source exactly as to any\n" +
			"other kind, and is the answer to a fresh clone with no bazel installed.\n\n" +
			"Adding resolves the new source immediately — it is the one operation here that\n" +
			"touches the network, or bazel — so a target that cannot be dialed, bytes that do\n" +
			"not parse, and a label that cannot be built (an untrusted workspace included)\n" +
			"FAIL the add and nothing is added. That is deliberate: a listed source that\n" +
			"silently provides nothing is worse than an error. A source that resolved once\n" +
			"and whose target later goes away is the different case — it stays listed with\n" +
			"the reason, contributing nothing, and `sources ls` shows it.\n\n" +
			"Because it resolves, this is one of the two verbs that WAITS. A LABEL therefore gets\n" +
			"a " + buildTimeout.String() + " deadline instead of the usual one: a cold build of a large target is\n" +
			"minutes, so waiting is the normal case and not the pathological one. A dial and a\n" +
			"file read keep the ordinary deadline. `--timeout` overrides either way — for a build\n" +
			"slower still, or to give up sooner.\n\n" +
			"Nothing is printed on success.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			msg, err := buildAddSource(args[0], tls, commit)
			if err != nil {
				return err
			}
			if msg.GetBazel() != nil {
				useBuildTimeout(cmd, g)
			}
			return runSourcesAdd(cmd.Context(), g, open, msg, args[0])
		},
	}
	cmd.Flags().BoolVar(&tls, "tls", false,
		"dial the reflection target over TLS; it is part of the source's identity, so the same address with and without it are two sources")
	cmd.Flags().BoolVar(&commit, "commit-descriptors", false,
		"commit what this source resolves to, as a protojson sidecar in the collection, instead of caching it in local state; `sources commit` toggles it afterwards")

	return cmd
}

func runSourcesAdd(ctx context.Context, g *globalFlags, open clientFactory, msg *grpcviewv1.AddDescriptorSourceRequest, arg string) error {
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
		return nil, errors.New("no definition source given: `sources add` takes a reflection address, the path of a descriptor-set file, or a bazel label")
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
		abs, err := filepath.Abs(arg)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve the path of the descriptor set %q: %w", arg, err)
		}
		return &grpcviewv1.AddDescriptorSourceRequest{
			Source:            &grpcviewv1.AddDescriptorSourceRequest_DescriptorSet{DescriptorSet: raw},
			FileName:          filepath.Base(arg),
			Path:              abs,
			CommitDescriptors: commit,
		}, nil

	case isBazelLabel(arg):
		if tls {
			return nil, fmt.Errorf(
				"--tls does not apply to %q: it names a bazel label, and TLS selects how a reflection TARGET is dialed", arg)
		}
		return &grpcviewv1.AddDescriptorSourceRequest{
			Source:            &grpcviewv1.AddDescriptorSourceRequest_Bazel{Bazel: &grpcviewv1.Bazel{Label: arg}},
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

func isBazelLabel(arg string) bool {
	return strings.HasPrefix(arg, "//") || strings.HasPrefix(arg, "@")
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
		Long: "Re-resolve a definition source from its pointer, whatever that pointer is: a\n" +
			"reflection target is re-dialed to pick up a redeployed schema, a BAZEL source is\n" +
			"BUILT and its outputs re-read, and an upload that recorded a path when it was\n" +
			"added re-reads that file.\n\n" +
			"A PATHLESS upload — bytes that arrived through the browser's file picker, which\n" +
			"has a file and no path — is the one kind that cannot be refreshed. Nothing here\n" +
			"knows where those bytes came from, so the way to update one is `sources add` with\n" +
			"the rebuilt file, which refreshes that source in place. Naming its id fails\n" +
			"rather than reporting a refresh that fetched nothing.\n\n" +
			"Given no id, every REFRESHABLE source is refreshed in PRIORITY order — the order\n" +
			"`sources ls` prints, which is also the order the merge walks — one RPC per\n" +
			"source, since the RPC refreshes exactly one. Pathless uploads are the only rows\n" +
			"skipped there, because 'refresh everything that has a way to re-acquire itself'\n" +
			"is the useful reading of a bare refresh. The run stops at the first failure and\n" +
			"reports it, so a later source is never quietly skipped.\n\n" +
			"An unreachable target, or a build that fails, fails this command, unlike the\n" +
			"passive listing: you named the source and asked for it to resolve.\n\n" +
			"This verb WAITS, so it runs with a " + buildTimeout.String() + " deadline instead of the usual one: a cold\n" +
			"bazel build of a large target is minutes, and a bare refresh pays for every source\n" +
			"in the list inside that one budget. `--timeout` is the knob either way — for a build\n" +
			"slower than this, or for giving up sooner.\n\n" +
			"Nothing is printed on success.",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			useBuildTimeout(cmd, g)
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
				if src.GetUpload() != nil && src.GetUpload().GetPath() == "" {
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

func sourceOrigin(src *grpcviewv1.DescriptorSource) string {
	if src.GetOrigin() == grpcviewv1.SourceOrigin_SOURCE_ORIGIN_WORKSPACE {
		return "workspace"
	}
	return "collection"
}

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
	case src.GetBazel() != nil:
		return "bazel"
	default:
		return "unknown"
	}
}

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
