# Phase 1 — the workspace is the repo, collections are what's in it

**Status: shipped** — 1a through 1e are all on trunk (see §Sub-phases for what each
covered and the five details 1e changed). **Prereqs:** none. **Unblocks:** everything
(the extension must be able to say "the collection is *this* folder"). The rest of the
track — phases 2–6 — is unbuilt and lives in
[`active/vscode/README.md`](../../active/vscode/README.md). The workspace **daemon** was
part of this phase and is now its own document: [`daemon.md`](../../shipped/daemon.md).

Its four **Open questions** at the bottom are still open, and they are inputs to phase 2
and later rather than leftovers of this one.

## Goal

Run `grpcview` anywhere inside a repository and have it open **that repository** as a
workspace, listing every collection it contains. A collection stays exactly what it is
today — a directory with `grpcview.json`, `tree/`, `scripts/` — so from there on the
model is unchanged. What changes is *addressing*, plus a clean split between how
descriptors are **acquired** and where they are **stored**, which is what lets a
monorepo's own build outputs be the schema.

`docs/design/shipped/storage.md` already states half the intent — "a workspace = **any**
directory the user opens" — but it was never wired: `service/workspace/workspace.go:44`
hardcodes `store.New(filepath.Join(configDir, ".grpcview"))`, the user config dir, and
`ui/src/lib/workspace-query.ts:35` pins the collection name to `"default"`.

