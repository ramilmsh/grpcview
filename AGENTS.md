# Project Overview

`grpcview` is a gRPC client — a Postman-like tool for exploring and calling
gRPC services: reflects a server's schema, lets you author requests, invokes
them, from a single self-contained binary.

Core decision: **requests are authored as TypeScript.** A body is a typed TS
object literal, checked in-browser against the method's input message;
metadata is authored the same way; both are *evaluated* in a sandboxed JS
engine at invoke time, so they can call user scripts.

**TypeScript is the authoring affordance, not the contract: a body is
protojson**, accepted everywhere a body is. See
[`docs/design/request-body-contract.md`](docs/design/request-body-contract.md).

## Project Stage

No users yet — pre-release. **Simplicity over backwards compatibility.** No
migrations/compat shims/`reserved` markers. Delete dead code on sight.

## Working in this repo

- **Bazel workspace. Never run bare `go build`/`test`/`run`/`mod`** — they
  reach the network, hang, can wedge git.
- `.envrc` sets `GOPROXY=off` — offline by default; a network-wanting
  command is buggy, not missing a dependency.
- Default shell is fish; for bash syntax use `bash -c '...'`.
- **`bazel clean --expunge` is not a fix** — Bazel is hermetic, no stale
  cache. Retry, or `bazel fetch @broken_repo`; a repo hook pauses to confirm.

## Delegating to background agents

