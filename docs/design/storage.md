# grpcview storage layer — design

Status: **implemented.** The git-versionable directory-tree storage described here
has shipped and replaced the SQLite-blob storage; this doc is now the design
rationale + layout reference (some phased-plan notes below are historical).

## 1. Goal & context

grpcview is "Postman, tailored for gRPC." We are rewriting the **storage layer**
so that a workspace/collection is a **git-versionable directory tree** the user
can open from any location and commit alongside the project it tests. This is the
Bruno model (plain files on disk) applied to gRPC.

Locked decisions (from design Q&A):

- **Addressing:** a workspace = **any directory** the user opens. The UI is
  **git-aware** (backend serves git status/diff/history to the frontend, prefer
  `go-git`).
- **File format:** **JSON via `protojson`** (canonical mapping from the existing
  proto messages; request bodies are already JSON in the UI).
- **Granularity:** **a folder = a directory, a request = a directory** (a
  `protojson` config file per item, draft body inlined as a JSON `string`) for
  clean diffs.
- **Versioning split:** **config in git; run-history, resolved-schema cache, and
  secrets in a local, gitignored state dir.**

Two consequences of dropping the DB that this design must solve explicitly:

1. **Inheritance** — folder-level auth/metadata/scripts/variables/target that
   cascade to child requests (a DB modeled this for free).
2. **Linked/shared resources** — auth profiles, environments, targets, descriptor
   sources referenced from many places, with no FK integrity on a filesystem.

## 2. Current state (what we replace)

- `service/workspace/workspace.go` opens `/tmp/ws.db` (SQLite) but **never uses
  it** — dead code. Real persistence = the whole `Workspace` proto serialized as
  **one opaque binary blob** at `~/.grpcview/<name>`.
- Every mutation: load whole blob → mutate in memory → write whole blob; every
  RPC returns the **entire** workspace; the React store swaps it in wholesale.
- The tree model already maps 1:1 to a filesystem: `Item{name, oneof{Folder,
  Request}}`, path-addressed via `findItem/findFolder/findFile`.
- **SQLite is the only cgo dependency in the repo.** Removing it makes the storage
  + git stack cgo-free (go-git is pure Go), simplifying the static-binary Bazel
  toolchain story.

Reusable "bones": the proto data model, the Connect RPC surface, and
reflection-based descriptor fetching. Delete: SQLite and the single-blob
persistence.

## 3. Design principles

- **Plain data, app-owned mutations.** Files are human-readable and hand-editable,
  but the app performs structural mutations (create/rename/move/delete, ref
  rewrites) so it can keep references consistent — the main mitigation for losing
  DB referential integrity.
- **Diff-first.** Configs are real `protojson` files; the draft body is a JSON
  `string` field (plain text, not a base64 blob). Reordering/renaming produces
  minimal diffs.
- **Config vs. state separation.** Anything reproducible or ephemeral (resolved
  schemas, run history, secrets, UI state) is local and gitignored.
- **Forward-compatible layout now, phased implementation.** Phase 1 is a pure
  persistence swap that keeps the current API/UI working; the layout already
  reserves places for inheritance/linking/git so later phases don't re-lay-out.

## 4. On-disk layout

A collection is the directory the user opens. Proposed layout:

```
my-collection/                 # opened by the user; own git repo OR nested in one
  grpcview.json                # collection manifest: schemaVersion, name, settings, defaultEnvironment
  .gitignore                   # generated; ignores .grpcview/ (local state)
  tree/                        # the request tree (folders + requests) lives here
    folder.json                # optional root folder config (inherited by everything)
    Users/                     # a folder = a directory
      folder.json              # folder config: seq + optional auth/metadata/target/vars/scripts
      GetUser/                 # a request = a directory
        request.json           # service, method, draft_body (JSON string), + optional target/auth/metadata/timeout/vars refs
        metadata.json          # optional draft metadata (or inline in request.json)
      ListUsers/
        request.json
  environments/                # shared: named environments (non-secret values only)
    local.json
    prod.json
  targets/                     # shared: reusable servers (host:port + TLS)
    default.json
  auth/                        # shared: reusable auth profiles (non-secret parts)
    bearer-dev.json
  proto/                       # descriptor sources config (+ optional committed set)
    sources.json               # [{reflection: {targetRef|host,port}} | {descriptorSet:"bundle.binpb"}]
    bundle.binpb               # optional committed FileDescriptorSet for offline/reproducible schema
  .grpcview/                   # LOCAL STATE — gitignored
    state.json                 # active environment, last-opened, UI state
    history/tree/Users/GetUser/2026-07-16T....json   # run history, keyed by request path
    cache/descriptors/         # resolved schemas (regenerated from sources/reflection)
    secrets/local.json         # resolved secret values for env "local" (never committed)
```

Why `tree/` (not requests at the root, Bruno-style): keeps the collection root
clean and avoids name collisions between user folders and reserved dirs
(`environments/`, `proto/`, …). Flat-at-root is the alternative if we prefer
Bruno-exact ergonomics; then reserved names become forbidden folder names.

## 5. File formats & conventions

- Every managed file is a **`protojson`-encoded** message (deterministic,
  round-trips through the existing proto types, no new dep — uses
  `google.golang.org/protobuf/encoding/protojson`).
- **The draft body is a `draft_body` `string` field in `request.json`** — a plain
  JSON string, not `bytes`, so `protojson` writes the text directly instead of a
  base64 blob (the body is always UTF-8 text typed in Monaco). Folding it into the
  request config (rather than a separate `body.json` sidecar) keeps one file per
  request and a single read/write path; the trade-off is that a body edit shows as
  one escaped line in the diff rather than raw multi-line JSON.
- **Naming / identity (LOCKED — slug dir + display name in config):** each item's
  directory is named by a **stable slug**; the human display name lives in
  `meta.name` inside the item's config file. No separate mapping file — the dir name
  *is* the slug, the config *is* the name.
  - Slug derived from the display name on create (`"Get User"` → `get-user`), made
    unique within its parent (`-2`, `-3`, …) and filesystem-safe (sanitize `/ : \0`,
    reserve config names, enforce case-insensitive uniqueness on macOS/Windows).
  - **Renaming the display name only edits `meta.name`; the slug/dir is stable** — so
    identity (and any references) survive a rename. Moving an item = `mv` its dir.
  - Gives tree items stable identity for free, on top of the logical-key refs used
    for shared resources (§7).
- **Ordering (LOCKED — ordered list in the parent):** each folder's config
  (`folder.json`, or `collection.json` for the root) holds an ordered `items: [slug,
  slug, …]` list of its children.
  - A reorder edits exactly one file (no multi-file `seq` churn — the reason this was
    chosen); create appends a slug; delete removes one.
  - **Reconciliation on load** (the list can drift from disk via git merge or manual
    edits): slugs in the list but missing on disk are dropped (warn); dirs on disk
    absent from the list are appended in name order. Keeps the tree self-healing.

## 6. Inheritance & resolution model

Three mechanics (validated against Bruno/Postman/Insomnia):

- **Structural props** (target, auth, deadline, descriptor source) — **replace**,
  **nearest-in-tree wins**.
- **Map props** (metadata/headers, variables) — **merge** down the whole chain,
  per-key winner by precedence, with an `enabled:false` **tombstone** to disable an
  inherited key.
- **Scripts** (later) — **chain** (every level runs), not replace.

Chain (most -> least specific):

```
request -> nearest folder -> ... ancestors ... -> root folder -> collection -> active environment -> built-in default
```

Per-property semantics:

| Property | Cascades | Rule | Winner order |
|---|:--:|---|---|
| Target (host:port, TLS) | yes | replace | request -> folder(near->root) -> collection -> env `default_target` -> error |
| Auth / call-creds | yes | replace | request -> folder -> collection -> env `default_auth` -> none |
| Metadata / headers | yes | **merge**; `enabled:false` tombstone disables an inherited key | request > folder(near->root) > collection > env |
| Deadline / timeout | yes | replace | request -> folder -> collection -> target default -> built-in |
| Variables | yes | **merge** (per key) | script/runtime > request > **active-env** > folder > collection *(env-above-tree is Postman-style — open Q, §15)* |
| Descriptor source | yes | replace active (collection holds the pool in `descriptors/`) | request -> folder -> collection |
| Pre/post scripts | yes | **chain** (pre: collection->folder->request; post reversed) | deferred — needs JS runtime |

**Binding model (`INHERIT` / `NONE` / `SET`):** each config field is a small binding:
- **absent** = `INHERIT` (a clean `folder.json` that only sets one header is tiny and
  diffs beautifully — you pay bytes only for what you override);
- `{"mode":"none"}` = **explicitly disabled**, stops the cascade (e.g. "no auth
  here" — matches Postman/Bruno `auth{mode:none}`);
- `{"mode":"set","ref":"prod"}` or `…,"inline":{…}` = **override** (shared-key ref or
  inline value).

**Model fix — separate `target` from `descriptor source`.** Today they're conflated
(`DescriptorSource.reflection = Server`). A **target** (host:port + TLS + the auth
used to reach it) is a distinct, inheritable/shareable thing from a **descriptor
source** (where schemas come from: reflection / protoset / proto files). A reflection
source *references a target key* to make its reflection call — which is what lets
auth/TLS/metadata be reused for both the RPC and its reflection.

MVP subset: target + metadata (merge+tombstone) + deadline + descriptor
(nearest-wins) + auth limited to bearer/raw-metadata. Defer scripts, OAuth2/mTLS UX,
and variable-precedence tuning. Full detail:
`docs/design/research/inheritance-linked-resources.md`.

## 7. Shared / linked resources

Candidates: targets, auth profiles, environments, descriptor sources, (later)
body/snippet templates.

**Chosen representation: reference by *logical key* within a well-known namespace
dir — not by raw filesystem path, not by opaque id+manifest, not by symlink.**

- `targetRef: "default"`  → `targets/default.json`
- `authRef: "bearer-dev"` → `auth/bearer-dev.json`
- `environment: "prod"`   → `environments/prod.json`

Rationale vs. alternatives:

| Option                    | Diff-readable | Survives rename | Integrity | Windows/git | Verdict |
|---------------------------|:-------------:|:--------------:|:---------:|:-----------:|---------|
| **Logical key + known dir** | ✅ (`"default"`) | app rewrites refs | validate on load | ✅ | **chosen** |
| Relative FS path          | ⚠️ ugly deep  | ❌ brittle       | none      | ✅          | no |
| Stable id + manifest      | ❌ opaque      | ✅              | ✅ (manifest) | ✅       | overkill for now |
| Symlink                   | n/a           | ❌              | native    | ❌ poor     | no |

- **Decisive advantage (confirmed by the research): keys resolve from the collection
  root, not from the referrer** — so the most common user op, reorganizing the tree
  (dragging a request into another folder), never breaks refs. Relative paths break
  exactly on that move. The one cost is that *renaming a shared resource* is
  O(referrers) — handled by app-driven rename (rewrites all refs atomically) and
  broken-link surfacing for manual `git mv`. This is Kreya's id-ref model made
  human-readable.
- The namespace dir **is** the manifest (no separate index to drift).
- **Broken-ref handling (the FK-integrity mitigation):** on load, resolve all refs;
  collect unresolved ones; return them to the UI as warnings/badges on affected
  requests. App mutations that delete/rename a shared resource either rewrite all
  referrers or block with a usage list.
- Refs only point "up" into top-level shared dirs, so tree items can't form cycles;
  shared→shared refs are validated acyclic.

**Phase 2 intent — RECORDED** (follow-up FS-refs exploration —
`docs/design/research/fs-refs-and-secrets.md`): upgrade a bare logical key to
**id-primary + path-hint**. Each shared resource embeds a stable `id`; a ref stores
both the `id` (authoritative) and a readable `path` hint, e.g.
`"auth": { "ref": "auth_ab12", "path": "auth/prod.json" }`. Resolve by id first, fall
back to the hint, and **auto-heal + warn** on disagreement (catches a git add+delete
rename). The id→path index is *derived by scanning* into the gitignored state dir —
no committed manifest to merge-conflict. This also survives **renaming the shared
resource itself** (pure logical keys don't) and is consistent with the slug-identity
choice for tree items (§5). Cost: an opaque id in the file (mitigated by the readable
hint) + a scan-built index. **Recorded as the Phase 2 direction — build it then, not
in Phase 1** (Phase 1 exercises no shared-resource refs); re-confirm the exact `id`
format at implementation time. Avoid symlinks for committed refs (Windows
`core.symlinks=false` silently checks them out as text files).

## 8. Git: committed vs. local

- **Committed:** `grpcview.json`, `tree/**` (folder/request configs incl. the draft
  body + metadata), `environments/*.json` (non-secret values), `targets/*`, `auth/*`
  (non-secret parts), `proto/sources.json`, optional `proto/bundle.binpb`.
- **Gitignored (`.grpcview/`):** `history/`, `cache/` (resolved descriptors/
  schemas — reproducible from sources), `secrets/`, `state.json`.
- **Secrets:** committed files mark a field `secret: true` with no value; the real
  value lives in `.grpcview/secrets/<env>.json` (later: OS keychain).

## 9. Backend architecture

Package layout:

```
service/
  store/
    store.go     # Store interface + domain types
    layout.go    # path<->disk mapping, name sanitization, seq ordering, reserved names
    codec.go     # protojson (de)serialize; atomic write-temp-rename
    fs.go        # filesystem-backed Store
    resolve.go   # inheritance + ref resolution + broken-ref detection (phase 2)
    store_test.go
  git/           # phase 3: go-git/exec wrapper + GitService handler
  workspace/     # RPC handlers; depend on store.Store; rename package to `workspace`
  service.go     # wire-up (drop sqlite)
  logging.go
```

`Store` interface (Phase-1 subset first; grows later):

```go
type Store interface {
    Open(ctx context.Context, root string) (*Collection, error)  // open/create collection at a dir
    Load(ctx context.Context) (*grpcviewv1.Workspace, error)     // assemble full tree (keeps current API)
    CreateFolder(ctx context.Context, path []string, name string) error
    CreateRequest(ctx context.Context, path []string, req *grpcviewv1.Request) error
    UpdateRequest(ctx context.Context, path []string, patch *RequestPatch) error
    Delete(ctx context.Context, path []string) error
    // rename is a field on RequestPatch (via UpdateRequest), not a separate Move
    // phase 2+: shared-resource CRUD, environments, Resolve(path) -> effective config
}
```

- **Phase 1 keeps the RPC handlers returning the whole `Workspace`** (reload the
  tree after each mutation). The React UI is unchanged. The only thing that changes
  is persistence: blob → directory tree.
- **Atomicity:** write to a temp file + `rename` per file; a per-collection
  in-process mutex (optionally an flock) serializes mutations.

## 10. Git-awareness

Backed by a completed `go-git` exploration (full report:
`docs/design/research/go-git.md`).

**Recommendation: go-git-first, with an optional exec-`git` upgrade path.** Pin
**`github.com/go-git/go-git/v5` v5.18.0** (v6 is alpha — a transport rewrite; only
relevant if/when we do network sync). go-git is **pure Go / CGO-free**, so it
embeds in the static binary with zero runtime dependency — this must be the
baseline, because a gRPC tool can't assume a `git` binary is installed (Windows,
minimal containers, locked-down machines). Detect a `git` binary on `PATH` at
startup and use it only where go-git is weak/slow (below). Keep both behind one
`gitBackend` interface (`goGit`, `execGit`) chosen per-op.

- **MVP ships go-git-only** (read-only + branch display). The exec upgrade is
  phase 2+, for the few ops where it matters.

**go-git capability gotchas that shape the design:**

- **No worktree-vs-HEAD / staged-vs-HEAD diff-patch helper.** → `GetDiff` returns
  **old text + new text**, and the UI renders it with **Monaco's `DiffEditor`**
  (already shipped in `ui/`). Sidesteps the gap and is nicer for small protojson
  files. Server-rendered patches are only used for commit-to-commit history
  (`tree.Patch().String()` works there).
- **`Worktree.Status()` is the perf bottleneck** (walks + hashes the whole
  worktree; descends into ignored/nested dirs). Mitigate: sparse/`Empty`
  `StatusStrategy` (changed files only), a **single cached call + prefix-index
  rollup** (never per-item), ignore hygiene, and scoping to the workspace subdir
  when nested in a monorepo. Main case for the exec-`git status --porcelain=v2`
  fallback.
- **No per-file unstage/discard** (`Reset`/`Checkout` are whole-tree) → exec
  `git restore [--staged] <file>` when available; coarse go-git fallback otherwise.
- **Commit identity**: go-git reads `user.name`/`user.email` from git config and
  **fails if unset** → need a settings fallback/prompt.
- **Ahead/behind not built-in** (compute via merge-base; needs a fresh fetch) and
  **no credential-helper support** for push/pull → defer sync; favor exec-`git`
  there (reuses the user's stored credentials).

**New `GitService`** (separate from `WorkspaceService`; git is optional/separable,
with its own repo-handle + status cache + fs-watch lifecycle). It reuses the exact
existing addressing so the frontend reuses its path model:

```
message ItemRef { string workspace_name = 1; repeated string path = 2; string item_name = 3; }

service GitService {
  rpc GetRepoInfo(RepoInfoRequest) returns (RepoInfoResponse);   // is_repo, branch, subdir, git_binary_available
  rpc GetStatus(StatusRequest) returns (StatusResponse);         // pre-aggregated per-item badges (dirty only)
  rpc GetDiff(DiffRequest) returns (DiffResponse);               // old_text/new_text per file -> Monaco DiffEditor
  rpc GetLog(LogRequest) returns (LogResponse);                  // commits touching a path
  rpc ListBranches(ListBranchesRequest) returns (ListBranchesResponse);
  // phase 2 (writes, local): Stage, Unstage, Discard, Commit, CreateBranch, SwitchBranch
  // phase 3: WatchStatus (server-stream on fs changes); Fetch/Pull/Push (auth -> exec-git favored)
}
```

- **Per-item badge aggregation:** one sparse `Status()`; for each changed file
  derive a badge and merge it into every ancestor dir up to the workspace root
  (worst-wins precedence: `conflict > staged+modified > staged > modified > new >
  clean`). Item query = prefix lookup; absent ⇒ clean. Cache keyed by `{repoRoot,
  HEAD hash, index mtime}`; invalidate on our mutations + WorkspaceService writes;
  phase 3 backs it with `fsnotify` + `WatchStatus` streaming.
- **MVP:** `GetRepoInfo` + `GetStatus` (badges) + `GetDiff` (worktree-vs-HEAD →
  Monaco). Zero write risk, zero network/auth. Phase 2: log + stage/commit/discard/
  unstage + branches. Phase 3: fs-watch streaming + fetch/pull/push.

New git-specific open questions from the exploration are in §15.

## 11. Proto / API changes

- **Phase 1:** no proto changes required — keep the current `WorkspaceService` and
  `Workspace`/`Item`/`Request` messages; only persistence changes.
- **Phase 2 (config model):** define the on-disk shapes as protojson messages (the
  repo is proto-first). Reuse today's `Server`/`DescriptorSource`/`Service`/`Method`/
  `Message` where possible; add:

```proto
message Binding { enum Mode { INHERIT = 0; NONE = 1; SET = 2; } }   // INHERIT == field absent

message TargetBinding     { Binding.Mode mode = 1; string ref = 2; Target inline = 3; }
message AuthBinding       { Binding.Mode mode = 1; string ref = 2; Auth   inline = 3; }
message DeadlineBinding   { Binding.Mode mode = 1; google.protobuf.Duration value = 2; }
message DescriptorBinding { Binding.Mode mode = 1; repeated string refs = 2; }
message MetadataEntry     { string key = 1; string value = 2; bool enabled = 3; bool binary = 4; }

message Config {                    // collection.json / folder.json / embedded in request.json
  ItemMeta meta = 1;               // name, order, tags, docs
  TargetBinding target = 2;
  AuthBinding auth = 3;
  repeated MetadataEntry metadata = 4;   // merged; enabled:false = tombstone
  DeadlineBinding deadline = 5;
  DescriptorBinding descriptor = 6;
  map<string,string> vars = 7;
  Scripts scripts = 8;             // reserved
}
message RequestFile {              // request.json
  Config config = 1;
  string service = 2; string method = 3;
  string draft_body = 4;           // JSON string (not bytes; not a base64 blob)
  RpcType type = 5;                // UNARY | CLIENT_STREAM | SERVER_STREAM | BIDI
}

message Target { string host = 1; int32 port = 2; TLS tls = 3; google.protobuf.Duration default_deadline = 4; }
message TLS  { bool enabled=1; bool insecure_skip_verify=2; string ca_cert=3; string client_cert=4; string client_key=5; string server_name=6; }
message Auth { oneof kind { BearerAuth bearer=1; BasicAuth basic=2; ApiKeyAuth apikey=3; OAuth2Auth oauth2=4; MetadataAuth metadata=5; } }
message Environment { string name=1; map<string,string> vars=2; repeated string secret_vars=3; string default_target=4; string default_auth=5; }
```

- **Streaming forward-compat:** add `RpcType`; allow the draft body to hold an array
  of messages (`{"messages":[…]}`) so unary→streaming isn't a later format break. No
  streaming UX in MVP.
- **Phase 3:** add `GitService` + `Git*` messages (see §10 and the go-git research
  doc); regenerates cleanly via the existing proto→go+connect+connect-query-ts rules.

## 12. Build system changes

- Remove `github.com/mattn/go-sqlite3` from `go.mod`, `MODULE.bazel` `use_repo`,
  `service/BUILD.bazel:24`, `service/workspace/BUILD.bazel:14`.
- Add `github.com/go-git/go-git/v5` (phase 3) to `go.mod` + `MODULE.bazel` +
  `service/git/BUILD.bazel`.
- `protojson` needs no new dep.
- Stack becomes **cgo-free** → follow-up opportunity to slim the hermetic C
  toolchain (protoc is already prebuilt).
- Run `gazelle` to regenerate BUILD files.

## 13. Migration

Existing data is a single proto blob at `~/.grpcview/<name>`. Provide a one-time
importer (`grpcview import` or auto-migrate on first open): read blob → walk tree →
write the directory layout into a chosen collection dir. Low priority given
early/local/single-user usage, but cheap to include.

## 14. Phasing / roadmap

- **Phase 0 — cleanup:** delete SQLite; fix `package service` → `package workspace`
  in `service/workspace/`. No behavior change.
- **Phase 1 — storage swap (the "start"):** FS-backed `Store` implementing the 6
  current RPCs over a `tree/` of `protojson` files; atomic writes;
  rename/move support; keep API/UI unchanged; migration importer.
- **Phase 2 — inheritance, environments, shared resources:** config messages,
  resolution engine, environments, `targets`/`auth` refs, broken-ref validation
  surfaced to the UI.
- **Phase 3 — git-awareness:** `GitService` (hybrid go-git/exec); status badges,
  diff, log, commit/discard, branch/ahead-behind.
- **Phase 4 — later:** scripts (JS runtime), streaming RPC execution, fs-watch live
  reload, secrets in OS keychain.

## 15. Open decisions to confirm

**Phase 1 (the storage swap) is unblocked** — everything below shapes Phase 2/3.

Storage / layout — **RESOLVED** (see §4/§5):

- ✅ **Tree location:** `tree/` subdir.
- ✅ **Item identity:** slug dir + `meta.name` display name (stable identity).
- ✅ **Ordering:** ordered `items:[slug]` list in the parent's config (one-file
  reorder; reconciled with disk on load).

Still open:

4. **Scripts:** defer to Phase 4 (recommended; needs a JS runtime) vs. earlier.

Config / inheritance (from the inheritance exploration):

5. **Variable precedence:** active-env *above* tree vars (Postman-style, recommended
   — switching env reliably changes values) vs. *below* (Insomnia-style).
6. **Environments overriding structural props:** env supplies variables + a
   bottom-of-chain default only (recommended) vs. env may directly override
   target/auth.
7. **Secrets / cert material:** client key + tokens in gitignored `.grpcview/`
   (recommended) vs. OS keychain now; a committed `certs/` dir for CA/public certs?
8. **Descriptor cache:** local-only (recommended, clean diffs; needs reflection/proto
   access to use) vs. optionally commit `descriptors/*.pb` for offline/no-server use.
9. **Metadata disable:** `enabled:false` tombstone entries (recommended) vs. a
   separate `removeMetadata:[keys]` field.
10. **Collection discovery:** find the collection root by walking up to the nearest
    `grpcview.json` (like `.git`) — confirm.

Git (from the go-git exploration):

11. **Git backend:** go-git-only for MVP with an exec-`git` upgrade path
    (recommended) vs. go-git only forever vs. exec-first.
12. **Nested-in-monorepo:** support a workspace that is a *subdirectory* of a larger
    repo (scope status/diff/commit to that subtree) vs. only when it *is* the root.
13. **Auto-init:** offer `git init` on a plain directory vs. only show git UI for
    existing repos.
14. **Commit identity & sync auth:** git config vs. a settings screen; and for
    push/pull, require the `git` binary (OS credential helpers) vs. a token/SSH UI.

## Follow-up items (Phase 2, non-blocking)

15. **Shared-resource refs — DECIDED (intent recorded; build in Phase 2):** adopt
    **id-primary + path-hint + derived index** (survives renaming the resource;
    consistent with the slug-identity choice for tree items). Deferred — Phase 1 has
    no shared-resource refs to exercise it. See the §7 note.
16. **Secret storage backend:** `99designs/keyring` (recommended — native keychains +
    encrypted-file fallback for headless/CI + documented size limits) vs.
    `zalando/go-keyring` (simpler, no file fallback). Reference syntax `{{secrets.X}}`
    (keychain/state) + `{{process.env.X}}` (dotenv, Bruno-proven), keyed by
    `collectionId + name` (never path).

## Verification status

All explorations completed against primary sources; full reports under
`docs/design/research/`.

- **Inheritance & linked resources:** verified (`inheritance-linked-resources.md`) —
  Bruno, Postman, Insomnia v5, Kreya, grpcurl. Logical-key refs,
  auth=replace/metadata=merge, scripts=chain, secrets-by-name are confirmed
  conventions.
- **go-git:** verified (`go-git.md`). Confirm the exact `StatusStrategy` constant
  against pinned v5.18.0 before coding.
- **FS refs & secrets (follow-up):** verified (`fs-refs-and-secrets.md`) — JSON Schema
  `$id`/`$ref`, RFC 6901, Insomnia/Bruno/Postman/VS Code/pnpm/Bazel ref models,
  `zalando/go-keyring` vs `99designs/keyring`, XDG dirs.

**Security note:** across runs the research agents repeatedly encountered
prompt-injection attempts — in fetched third-party web pages *and* in nested
subagent output (a fake "was this written by Claude" task; a fake `security-review`
tool). All were correctly ignored; every finding traces to a cited primary source.
That untrusted content reached agents via both the web and the subagent-result
channel is worth a separate look at the harness.