**This supersedes the original phase 1** ("the collection is the directory grpcview runs
in", one collection per process, `workspace_name` deleted). What changed and why is at
the bottom.

## The three tiers

```
acme/                                   ← workspace root: the repo (or --workspace)
  .git/
  MODULE.bazel                          ← bazel root, found separately (may be elsewhere)
  grpcview.work.json                    ← OPTIONAL: pinned collections, shared sources, bazel config
  proto/…                               ← the monorepo's protos, built by the monorepo's build system
  services/
    payments/
      requests/                         ← a collection
        grpcview.json  tree/  scripts/  descriptors/
    ledger/
      requests/                         ← another collection
        grpcview.json  tree/  scripts/
```

- **Workspace** — a root directory, a set of collections in it, and the shared
  descriptor-source definitions + build config they draw on. Owns no requests.
- **Collection** — unchanged: the unit that holds a tree, scripts, and its own ordered
  source list. Portable and committable on its own — with the one caveat in Decision 8,
  that a source pointing at a path or a build label only means the same thing inside the
  same repo.
- **Request** — unchanged.

Note what is **not** in a collection directory any more: `.grpcview/`. All local state
moves out (Decision 6), so everything left in a collection is committed content.

## Decision 1 — the root is the repo, found by walking up

In order, first hit wins:

1. `--workspace <path>` — that path *is* the root; no walking. Explicit, scriptable, what
   the extension passes (`workspaceFolder`), and it lets you serve a workspace **from
   outside it** without a `cd`. The name is free precisely because the old `--workspace`
   (which meant a collection) becomes `--collection` (Decision 3).
2. The nearest ancestor of the cwd holding `.git`.
3. Otherwise the cwd, with a startup warning.

One sentence the user can hold: **grpcview's workspace is your repo.** Keep it to those
two rules. Earlier drafts also made `grpcview.work.json` a root marker; that reintroduces
the "which of several nearest roots wins" question a monorepo user is already fighting,
for no gain — the manifest is *read* from a root you have already determined, never used
to find one. The **bazel** root is a separate question answered separately (Decision 8);
it is often the same directory and must not be assumed to be.

The manifest is **optional and creatable-on-demand**: zero-config is the common case, and
the file appears only when the user pins a collection list, declares a shared source, or
sets bazel options. Nothing in sub-phase 1a needs it to exist.

Consequence to keep in view: `grpcview` in a non-repo directory makes *that* directory the
root, so `$HOME` is a legal (if unhelpful) workspace. The discovery cap in Decision 4 is
what stops that being a hang.

## Decision 2 — a collection is addressed by its path, and named separately

`workspace_name` becomes `collection`, and its value is the slash-separated path of the
collection directory relative to the workspace root — `services/payments/requests`. No
registry, no ids to allocate, readable in a log, and stable under everything except
moving the directory (which is a `git mv` the user did deliberately).

This is why the original plan's *deletion* of the field was wrong to reach for: the
addressing axis it provides is precisely what a monorepo needs. Every handler keeps its
shape; `store.Open(ctx, id)` keeps working. The diff is a rename plus a resolution
change, not the removal of a parameter from twenty RPCs.

`store.New(base)` already roots collections at `<base>/<name>` (`store.go:78-81`), so the
change at the store is: treat `base` as the workspace root, `name` as a relative path,
and add the two guards that a path-shaped, wire-supplied key needs —

- **Traversal.** Reject absolute paths, `..` segments, and anything that resolves outside
  the root after `filepath.Clean`. The id comes off the wire; it must not be able to name
  `../../../../etc`.
- **One directory, one handle.** `Collection.mu` is the write serializer, so two handles
  on one directory defeat it. Key the handle map on the cleaned path **case-folded** —
  `Requests` and `requests` are one directory on macOS, and the store already treats slug
  comparison as case-insensitive for exactly this reason (`layout.go:92-99`).

### The id is not the display name

Today `Collection.name` is both, and it leaks: `writeCollection` defaults the manifest's
`name` from the addressing key (`fs.go:747-753`), and `load` falls back to it
(`fs.go:57`). With the key now a path, a new collection would commit
`"name": "services/payments/requests"` and render that string as its panel header and
tree root.

Split them:

- **`id`** — the workspace-relative path. Addressing only. Never written to disk; it *is*
  the disk location.
- **`name`** — display only, `grpcviewstorev1.Collection.name` as it already exists
  (`storage.proto:22`). Set by `grpcview init --name`, defaulting to the directory's base
  name (`requests`). `load`'s fallback becomes the base name, never the id.

Five collections all defaulting to `requests` is the predictable annoyance, and it is the
same one VS Code has with same-named folders: `ListCollections` returns both, so the UI
shows the name and disambiguates with the path.

`definitionsCache` (`definitions.go:34`) is keyed by the addressing string; see Decision 9
for what changes there and why.

## Decision 3 — the wire message `Workspace` becomes `Collection` (decided)

Introducing a real workspace while `grpcview.v1.Workspace` means "one collection's
contents" would leave the most important word in the codebase pointing at two things.
Rename the message; the on-disk schema already calls this a `Collection`
(`storage.proto:22`), so the two schemas finally agree, and `convert.go` stops bridging a
name change.

- `service WorkspaceService` **keeps** its name — it now genuinely serves a workspace.
- `Workspace` → `Collection`; `workspace_name` → `collection`; `GetResponse.workspace` →
  `.collection`.
- UI directory names (`features/workspace/`) stay — they name the cockpit view, not the
  message.
- The CLI's `--workspace` (`service/cli/root.go:64`, default `"default"`) becomes
  `--collection`, freeing `--workspace` for the root (Decision 1). The two flags then read
  as what they are: **which workspace** and **which collection in it**.

`--workspace` therefore *changes meaning* rather than being added — the old spelling
`--workspace default` now names a directory. Make a `--workspace` that does not name an
existing directory exit 2 with that sentence, rather than silently rooting a workspace at
a path that isn't there.

Scale of the mechanical diff, so it is not underestimated: 36 occurrences of
`workspace_name` in `service.proto` alone, ~15 `Workspace workspace = 1` response fields,
every UI and CLI call site, and the **committed** generated declarations
(`proto/grpcview/v1/*_pb.d.ts`) — see Verify.

Alternative considered: keep the names and accept the overload. Rejected — the cost is
paid by every future reader, and *this* is the change that makes the two meanings
collide.

## Decision 4 — discovery: declared if declared, bounded scan otherwise

`grpcview.work.json` may pin the list:

```jsonc
{
  "schemaVersion": 1,
  "name": "acme",
  "collections": ["services/*/requests", "tools/loadgen/requests"]
}
```

Declared wins whenever present: fast, deterministic, reviewable, and it survives a
colleague's stray `grpcview.json`. When absent, scan from the root for `grpcview.json`:

- **Prune** `.git`, `node_modules`, `bazel-*` (convenience symlinks into the output base
  — following them is unbounded), any dot-directory, and anything gitignored. Do not
  follow symlinks.
- **Prune at a hit, and this invariant is load-bearing.** A collection is a leaf; do not
  descend into one looking for nested collections. Nested collections are not a supported
  shape (`tree/` is the nesting mechanism) — and because no collection id can then be a
  path prefix of another, `<collection id>/<path inside the tree>` is unambiguous with a
  plain `/` separator, which is what Decision 10 keys the UI on.
- **Cap it.** Beyond ~20k directories visited, stop and report: *"too large to scan —
  pin `collections` in grpcview.work.json."* A home directory that happens to be a git
  repo, or a non-repo `$HOME` falling through to rule 3 of Decision 1, must fail loudly
  and cheaply, not hang.
- **Cache** the result in workspace state (Decision 6), invalidated by the root's own
  mtime plus an explicit refresh. Discovery is not on the hot path — every RPC addresses a
  collection directly.

**Gitignore matching comes from go-git.** `go-git/v5/plumbing/format/gitignore` is a
standalone matcher (`ReadPatterns` collects nested `.gitignore` files, `NewMatcher(…).Match`
answers) and does not drag in the `Status()` weaknesses recorded in
[`go-git.md`](../../research/go-git.md) (line 59: it walks and hashes untracked
files and descends into ignored dirs — that is about *status*, not pattern matching).
go-git is landing next anyway to make the tool git-aware, and that work needs the same
matcher for its own ignore hygiene, so this is one dependency serving two features.
`go.mod` gains `go-git/v5` and `go-billy/v5`; gazelle / `bazel mod tidy` must run before
anything builds.

Why gitignore at all: the fixed prune list covers the build outputs we happen to know
about. Gitignore covers the rest — a `dist/`, `target/` or `vendor/` that a build step
copied a collection into would otherwise show up as a second, real-looking collection.

New RPC: `ListCollections` → `[{id, name, path, source_count, error}]`. Cheap by
construction, and the only thing the UI needs before it knows which collection to
`Get`. **Never `Get` every collection eagerly** — `Collection.descriptor_set` is the
merged `FileDescriptorSet` in bytes, and N of those is the one way to make this design
feel slow.

## Decision 5 — nothing is created that the user did not ask for

The original plan's "watch out" was that pointing a collection at a repo root scatters
`grpcview.json`, `tree/` and `scripts/` among the project files. The workspace tier
removes the hazard rather than documenting it:

- **A workspace root gets no files.** Not even the manifest, until something needs it.
- **Collections are created explicitly** — `grpcview init [dir] [--name]`, or the UI's
  empty state asking where to put one (`requests/` offered as the default).

### Delete the auto-create, and build the empty state

This is not a rule to add; it is a behaviour to remove. **Three handlers currently create
a collection on demand**: `Get` catches `ErrNotFound` and calls `EnsureCreated`
(`workspace.go:79`), and so do `openWithSources` (`:199`) and `mutate` (`:237`).
`EnsureCreated` does `MkdirAll(<root>/<id>/tree)` and writes `grpcview.json` plus a
`.gitignore` (`fs.go:129-142`).

That is harmless while the id is the constant `"default"` under `~/.config`. Once the id
is a wire-supplied path joined onto a repo root, **a stale UI query, a typo'd
`--collection`, or an extension asking about a path someone `git mv`d silently
materialises a collection in the middle of the repo.** It is exactly the pollution this
decision claims to have designed away.

So: `EnsureCreated` leaves the read and mutate paths, `Get` returns `NotFound`, and
`grpcview init` (or the UI) is the only thing that creates a collection. The consequence
is real work, not a one-line deletion: **the UI currently depends on the auto-create** —
first load is a `Get` that gets an empty collection for free. 1a owns the empty state that
replaces it ("no collection here — create one?"), and the CLI's equivalent is Decision 11's
exit-2-with-candidates.

## Decision 6 — local state leaves the collection directory

A collection currently keeps `.grpcview/` beside its committed files, holding three
things (`store.go:101-118`, `fs.go:860-866`):

| path | what it is |
|---|---|
| `.grpcview/cache/services.json` | the merged descriptor set + services + per-source summaries |
| `.grpcview/cache/sources/<slug>-<hash>.binpb` | one resolved `FileDescriptorSet` per source id |
| `.grpcview/history/<tree slug path>/history.json` | run history, keyed by slug so it survives renames |

None of it belongs in the repo. Move all of it to a **single durable per-workspace state
root**, keyed by a hash of the workspace root's absolute path, and the collection
directory becomes 100% committed content.

- **Durable, not a cache directory.** `os.UserCacheDir()` is `~/Library/Caches` on macOS,
  which the OS may reclaim and cleanup tools do delete. Run history is user data, and a
  pointerless upload's descriptors (Decision 7) cannot be re-fetched, so neither may live
  somewhere disposable. Use a state dir (`os.UserConfigDir()`-rooted), and reserve
  `os.UserCacheDir()` for the daemon's registration file, which genuinely is disposable
  ([`daemon.md`](../../shipped/daemon.md)).
- **`ensureGitignore` is deleted, not fixed** (`fs.go:757-764`). Its bug — returning early
  whenever any `.gitignore` exists, and so never ignoring `.grpcview/` inside a repo that
  already has one — becomes unreachable because there is nothing left to ignore. Earlier
  drafts replaced it with a self-ignoring `.grpcview/.gitignore` containing `*`; that is no
  longer needed either.
- `stateDir` and `gitignoreFileName` drop out of `reservedSlugs` (`layout.go:58-64`),
  which the Windows removal is already shrinking.
- **A collection may be the workspace root** (`id` = `.`, the common non-monorepo case:
  `cd ~/api-requests && grpcview`). With no state in either directory there is nothing to
  collide, which is the other reason to move it.

Cost to accept: copying or `git mv`-ing a collection directory orphans its history and its
resolve cache, because both are keyed by path. Both were gitignored, so neither ever
travelled between machines anyway — a move loses local history and forces a re-resolve.

Per AGENTS.md:23-28 there is no migration: the existing `~/.config/.grpcview/default`
collection is abandoned, not moved.

## Decision 7 — acquisition and storage are two systems; the descriptor store is the seam

This is the decision the rest of the source model hangs off, and it is mostly a statement
of what the code already does, written down so it stops being drifted away from.

**Two disconnected systems.**

1. **Acquisition** — reflection, upload, `bazel`, later a buf registry. These are
   *mechanisms for populating and updating the descriptor store*, nothing more. They are
   allowed to be unavailable: prod is unreachable, the file is gone, bazel isn't installed.
2. **Everything downstream** — the merge, the link, the services list, `describe`,
   `invoke`, type generation — reads **only** the store. It never knows or cares which
   mechanism put the bytes there, and it must never rescan a source.

The store is the unifying layer. A source is therefore just a **pointer** plus its bytes
in the store, and the kinds differ only in the pointer:

| kind | pointer | what an update does |
|---|---|---|
| `reflection:<addr>` | an address | re-dial |
| `upload:<file name>` | a path relative to the workspace root, **when one is known** | re-read; with no path, a human hands over new bytes |
| `bazel:<//pkg:target>` | a label | **build**, then read |

**Acquisition happens on add and refresh only.** This is already true and worth pinning:
`putDescriptorState`/`resolveOne` are reachable from exactly four RPCs —
`AddDescriptorSource` (`workspace.go:137`), `RefreshDescriptorSource` (`:159,163`),
`ReorderDescriptorSources` (`:184`), `RemoveDescriptorSource` (`:222`). `Get`,
`Collection.load`, both invoke paths and `describe` cannot reach them. **No load-time
acquisition, ever** — no stat-and-re-read, no polling, no implicit rebuild. An earlier
draft had bazel sources re-reading their output on load "so `bazel build` in your terminal
is picked up automatically"; that is deleted. A bazel source exists precisely because it
knows how to *build*, so a refresh builds, and noticing a source change is a separate
mechanism (see the note at the end of Decision 8).

**Upload keeps its file name for identity, and a path only as a refresh recipe.** A
browser file picker hands JS bytes and a filename and never a path
(`AddSourceModal.tsx:17`); the CLI, adding the same bytes, does know the path. Both must
produce **the same source** — so identity is the file name in either case, and the path is
optional metadata that says how to re-read. There is no second kind for "an upload that
remembered where it came from": that is one kind with the field filled in.

The corollary is the point of this whole decision: **the bytes are in the store, so a
missing, dead or never-known path costs you a refresh and nothing else.** Merge, link,
`describe` and `invoke` are unaffected, because none of them ever look at a pointer.

**The source id and the content key are different keys.** The id is the pointer, and it is
stable across content changes — that is what makes re-adding a source a refresh instead of
a duplicate (`service.proto:16`). The content key is the bytes' digest. So the store holds
`id → current digest` alongside `digest → blob`. Collapsing them breaks refresh-in-place.

### `--commit-descriptors`: the same bytes, a different location

Whether a source's descriptors are committed is a **per-source flag** and changes only
*where the store writes*, never any mechanics:

- **off (default)** — blobs in the workspace state root, **content-addressed**:
  `blobs/<sha256>.binpb`. CAS gives dedup for free, which is exactly what a monorepo needs
  when five collections point at one bazel target (Decision 8), and "same digest, no write"
  removes mtime churn and lines up with the digest-gated link cache.
- **on** — protojson in the collection, in a **sidecar named by source id**:
  `descriptors/<slug>-<hash of id>.json`, reusing `sourceCachePath`'s existing naming
  (`store.go:112-118`), whose hash is of the id — which is now exactly the right choice for
  the right reason.

The asymmetry is deliberate and the reasons are opposite: CAS is right for the cache
(dedup, immutability); a **digest**-named committed file would turn every refresh into an
add+delete instead of a diff, destroying the readable-protojson rationale for committing at
all.

Not inline in `grpcview.json`, which is what uploads do today: that file also holds root
ordering, the source list and script ordering, so an inline multi-megabyte descriptor set
means **dragging one request rewrites a multi-megabyte file**. Sidecars fix uploads as a
side effect.

Consequences:

- **Toggling the flag never dials or builds.** On = write the sidecar from the bytes
  already in the store. Off = drop the sidecar, keep the blob. The one edge is toggling on
  for a source that has never resolved: resolve-then-commit, or reject — pick one.
- **Upload's special case disappears.** `resolveOne`'s upload arm (`sources.go:100-108`),
  `UploadDescriptors` (`convert.go:125`) and `Upload.descriptor_set` on disk all fold into
  "the bytes arrived with the add call and went into the store like everything else". The
  `DiscardUnknown` normalisation that keeps a `buf build` image's bytes stable across a
  round trip (`sources.go:85-88`) moves to the store's write boundary, where it applies to
  every kind. That deletion is the best evidence the layering is right.
- **Forcing the flag on for uploads is unnecessary** given a durable state root
  (Decision 6): an uncommitted upload's only copy is a blob that nothing reclaims. It stays
  a real choice.
- **`pruneSourceResolves` becomes unsafe as written** (`fs.go:837-841`). It deletes every
  cache file whose id is not in *this collection's* list; under a shared CAS another
  collection may reference the same blob. Pruning is workspace-scoped: delete blobs that no
  source record in any collection references. Scope the CAS per workspace — cross-workspace
  dedup is not worth widening GC to every workspace.

### Definitions can be shared; order stays per collection

Five collections pointing at the same target must not mean five copies of the config. But
precedence is per-collection by design ("order is precedence, and only order" —
`AGENTS.md`), so the tiering splits along that line:

- **The workspace manifest holds source *definitions*,** named by the same config-derived
  id `store.SourceID` already produces (`sources.go:11`), so a source declared in both
  places is literally the same source.
- **A collection's `grpcview.json` holds the ordered *list*.** An entry is either an inline
  definition (as today) or a **reference**: an id with no oneof arm set, resolved against
  the workspace manifest. `normalizeSources` (`sources.go:45`) currently drops a contentless
  entry — that rule becomes "contentless + id = a workspace reference", an error only when
  the workspace does not define it.
- **A workspace-level definition must carry a pointer**, so a *pathless* upload cannot be
  one: there is nothing to declare but a digest, and a digest is content, not config.
  Reflection and bazel qualify; an upload that knows its path qualifies in principle, and
  sharing it is left unbuilt because nothing yet asks for it (`storage.proto:41` says
  "may not be an Upload" and stays true of the shipped shape).
- Blobs are shared by construction: CAS is per workspace, so one build serves every
  collection referencing it with no id-keyed sharing scheme needed.
- The wire message gains a read-only `origin` (`COLLECTION` | `WORKSPACE`) so the Sources
  view can show a shared source as shared: reorderable and removable *from this
  collection's list*, editable only at the workspace level.
- `defaults.sources` in the manifest seeds a new collection's list, so `grpcview init` in a
  configured monorepo produces a collection that already resolves.

### The dial target is the last place the two systems are fused

`mergeSources` builds `dialFor` from `resolvedSource.server` and stamps it onto
`Service.source`, documented as "the default invoke target" (`workspace.proto:46`);
`resolveTarget` then walks the *source list* for a reflection source when a request has no
target (`invoke.go:667-700`). So which acquisition mechanism supplied the schema currently
decides where traffic goes — backwards under this decision.

The decoupling that keeps the ergonomics: when a reflection source is added and the
collection has no default target, record it as **the collection's** default target. Invoke
then reads request target → collection default and never consults the source list. "Add a
reflection source and immediately invoke" still works, and a collection whose schema comes
only from bazel gets a target set once instead of a `FailedPrecondition`. Arguably its own
change rather than this phase's; recorded here as the intended end state.

## Decision 8 — upload learns its path, and bazel is a label that produces one

Today a schema that is not reflected must be **uploaded**, and an upload is a dead end: it
cannot be refreshed, because the bytes arrived without any record of where they came from.
In a monorepo that is doubly backwards — the descriptors are a *build output* of the repo
the collection lives in, and they change on every proto edit.

Two changes, and neither of them is a new "file" kind:

```proto
// Upload gains a refresh recipe. Empty when the bytes came from a browser picker.
message Upload { string file_name = 1; string path = 2; }   // id: "upload:<file name>"
// A label whose default outputs are descriptor sets; built, then read.
message Bazel  { string label = 1; }                        // id: "bazel://pkg:target"
```

An earlier draft added a separate `File { path }` kind and demoted upload to browser-only.
That was wrong, and the reason it was wrong is Decision 7's own layering: **a source is
bytes in the store plus an optional recipe for getting new ones.** "Bytes with a path" and
"bytes without a path" are one kind with a field set or unset — not two kinds — and the
CLI and the browser must produce the same thing when a human adds the same descriptor set
from each. Naming the with-path case `file:` would also have made the two adds two
different sources with two different ids, so re-adding from the other surface would
duplicate instead of refresh.

`path` is what every non-bazel monorepo needs — buf, protoc, gradle and pants all write a
descriptor set somewhere, and pointing at it makes the upload refreshable. `Bazel` is that
plus knowing how to *produce* the file.

Consequences, which are mostly things that do **not** change:

- **`sources add <path>` keeps meaning what it means** — read the bytes, store them, and
  now also record the path. One positional argument, one reading; the earlier draft's
  disambiguation problem never arises because there is nothing to disambiguate against.
- **`--commit-descriptors` is orthogonal.** It picks where the store writes (CAS blob vs
  committed sidecar) and nothing else, for uploads exactly as for every other kind.
- **A dead path costs a refresh and nothing else.** The collection loads, links, describes
  and invokes from the stored bytes, so `/tmp/x.binpb` disappearing overnight degrades to
  "refresh errors" — which is strictly better than today's upload, where refresh was never
  possible at all.
- **A browser upload refreshes by re-dropping the file.** Same file name is the same id, so
  the existing re-add-is-a-refresh path already covers it; the UI should say that rather
  than disabling the refresh button.
- `id` is the file name, never the path, so a `git mv` of the source file changes the
  recipe and not the source's identity.

Surface this actually touches, which the two message stubs understate:
`AddDescriptorSourceRequest`'s own differently-shaped oneof (`service.proto:10-21` —
`bytes descriptor_set | Server reflection`, plus `file_name` and `commit_descriptors`), the
disk oneof (`storage.proto:169-193`), `SourceID` **and** `diskSourceID`
(`store/sources.go:14,25`), `diskToWireSource`/`wireToDiskSource` (`convert.go`),
`resolveOne`'s arms, and `grpcview sources add`.