Main thread orchestrates/verifies/commits; agents write code. Cache-read
cost scales ~`1300 × turns²`: scope an agent to ~40 turns, pre-load context
(paste excerpts/paths, don't make it grep), cap verify loop at 2 tries then
report and stop, one reviewer handed `git diff` directly, `effort` one
tier below the session's (floor `medium`), verify via CLI unless UI-only.

False-pass traps: a new `.go` file not in `BUILD.bazel` `srcs` isn't
compiled; Bazel caches test results — use `--nocache_test_results` and
check the count changed.

## Architecture

- **Frontend** (`ui/`): React 18 + TS SPA, Vite, single HTML file,
  embedded into the Go binary.
- **Backend** (`service/`): Go server, `WorkspaceService` over Connect
  (h2c) — reflection, invoke, disk persistence, scripting.
- **Store** (`service/store/`): filesystem-backed collection, protojson
  tree; on-disk schema (`grpcview.store.v1`) decoupled from wire
  (`grpcview.v1`), bridged by `convert.go`.
- **Scripting** (`service/scripting/`): QuickJS-WASM (wazero), user JS/TS.
  Network always on; filesystem deny-by-default behind a `Grant`
  (`node:fs`); sources bundled with esbuild first.

## Request authoring model

Core of the product.

- Body: a bare TS object literal in Monaco, edited inside `//
  grpcview:script start`/`end` markers inside `export default async ():
  Promise<RequestMessage> => ( … )`. Leading `{` stays wrapped, else
  plain (backend still wraps a bare expression). Imports above the
  marker are auto-managed in wrapped mode; author-owned in plain mode.
- Metadata authored identically under `Promise<Metadata>`; filename picks
  the skeleton.
- Both evaluated in QuickJS at invoke time, persisted beside
  `request.json`, written verbatim with one trailing newline — always
  `.ts`. **Do not normalize on read**: untouched files round-trip
  byte-identical. Absent `body.ts` reads as `{}`; **empty `metadata.ts`
  means "inherit the folder chain"**. `draftBody`/`draftMetadataScript` in
  `request.json` fails to load loudly.
- Plain protojson is equally valid — a **module** or **expression**
  (wrapped by `wrapExpressionScript`, `service/workspace/invoke.go`, the
  one seam, opening no new line so bundler errors still name the
  author's line).
- Scripts are `.ts` under `scripts/`, esbuild-bundled, run under a grant;
  path is identity.

### The `grpcview:` modules

Imports, not globals. `invoke` (`grpcview:invoke`, `(path, params?) =>
Promise<InvokeResult>`) runs another saved request through the same
pipeline — a gRPC-status failure **resolves** `ok:false`, rejecting only
for unknown path/streaming target/un-evaluable body/depth cap. `inherit`
(`grpcview:metadata`) returns merged ancestor-folder metadata,
unconditional (transitivity is userland `{...inherit(), …}`). `assert`
throws `AssertionError` on falsy — sync path throws synchronously, a
thenable condition returns a promise. `params` (`grpcview:request`) is
the invoke-time params object. All degrade gracefully with no context.

### Imports, sigils, and typed paths

A script's path is its identity. `@/…` resolves against the workspace
root; `#/…` against the compiling script's collection root. `import`
can't stand in expression position — bare-object bodies use
`require("…")`, modules use `import` (UI wrapper spares the author
this); computed specifiers are rejected before the build.
`Request.middleware` holds specifiers, not names. `invoke`/`require` are
generic over their path literal — it completes inside the quotes and
infers the real response/export type (the checker won't follow `paths`
from a call expression). Degradation is the point: no descriptor set /
unresolvable symbol / computed specifier → `any`, never a false error.

## Definition sources (where schemas come from)

Services and `descriptor_set` are derived, never authored — from a
priority-ordered list of sources (reflection targets, uploaded
`FileDescriptorSet`s, bazel labels), merged by
`service/workspace/sources.go`. Each resolves independently, storing per
`commit_descriptors`: **off (default)** — content-addressed blob under
the workspace state root, shared across collections; **on** — protojson
in the collection, so a fresh clone resolves offline (sticky: on but
never off again — `sources commit --off` is the escape). Merged view is
derived in memory per collection on first touch (`mergeSources`, front to
back — first source to define a file/serve a service wins); nothing
persisted.

Order is precedence only, never recency. Identity is config-derived
(`store.SourceID`: `reflection:<address>[+tls]`, `upload:<file name>`,
`bazel:<canonical label>`); re-adding refreshes in place, new ones append
at lowest priority. No resolved definitions → `FailedPrecondition`. A
service's dial target is independent of who won its descriptors —
`Service.source` is the first *reflection* source serving it;
`resolveTarget` honors a per-request override first.

**Definitions are shared; order is per collection.** `grpcview.work.json`
holds the unordered definition set; `grpcview.json` holds the ordered
per-collection list, inline or by **reference**. An upload can't be a
definition (bytes belong to the collection); a bazel label can — no RPC
edits a `WORKSPACE`-origin definition's address in place. An upload's
identity is its file name; `path` is only a refresh recipe (browser
uploads record none — refresh is then `FailedPrecondition`); read-time
confinement refuses `..`/symlink escape. Descriptor bytes are normalized
once at the write boundary (`DiscardUnknown` + deterministic marshal) —
digest is a function of schema, not encoder.

### The bazel kind, and workspace trust

A `Bazel{label}` refresh **builds**: `bazel query` for the descriptor-set
closure, `bazel build`, `bazel cquery --output=files` — argv slices,
never a shell string; downstream it behaves like an upload (no dial
target). Adding one is a combobox pick, not free-typed.

Gated on **workspace trust** (VS Code's model, since a build runs
arbitrary repo code): granted once per root, stored in user state, not
the repo. Untrusted is a working state, not broken — only a build is
refused (`ListCollectionsResponse.trusted` + `SetWorkspaceTrust`).

## The collection tree

`ui/src/components/tree/` is a hand-rolled, domain-agnostic tree —
`features/workspace/` supplies the gRPC half.

- One contract, two row tiers: `TreeAdapter<T>` gives the **portable**
  tier (default row); `renderRow` opts into the **rich** tier (arbitrary
  React per row) — the request tree is rich.
- The flat visible-rows array is load-bearing: `flatten.ts` reduces
  roots+expanded to an ordered array + id→index map; every behavior
  (arrow keys, range select, drop targeting) is array arithmetic over it,
  never recursion. State is zustand (`treeExpanded`/`treeSelection`/
  `treeFocused`); decisions are pure functions (`keymap.ts`,
  `dispatch.ts`, `navigate.ts`, `dnd.ts`), `Tree.tsx` a thin interpreter.
- Rename is the component's — validates against visible siblings, server
  stays collision authority. Context menu is the host's. DnD is native
  HTML5: a folder row splits into quarters (outer = between-rows, middle
  half = into), a leaf splits in half. A multi-row move is **sequenced**
  (each call chained off the previous `onSuccess`) — order becomes
  persisted sibling order.
- **Identity hazard: `itemKey` is path+name derived** — rename/move
  changes an item's key (and descendants'). Any such mutation must call
  `moveSubtree(oldKey, newKey, newName)`, the one remapper of every keyed
  UI-store field.
- Not built: typeahead, compact folders, sticky scroll, virtualization,
  async `getChildren`.

## The workspace and its collections

A workspace is a repository; collections are what's in it. Root owns no
requests; a collection is `grpcview.json` + `tree/` + `scripts/`.

- Root found by walking up: `--workspace` wins, else nearest `.git`, else
  cwd with a stderr warning.
- A collection is addressed by its path relative to root (`.` = root); id
  is disk location, never written to disk. Only `CreateCollection`
  creates one. `UpdateCollection` takes `name` (manifest write) and
  `new_collection` (a move — `os.Rename` + moving local state).
- Directory slug is identity, display name is data. Renaming writes
  `meta.Name`, leaves the directory alone (avoids git-history churn). A
  request directory holds `request.json` + `body.ts` + `metadata.ts`,
  moving together. **Scripts are the exception** — path is identity,
  renaming moves the file.
- TopBar picker switches collections via pure UI state — every query key
  is built from the active id, so switching triggers no reload.
- Local state (resolve caches, run history) lives outside the collection,
  under `os.UserConfigDir()` (`GRPCVIEW_CONFIG_DIR`) — a collection
  directory is therefore 100% committed content.
- Discovery is declared-or-scanned: a non-empty `grpcview.work.json`
  `collections` list wins (globs allowed); otherwise the root is scanned
  for `grpcview.json`, pruning dot-dirs/`node_modules`/`bazel-*`/gitignored
  paths. **Not cached** — a mtime cache previously missed collections
  created below the root. `ListCollections` reads manifests only, never
  trees.

## The CLI

`grpcview` with no subcommand serves the UI + API. Everything else is a
cobra verb in `service/cli/`, on the same binary (embedded UI is 26.9 MB
of 51.5 MB).

```
grpcview                     serve UI+API, open browser
grpcview serve [--port 10000] [--idle-timeout <d>] [--no-open]
grpcview url | open | shutdown
grpcview invoke <request-path>|<service>/<method>
grpcview describe <service>/<method>  [-o proto|json]
grpcview ls [<folder-path>]  [-o text|json]
grpcview get
grpcview sources ls|add|commit|refresh|rm|reorder
grpcview trust [--off]                allow sources that run a build
grpcview request create|rm|mv   grpcview folder create
grpcview script ls|run          grpcview completion bash|zsh|fish
grpcview init [dir] [--name]
grpcview collections ls      [-o text|json] [--refresh]
grpcview mcp
```

Runs a saved request from a shell, exit code reflects gRPC status. Every
verb takes `--workspace <root>`, `--collection <id>`, `--server <addr>`,
`--in-process`. **Root resolves in one place, `wsroot.Discover`:**
explicit `--workspace`, else `$BUILD_WORKSPACE_DIRECTORY`, else nearest
`.git`, else cwd with a warning.

- `service/cli` must not import `//service` (would drag 26.9 MB into
  every CLI test). One `Client` interface, wire default (`service/wire`,
  shared with `service/mcp`): local, pinned-remote, reconnecting-remote.
- **Exit codes are the contract:** `0` = OK; `1` = other gRPC status
  (inside `Request.Response.status`, no error); `2` = grpcview's own
  failure, nothing invoked.
- Where you stand decides what you address, like git/bazel: no
  `--collection` → nearest collection at/above cwd bounded by root, else
  the workspace's only collection, else exit 2 listing candidates.
- stdout is data, stderr is everything else. `-o body` (default) prints
  nothing on failure; `-o json` prints the whole `Response` either way;
  streaming prints NDJSON; a mutation prints nothing, exits 0.
- Structured input only, never per-field flags: bodies via `-f file`,
  `-f -`, or a bare pipe; stdin is NDJSON for client-streaming/bidi, one
  verbatim message otherwise. `invoke`'s argument resolves against both
  saved-request path and `service/method` off one `Get` snapshot —
  matching both is exit 2.
- `InvokeSaved`/`InvokeSavedStreaming` resolve saved
  body/metadata/middleware/target server-side, support `dry_run`;
  `describe` never dials, answering from the cached merged descriptor set.
- `sources add` reads kind from the argument: a path that stats as a
  file → upload; `//`/`@` prefix → bazel label; else → reflection
  address.
- One writer — every verb goes through this workspace's daemon by
  default; `--in-process` is the escape hatch to two writers on one
  directory.

## The workspace daemon

Bazel's client/server model: a CLI verb connects to the workspace's
server if running, starts one if not, server exits after inactivity.
Design: [`docs/design/shipped/daemon.md`](docs/design/shipped/daemon.md).
Payoff: linked-descriptor cache (LRU 16) and compiled QuickJS engine stay
warm between invocations.

- Registration file is a hint, never authority —
  `<cache>/grpcview/servers/<sha256 of abs root>.json`. Client checks
  pid-alive → connects → verifies the server reports the same workspace
  root.
- Port defaults `10000`, falls back on conflict; explicit `--port` in use
  is an error (`grpcview url` reveals the actual port). `ServerService`
  is separate from `WorkspaceService` on purpose — its RPCs would
  register as MCP tools.
- Startup is locked, never hangs — advisory `flock` covers
  check→spawn→wait→connect. Version skew restarts the server, keyed on
  the binary (path+mtime+size), since `"dev"` links every unstamped
  build.
- Idle exit is a counter from the last request, armed only when nothing's
  in flight (default 1h). **Only an auto-spawned server idles out** — a
  hand-run `grpcview` runs forever.
- A **dial** failure allows retrying anything; an in-flight break allows
  retrying only reads. `--in-process` is bazel's `--batch`. Boundary is
  loopback + origin policy — no token.

## The MCP server

`grpcview mcp` speaks MCP over stdio on the same binary — no HTTP
endpoint, no auth. `--timeout` doesn't apply — bypasses the per-verb
timeout for a long-lived session.

- Tools are grpcview's own unary RPCs, registered at runtime from
  `WorkspaceService`'s descriptor via `protoc-gen-go-mcp` — not one tool
  per reflected target method.
- Streaming RPCs get two hand-registered tools: the handler **drains the
  stream**, returns `{messages, result, truncated}` under a
  frame/byte/deadline cap (200/256KB/60s); the invoked call's gRPC status
  lives in `result`, never the tool's error channel.
- `service/mcp` is the one seam for payload rules: renames tool names,
  drops the output schema, strips every `descriptor_set`, defaults
  `collection`. An oversized response-body string is elided (8KB — a
  base64 descriptor set inside JSON inside `bytes` is exactly that
  shape). `history`/`services` are owned only by the RPCs that can
  change them (`get_collection` is `history`'s only read access).
- **`run_script` hands the calling agent arbitrary JS with `fetch`
  enabled** — a known, unmitigated exposure. Runs in the daemon, not the
  MCP child; writes serialize on one `Collection.mu` like every other
  verb.

## Views (no router)

The SPA has no URL router. `App.tsx` renders one `AppShell`, switching the
main pane on a zustand field: **Workspace** (default) — tree + editor +
response pane; **Sources** — priority-ordered source list; **Scripts** —
authoring `.ts` files, addressed by path. Server state via
`@connectrpc/connect-query` + `@tanstack/react-query`; local/view state in
`zustand`.

## Verify through MCP or the CLI, not the browser

**MCP first, CLI second, browser last** — all three share the backend
without a browser session's per-check cost. CLI covers what MCP doesn't:
shell/exit-code checks, incremental streaming (MCP drains and caps the
whole stream), the CLI's own argv/flag surface.

**`example/` reflects grpcview's own workspace server (`:10000`) and its
descriptors are committed** — after a `.proto` change the snapshot is
stale until `sources refresh`; verifying a server-side change means
killing whichever daemon holds `:10000` (`grpcview shutdown`). **A CLI
check leaves a daemon running** by design; add `--in-process` when it
must not outlive itself.

Browser is last resort: rendering, Monaco behavior, tree keyboard/mouse/
DnD, focus/layout, zustand/query-cache state — a `ui/`-only change is a UI
bug by definition. Say which surface you used.

## Browser verification hook (editors)

The body and metadata editors register on `window` keyed by model URI
(`ui/src/lib/editor-debug.ts`):
`window.__grpcviewEditors["file:///grpcview/request/body.ts"]` (body) and
`.../metadata.ts` (metadata). Each is a Monaco `IStandaloneCodeEditor`:
`.getValue()` reads the exact buffer, `.setValue(src)` drives it (sidesteps
auto-closing brackets that corrupt naively *typed* code). The Scripts
scratchpad editor is not registered.

## Design language

**Nocturne**: dark, compact, token-driven; single blurple accent `#9184d9`,
Inter for UI text, JetBrains Mono for code, Phosphor icons, outlined
actions, 8px radii, ~0.7× density. Tokens in `ui/src/theme/`; plan in
`docs/design/shipped/ui-redesign-plan.md`.

## Build System (Bazel)

### Commands

```bash
bazel build //service/cmd            # host arch
bazel build //service/cmd:release    # all four published arches
bazel build //...
bazel test //...
bazel run //service/cmd/dev          # dev backend, -port 10000
bazel run //ui:dev                   # frontend dev server
bazel run //grpcview/v1:grpcviewv1_ts_proto.copy   # regen TS proto types
```

`//service/cmd` aliases `//service/cmd:grpcview`. `:release` is a filegroup
over one `go_cross_binary` per `RELEASE_PLATFORMS` entry — locate outputs
with `bazel cquery --output=files`, not a guessed path. Embedded UI is
pinned to `@platforms//host` (else the cross transition rebuilds the vite
bundle per arch and rollup's native binding fails to resolve).
`grpcviewv1_ts_proto.copy` copies regenerated `.d.ts` into the source
tree; runtime `_pb` modules are bazel-generated, not committed.

### Releasing

GitHub releases only, cut by the `Release` action in `buildbuddy.yaml` on
every push to `trunk` plus a pushed `v*` tag:

```bash
bazel run --config=ci --stamp -c opt //tools:release -- --dest dist
```

`//tools:release` (`tools/release.sh`) builds `//service/cmd:release`
optimized and version-stamped, writing into `--dest`: four binaries,
`SHA256SUMS`, `install.sh`, then `gh release create --generate-notes`.
Named by `tools/version.sh` — a trunk commit ships as a pseudo-version, a
`vX.Y.Z` tag ships under the tag; an already-published version is
skipped, not failed. The tag filter (`v[0-9]+.[0-9]+.[0-9]+`) is
**GitHub's filter-pattern dialect, not a regex** — `+` means "one or
more of the preceding char", `.` is literal.

`tools/install.sh.tmpl` renders to `install.sh`, sums baked in at render
time; picks `grpcview_<goos>_<goarch>` from `uname`, installs into the
first writable of `/usr/local/bin`, `~/.local/bin` via a temp-name rename
(avoids `ETXTBSY`). `grpcview uninstall` deletes only binaries by default;
`--purge` also deletes `wsroot.ConfigRoot()` (trust list, cached
descriptor blobs, run history — **not a cache**,
`service/wsroot/wsroot.go:96`) and `wsroot.CacheRoot()` (disposable).

Versions stamp into `cli.version`: an exact `vX.Y.Z` tag on HEAD wins,
else a Go pseudo-version, dirty worktree gets `+dirty`. `.bazelrc` omits
`--stamp` — unstamped builds leave `cli.version` at `dev`.

### Frontend gates

Three, checking different things — all must be green:

```bash
cd ui && ./node_modules/.bin/tsc --noEmit -p tsconfig.json  # the real typecheck
bazel test //ui:test                                        # vitest
bazel build //ui:ui                                         # release bundle
```

**`bazel build //ui:ui` does not typecheck** — Vite/esbuild strips types
without checking; `tsc --noEmit` isn't a Bazel target, run by hand.
`//ui:test` runs vitest with `environment: "node"`, no jsdom —
layout/focus/events need a browser.

## Directory Structure

```
grpcview/
  v1/               Wire API: service.proto (WorkspaceService+ServerService) +
                    workspace.proto (messages)
  store/v1/         On-disk persistence schema (storage.proto)
  echo/v1/          Trivial echo service for testing invoke end-to-end
service/
  service.go        Server lifecycle (bind, publish, drain); idle.go, logging.go
  cmd/              Production entry point (main.go embeds index.html)
  cmd/dev/          Dev backend entry point
  daemon/           Registration file, spawn lock, connect-or-spawn, browser launch
  wire/             The one Client interface + local/remote bindings
  workspace/        WorkspaceService handler (reflection, invoke, CRUD)
  store/            Filesystem-backed collection (protojson tree); convert.go
                    bridges store↔wire schemas
  scripting/        QuickJS-WASM engine, esbuild bundler, capability layer
  echo/             Echo service implementation + its cmd/ server
ui/src/
  App.tsx, main.tsx     App root + view switch
  components/shell/     AppShell, TopBar, Rail, StatusBar
  components/ui/        Nocturne design-system primitives
  features/workspace/   Request editor, tree, response pane, body/metadata
  features/sources/     Reflection-source configuration
  features/scripts/     Script authoring
  lib/                  client.ts, ui-store.ts (zustand), workspace-query.ts, format.ts
  theme/                Nocturne tokens, fonts, Monaco theme
third_party/quickjs/  Vendored QuickJS-WASM build inputs
tools/                Repo tooling
docs/design/          Design docs and plans (see below)
```

## Design docs

`docs/design/` is sorted by how much of each doc is real code — see
[`docs/design/README.md`](docs/design/README.md): `shipped/` (arc
finished, kept for the decisions), `active/` (stopped mid-arc, real
unbuilt work), `planned/` (nothing built; `roadmap.md` is the backlog
with no plan yet), `research/` (background, closed). Two docs stay
top-level: `request-body-contract.md` (authoritative on request bodies)
and `known-bugs.md` (defects deliberately left unfixed).

Every doc is present-tense about the code as written — its `file.go:line`
citations are the premise of a decision, not a description of trunk. Move
a doc when its status changes; delete a `shipped/` one once nothing in it
is worth keeping.