### The bazel source, concretely

Verified in this repo before writing this down:

- **A plain `proto_library` is enough.** `bazel build //proto/grpcview/v1:grpcviewv1_proto`
  writes `bazel-bin/proto/grpcview/v1/grpcviewv1_proto-descriptor-set.proto.bin`, and that
  file is **transitive** (it carries `google/protobuf/{any,duration,struct}.proto`, i.e.
  protoc ran with `--include_imports`) and **carries `source_code_info`** — a known doc
  comment from `workspace.proto` is present in the bytes. So it links standalone, *and* it
  beats reflection on hovers, which is the entire reason source order is user-controlled
  (`AGENTS.md`, "Definition sources"). No `rules_proto` helper rule is required; any label
  whose default outputs are descriptor sets works, including a `proto_descriptor_set` that
  merges several.
- **Find the outputs with `bazel cquery --output=files -- <label>`**, run after
  `bazel build -- <label>`. Verified: it prints
  `bazel-out/darwin_arm64-fastbuild/bin/proto/echo/v1/echov1_proto-descriptor-set.proto.bin`,
  workspace-root-relative. Parsing `bazel-bin/<pkg>/<name>-descriptor-set.proto.bin` by hand
  instead would bake in both the rule's output naming and `--symlink_prefix`. Pass
  **identical** flags to both invocations or cquery reports a different configuration's path.
- **Multiple output files, and duplicates, are expected.** Read every path cquery prints,
  unmarshal each into a `FileDescriptorSet`, concatenate, and **dedupe by file name before
  linking** — a merging rule concatenates per-target sets, so the same file can appear twice
  and `desc.CreateFileDescriptorsFromSet` rejects that.
- **Never build a shell string.** Exec `bazel` with an argv slice, `--` before the label,
  and a strict label regex, so a label can never arrive as a flag
  (`//x --output_base=…`). Canonicalize `proto:foo` and `//proto` to `//proto:foo` *before*
  deriving the id, or two spellings become two sources and break the
  "re-adding an id refreshes in place" invariant.
- **Bazel root** = nearest ancestor of the workspace root with `MODULE.bazel`,
  `WORKSPACE`, or `WORKSPACE.bazel`; overridable as `bazel.root` in the manifest. Do not
  shell out to `bazel info workspace` for this — walking is instant and starts no server.
- **A refresh builds, synchronously.** `--curses=no --color=no --noshow_progress`, a
  manifest `bazel.timeout` (default 10m), and the tail of stderr as the error text. A
  descriptor build is seconds in practice; the timeout is for the pathological case. No
  async `Resolved.state` machine in this phase — see the note below.

**Three rules about paths and symlinks, which read as contradictory until they are
separated.** They govern different things:

1. **An upload's `path` comes off the wire**, authored by a human. Confine reads to the
   workspace root and refuse symlink escape — same guard, same reason, as `store.Open`.
   Unconfined, `../../../../home/other/secret.binpb` is an arbitrary-file-read primitive
   whose contents surface in the UI's descriptor set and possibly in a committed sidecar.
   Note this applies to the *recorded* path on refresh too, not only at add time: a
   committed `grpcview.json` a colleague pushed is wire input like any other.
2. **A `Bazel` source takes no path from anyone.** It takes a label; the paths come back
   from bazel itself, so they are trusted and are resolved through the `bazel-out`
   convenience symlink (or via the `bazel-bin`/execroot `bazel info` reports, which sidesteps
   the symlink). No confinement applies, because nothing here is user input.
3. **Discovery must not *walk* `bazel-*`** (Decision 4), because those symlinks lead into
   an unbounded output base. That is about walking to find `grpcview.json`, and has nothing
   to do with reading one known file.

And neither kind ever persists a *path* as the payload — the payload is always bytes in the
store (Decision 7), so "does the committed thing point outside the root" is a non-question.

**Remote execution can leave the bytes remote.** With `--remote_download_minimal` the build
succeeds and the file does not exist locally. Do not silently add download flags to the
user's build — detect the missing file and say so, naming `--remote_download_toplevel` as
the fix. (This repo's `--config=remote` does not set a download mode and Bazel 9 defaults
to `toplevel`, which materialises an explicitly-built label's outputs — so the handling is
worth building but the case needs opting into, not "live by default".)

**CI is two steps.** With no load-time acquisition, a fresh clone whose bazel source is
uncommitted has an empty store, so `grpcview invoke` alone finds nothing resolved and
fails. The pipeline is `grpcview sources refresh && grpcview invoke …`, or `invoke` gains
an explicit `--refresh`. Either way the schema comes from a build rather than from a
reflection endpoint or a committed blob, which was the point.

**Future: an ibazel-style watcher, not polling.** The way to get "my rebuild shows up" is a
watcher that performs the same explicit refresh a human would — an *acquisition trigger*,
not a change to the read path. ibazel's approach is the one to copy: resolve the target's
source-file set from bazel (`bazel query 'kind("source file", deps(//pkg:target))'`, or the
build event protocol) and `inotify` that set. A directory glob is the version that silently
misses transitive deps in other packages. This also makes `Resolved.state` + polling a hard
prerequisite for that work — a watcher firing synchronous multi-minute unary RPCs at a UI is
not viable — so the async resolve lands with the watcher, not before.

### Trust (decided)

A committed `grpcview.json` naming a bazel label means opening a repo can run
`bazel build`, which runs arbitrary build code — and **bazel actions are not guaranteed to
be sandboxed.** `--spawn_strategy=local`, `local = True` on a rule, a
`repository_rule`/module extension fetching at load time, and platforms where sandboxing
is unavailable all execute with the user's full privileges. So the threat is not "a
sandboxed action misbehaves", it is arbitrary code execution from cloning a repo and
opening it — the same class as VS Code tasks, which is why VS Code has Workspace Trust.

Copy it, since the project rule is to copy VS Code where an equivalent exists: trust is
per workspace root, remembered in user state, granted by a UI banner or `--trust`.
Untrusted: everything loads and reflection/upload sources resolve; a source that would
execute a build does not, and shows its reason in `Resolved.error` — the existing channel
for a source that cannot resolve.

**Trust is checked at the point of exec, not at load.** A workspace trusted yesterday whose
manifest changed today is still trusted (matching VS Code — trust is on the folder, not the
content), but the check must live next to `exec.Command`, so no future caller can reach a
build by another path.

## Decision 9 — the merged view lives in memory, derived on first touch

Reads must not do I/O for a schema the process already has. Today they do, for a reason
worth naming so it isn't reintroduced.

`definitionsOf` (`definitions.go:80-115`) reads `services.json` off disk, sha256s the whole
merged `FileDescriptorSet`, and uses that digest to decide whether the cached *linked*
descriptors are still valid. So a cache **hit** still costs a full read plus a full hash;
only the linking is skipped. `0b9059d` says so outright ("the per-invoke cost is a cache
read rather than a re-link of every file") — the digest key was picked because the bytes
had already been read for a different reason, and against the network reflection round trip
that commit removed, a local read measured as nothing.

The daemon makes that read the only remaining per-invoke I/O, so:

- **Key the in-memory cache by collection id, not by content digest.** A hit is a plain map
  lookup: no read, no stat, no hash.
- **The writer invalidates.** The four acquisition RPCs (Decision 7) already funnel through
  `putDescriptorState`; they drop or replace the entry in the same code path. Nothing else
  can change descriptors — the blobs and sidecars are written only by grpcview, and a human
  hand-editing `request.json` is tree data that does not touch this cache.
- **Drop `services.json` entirely.** It is a cache of a cache: the merged view is a pure
  function of (the blobs, the source order in `grpcview.json`), which is all `mergeSources`
  does once bytes are in hand. Persisted descriptor state becomes exactly one thing — blobs
  keyed by source id — matching Decision 7's split with nothing extra. This deletes
  `writeMergedCache`, `readMergedCache`, `servicesCachePath` and `overlayResolved`'s file
  dependency (`fs.go:71-81,766-790`), removes the multi-megabyte protojson write that a
  **reorder** currently performs even though no descriptor changed, and removes the one
  place a *wire* message is persisted to disk, which `storage.proto:5-12` explicitly says
  should not happen.
- **Derive lazily, per collection, on first touch.** Daemon start reads no blobs and merges
  nothing; `ListCollections` touches only manifests. The first call needing definitions for
  a collection reads its blobs, merges in its source order, links, and holds the result.
  Eager merging at startup would be wrong for the same reason eagerly `Get`ting every
  collection is (Decision 4), and it would blow the readiness budget of an auto-spawned
  daemon with a CLI verb blocked on it. First-touch cost is N blob reads plus one
  `CreateFileDescriptorsFromSet` — local CPU, no network.
- **A merge failure must not fail `Get`.** `mergeSources` returns `FailedPrecondition` when
  sources disagree about protos they share. Today that can only surface during a source
  mutation, because that is when the merge runs; moving the merge to first touch means a
  committed `grpcview.json` a colleague pushed could make `Get` fail and the whole
  collection stop loading. Record the failure (per-source `resolved.error`, or a
  collection-level error) and return the tree with an empty `services` list — which is
  already what a fresh clone with no blobs does ("no resolved definitions: add a descriptor
  source (or refresh one) first").
- **The cache needs a bound.** `map[string]definitionsEntry` with no eviction is fine at one
  collection per process; with N collections and a long-lived daemon it is a slow leak of
  fully-linked descriptor sets. Bound it or make it an LRU.

Only `--in-process` runs pay for dropping `services.json`, because every one of them is a
cold start: read blobs, merge, link, per invocation. That is the path the daemon exists to
replace, and `--in-process` is the explicit escape hatch.

## Decision 10 — the UI: a collection tier that disappears when there is one

VS Code's Explorer shows one root folder as a header and multiple as top-level
collapsible rows; match that.

- **One collection** — no extra tier. The name goes in the panel header, exactly today's
  look.
- **Several** — one row per collection above the tree's current roots. A new adapter
  `kind`, portable tier (`IconToken`, no `renderRow` — `types.ts:29,32,57`) so it stays
  usable by a VS Code `TreeProvider`.
- **Lazy children need care.** `flatten`/`Tree` throw on a thenable by design
  (`flatten.ts:11,27`, `Tree.tsx:30,41`), so expanding a collection kicks off its `Get` and
  renders a loading child until data lands — the host resolves the promise, never the tree.
- **Cross-collection drops are rejected** in the host's `canDrop`. `MoveItem` addresses one
  collection; moving between them is a copy+delete across two source lists and two script
  sets, and it is not this phase.
- **Sources, Scripts and the service/method pickers scope to the active collection**,
  because the merged descriptor set now differs per collection.

### Keys become `<collection id>/<slug path>`, and slugs are the point

`itemKey` is currently built from **display names** (`keyOf` = `[...path, name].join("/")`,
`format.ts:36-42`), and nothing validates a display name — name a folder `a/b` and its
descendants' keys are already ambiguous today. Key on **slugs** instead:

- A plain `/` separator is unambiguous, because no collection id can be a path prefix of
  another (Decision 4's non-nesting invariant). No URI scheme is needed: nothing parses
  these keys — `findByKey` compares whole strings and `moveSubtree` prefix-matches on
  `key + "/"` (`ui-store.ts:134-144`).
- **A rename stops changing any key**, so the `moveSubtree` calls on rename
  (`CollectionPanel.tsx:135`, `RequestWorkspace.tsx:180`) become dead code and get deleted.
  Only real moves remap, and there is still exactly one remapper.
- This needs slugs on the wire: `Item` carries only `name` (`workspace.proto:124`). Add
  `Item.slug`, filled from the `childEntry` the server already holds (`fs.go:663`). RPC
  addressing stays on display-name paths; `ItemWithPath` carries a slug path for keys.

## Decision 11 — the CLI resolves the collection from the cwd

Same shape as `git` and `bazel`: where you stand decides what you address.

1. `--collection <id>` wins.
2. Else the nearest ancestor of the cwd with `grpcview.json`, bounded by the workspace
   root — so `cd services/payments/requests && grpcview invoke GetBalance` just works.
3. Else, if the workspace holds exactly one collection, that one.
4. Else exit 2, listing the candidates. Never guess.

Plus `--workspace <path>` on every verb and on `serve` (`service.Options` gains `Root`;
`service/service.go:30` — argv parsing stays in the callers), and `grpcview collections ls`.

**`//service/cmd/dev` needs the flag too.** Under `bazel run` the cwd is the runfiles tree,
not the repo, so cwd-walk discovery would root the workspace inside `bazel-out`. Pass
`--workspace`, or read `BUILD_WORKSPACE_DIRECTORY`, which bazel sets for `bazel run`.

## Decision 12 — bind loopback, and drop the wildcard CORS

Not a consequence of this phase — **a live defect in today's release binary**, pulled
forward because this is when the code is open, and because it should not wait behind
[`daemon.md`](../../shipped/daemon.md).

`service/service.go:81` binds `net.IPv4zero` — every interface, so the server is
LAN-reachable — and `:73-79` sets `AllowedOrigins: []string{"*"}`, so *any* web page you
visit can drive your local grpcview: list collections, read history, invoke against your
internal services.

- **Bind loopback.** Nothing off-machine ever needs to connect, so there is **no `--host`
  flag and no exposure option** — an option nobody needs is a footgun with a manual. This
  stays compatible with the extension's remote story: VS Code's port forwarding tunnels
  *because* the server is loopback-bound.
- **Production needs no CORS at all.** `client.ts:4` uses `window.location.origin` when
  `PROD`, so the UI's calls are same-origin and never preflight. The only cross-origin
  caller in existence is the vite dev server talking to `//service/cmd/dev`. So CORS
  becomes a **dev-only allowance for one known origin**, and today's `"*"` is a dev
  convenience that shipped into the release binary.
- **Loopback is not an authorization boundary** — any local process under any local user
  can connect. There is no token in this phase (see [`daemon.md`](../../shipped/daemon.md) for what
  was considered and why it was dropped); the boundary is loopback plus origin policy, and
  the doc should say that rather than implying more.

## Sub-phases

Each is independently verifiable and independently useful.

**1a–1e are all shipped, so phase 1 is done.** Everything below is still written as it was
planned — the decisions above cite the code as it stood *before* any of this landed, so
treat those `file.go:line` references as the premise of a decision rather than a
description of trunk. `AGENTS.md` is where shipped behavior lives.

| | Step | Status | Contents |
|---|---|---|---|
| **1a** | Addressing | shipped | `--workspace`, root discovery, `store.Open` by relative path + both guards, id/display-name split, local state out of the collection dir (Decision 6), auto-create deleted + UI empty state, the `Workspace`→`Collection` wire rename (and `--workspace`→`--collection`), `grpcview init`, loopback + CORS (Decision 12). Single collection, passed explicitly. |
| **1b** | Discovery | shipped | Bounded scan + prune rules + go-git ignore matching + cap + cache, manifest `collections`, non-nesting enforced, `ListCollections`, CLI cwd resolution, `collections ls`. |
| **1c** | Multi-collection UI | shipped | Collection tier, per-collection query keys, `Item.slug` + slug-based `itemKey`, `moveSubtree`-on-rename deleted, scoped Sources/Scripts, cross-collection drop rejected. |
| **1d** | The descriptor store | shipped | Decision 7 and 9 together: blobs + CAS, `commit_descriptors` + sidecars, upload's special case deleted, in-memory merged view keyed by collection with writer invalidation, `services.json` deleted, lazy first-touch merge, workspace-level definitions + references + `origin`. |
| **1e** | `Upload.path` and the `Bazel` kind | shipped | A refresh recipe on upload (CLI records it, browser leaves it empty, refresh re-reads), bazel root discovery, argv exec, cquery output resolution, dedupe-before-link, timeout/output handling, the three path/symlink rules, trust gate. |

**What 1e shipped differently from the text above.** The decisions held; five details did
not survive contact, and this is the list:

- **The CLI takes only a full `//`-prefixed (or `@`-prefixed) label**, never bazel's
  `proto:foo` shorthand this doc uses in passing. `localhost:8080` is indistinguishable from
  it, so accepting it would sometimes dial a label and sometimes build an address. The
  *server* still canonicalizes the shorthand, so a hand-written manifest may use it.
- **An upload path that does not confine to the workspace root records no recipe instead of
  failing the add.** The bytes are already in the request, so the source lands, unrefreshable,
  with a warning — which is the "a dead path costs a refresh and nothing else" rule applied to
  the add as well. Confinement is strict on the read side, where it matters.
- **Trust is a verb, `grpcview trust [--off]`**, not the `--trust` flag suggested above: it is
  a decision about a workspace root and not a modifier of some other operation.
- **The untrusted note lives on `sources ls`**, which this doc left open. `collections ls` was
  the other candidate and says nothing: it reads manifests and never source lists, so it
  cannot know whether anything in the workspace would build.
- **`invoke --refresh` was not built.** The CI pipeline is
  `grpcview sources refresh && grpcview invoke …`, which is this doc's own first option.

**1a moves the state files; 1d restructures them.** 1a relocates
`cache/sources/*.binpb`, `cache/services.json` and `history/` to the new state root
unchanged — same layout, same codecs, new parent. Content-addressing, the sidecars and the
deletion of `services.json` are 1d's job. One move then one restructure is more churn than
doing it once, and it is what keeps each step independently verifiable; an agent doing 1a
should not be building CAS.

1d before 1e is deliberate: the store is the thing both new kinds write into, and building
it the other way round hides the general primitive inside the specific one. The daemon
([`daemon.md`](../../shipped/daemon.md)) depends only on 1a and can land any time after it — the VS
Code extension is its second customer.

## Watch out

- **`readChildren` reads configs for ordering only** (`fs.go:593`). Discovery must not
  route through it, and `ListCollections` must not load trees.
- **Two processes, one collection, no lock** is currently accepted (`AGENTS.md`, "The CLI").
  1a–1e make concurrent writers *likelier* (more collections, more surfaces); the daemon is
  what actually fixes it by funnelling every surface into one process. Until it lands the
  wart is worse than today, not the same — see [`daemon.md`](../../shipped/daemon.md).
- **`AGENTS.md` will need editing when the daemon lands**, in two places: "no
  autodetection" and the no-lock wart. Do not edit it before then — it documents shipped
  behavior, and these documents are the record of the intent.
- **`grpcview.work.json` vs `grpcview.json`** differ by five characters in a listing —
  accepted, after `go.work`. Both names carry the product name, and **the product is going
  to be renamed**, so keep the two filenames in *one* place (`layout.go:14-29` already
  holds every other managed name) rather than spelling them at each use site.
- **Uploads keep working unchanged from the user's side.** A drag-and-drop `buf build`
  image is still the right answer for a schema that is not built in this repo; what changes
  is where its bytes are stored and that it can now be refreshed when a path is known.
- **`ui:dev` breaks on a non-fixed port** — `client.ts:4` hardcodes `127.0.0.1:10000` for
  non-PROD. Relevant to the daemon rather than to 1a, but whoever touches the port must run
  the vite flow, not just the prod binary.
- **No Windows.** The reserved-slug table, `.bazelrc`, `storage.md` and phase 5's `.vsix`
  targets are all shedding it; nothing here should add a `GOOS=windows` branch.

## Verify

- `bazel test //...` — not `//service/...`. The rename touches the UI, and the generated
  TypeScript declarations are committed.
- **After any `.proto` edit:** `bazel run //proto/grpcview/v1:grpcviewv1_ts_proto.copy`,
  then the three frontend gates from `AGENTS.md` — `cd ui && ./node_modules/.bin/tsc
  --noEmit -p tsconfig.json`, `bazel test //ui:test`, `bazel build //ui:ui`.
- Scratch dirs: traversal rejection (`..`, absolute), case-fold handle identity
  (`requests` and `Requests` are one handle), scan pruning and the cap, a collection at the
  workspace root (`id` = `.`), and `git status` clean inside a repo with a pre-existing
  `.gitignore` — now because the collection writes nothing local, not because an ignore
  rule covers it.
- **Nothing is created implicitly:** `Get` on a non-existent collection id returns
  `NotFound` and leaves no directory behind. Assert on the filesystem, not just the error.
- **Dogfood in this repo**, which the old plan could not: workspace root = the repo (it has
  `.git`), collection = `requests/`, and confirm nothing lands at the root or in
  `~/.config`.
- **Bazel source, end to end, through the CLI** (`AGENTS.md`: verify through the CLI):
  point a collection at `//proto/echo/v1:echov1_proto`, `grpcview sources refresh`, then
  `grpcview describe echo.v1.EchoService/Unary -o proto` — it must resolve without any
  reflection source, and **name the bazel source** as where the descriptors came from. Add
  a doc comment to `echo.proto`, rebuild, and confirm it appears **after an explicit
  refresh** and not before (there is no load-time acquisition).
- `grpcview invoke` against `//service/echo/cmd` with the bazel source as the *only*
  descriptor source, checking exit codes 0 and 1 — that is the claim that reflection is no
  longer a precondition for calling anything.
- **`--commit-descriptors` round-trips byte-stably:** refresh twice with no upstream change
  and `git status` stays clean. This is what the `DiscardUnknown` normalisation is for.
- **The in-memory cache does no I/O on a hit** — two invokes in one process, with the
  second doing no `open`/`stat` of the store. Easiest as a store-level counter asserted in a
  test rather than by inspection.
- Browser: a two-collection workspace shows two rows; a one-collection workspace shows
  none, and the tree looks exactly as it does today. Rename a folder and confirm expansion
  and drafts survive — the assertion that keys are slug-based.

## Open questions

- **No repo, no collection.** First run in a random directory lands on an empty workspace
  rooted at the cwd. Offer a **scratch collection** in user state (VS Code's untitled
  window), or only "create one here"? Leaning scratch — it keeps "poke a server in thirty
  seconds" intact.
- **Shared scripts.** The same argument as shared sources applies to `scripts/`, and the
  manifest is the obvious home. Deferred deliberately: phase 2 makes bodies real files that
  can `import "../../shared/auth.ts"`, which is the better answer to the same need. Decide
  after phase 2, not here.
- **`descriptor_set` on `Get`.** Serving it from memory is cheap, but it is still megabytes
  per collection **over the wire**, so this stays a wire-size question: split into
  `GetDescriptors(collection)` now, or when it hurts?
- **Multi-root workspaces.** VS Code has them; nothing here needs them. Ids are
  root-relative paths, so a later multi-root would prefix `<root-name>:`. Left unbuilt on
  purpose.

Closed since the first draft: **async resolve** (no — 1e ships the synchronous build; the
async `Resolved.state` machine lands with the ibazel-style watcher, since nothing else needs
it and there is no polling in this phase). **Toggling `--commit-descriptors` on for a
never-resolved source** (reject — it is `InvalidArgument` naming `refresh`, because
resolving as a side effect of a config change is acquisition by another name).

## What changed from the original plan

The original phase 1 collapsed the workspace-name layer: one collection per process,
rooted at the cwd, with `workspace_name` deleted from every RPC. Three things pushed the
other way.

1. **A monorepo has more than one collection.** `services/payments/requests` and
   `services/ledger/requests` are one repo the user opens once. One-per-process would mean
   one process per team, or a collection at the repo root owning everything.
2. **The field was not dead weight, it was under-used.** Reinterpreting `workspace_name`
   as a workspace-relative path keeps every handler's shape and shrinks the mechanical
   diff to a rename — while *adding* the axis a monorepo needs. (`docs/design/planned/mcp/` has
   since been corrected on this.)
3. **The "watch out" was a design smell.** "Do not point this at your repo root or four
   entries scatter among your project files" is a warning that a layer is missing. With a
   workspace tier the repo root is exactly what you point it at, and it stays clean.

Kept from the original: the root override (now spelled `--workspace`), `store.New(dir)`
rooting at the base rather than `<base>/<name>`, the two-handles-one-directory hazard, and
the `.gitignore` bug — though that one is now solved by removing local state from the
collection entirely rather than by writing a better ignore file.
