# Project Overview

`grpcview` is a gRPC client — a Postman-like tool for exploring and calling gRPC
services. It reflects a server's schema, lets you author requests, and invokes
them, all from a single self-contained binary.

The defining design decision: **requests are authored as TypeScript.** A request
body is a typed TS object literal, checked in-browser against the selected
method's input message; request metadata is authored the same way; and both are
*evaluated* (in a sandboxed JS engine) at invoke time, so they can call into
user-defined scripts. There is no JSON-schema layer — an earlier design that
converted proto descriptors to JSON schemas has been removed entirely.

**But TypeScript is the authoring affordance, not the contract: a body is
protojson.** Plain protojson is accepted everywhere a body is — the backend supplies
the wrapper, so nobody is required to speak TypeScript to send a request they already
have. See
[`docs/design/request-body-contract.md`](docs/design/request-body-contract.md) for
the two accepted forms and the one seam that normalizes them.

## Project Stage

**This project has no users yet — it is way pre-release.** Breaking any contract
you like is perfectly fine. **SIMPLICITY is the important part; backwards
compatibility is IRRELEVANT at this stage.** Don't add migrations, compatibility
shims, or `reserved` proto markers to preserve old on-disk/wire data — change the
schema and delete freely. Always favor the simplest change that works over the
most backwards-compatible one. Dead and legacy code should be deleted on sight.

## Working in this repo (read before running commands)

- **This is a Bazel workspace. Never run `go build`, `go test`, `go run`, or —
  especially — `go mod` / `go mod tidy`.** Bare `go` commands reach the network,
  hang, and can wedge git. Use Bazel for everything (see Commands below).
- **`.envrc` already exports `GOPROXY=off`** (via direnv), so Bazel/Go commands
  run offline by default — you don't need to prefix anything. Offline builds are
  green; a command that wants the network is a bug in the command, not a missing
  dependency.
- The default shell here is fish. For commands that need bash semantics, wrap
  them: `bash -c '...'`.

## Delegating to background agents

Code-writing here is delegated to background agents (Workflow / Agent) while the
main thread orchestrates, verifies in a browser, and commits.

Measured across the first 25 agents of the tree rewrite: **~83% of token spend
was context handling, not reasoning or writing.** Cache reads alone fit
`≈ 1300 × turns²` (so a 100-turn agent costs 4× a 50-turn one, and a 180-turn
agent 16×), and wall clock runs ~10s/turn. **Turn count is the lever that
matters** — every rule below exists to cut it.

- **Scope one agent to ~40 turns.** Halving a job quarters its cache cost. The
  ~30–50k of context each additional agent re-establishes is ~1% of even a small
  agent's total, so splitting never eats the gain — a worry that sounds right and
  is wrong by two orders of magnitude.
- **Pre-load context; don't make the agent find it.** Paste the relevant excerpt
  into the brief, and name exact paths and line ranges. "Check how the vendored
  monaco does it" buys a ~15-turn grep expedition *per agent*; the 40 lines it
  eventually reads cost ~500 tokens to paste. Agents left to discover their own
  context hit Read×40+.
- **Cap the verify loop: run each gate at most twice, then report the failure
  verbatim and stop.** "Make all the gates pass" licenses iterate-until-green,
  the single biggest turn driver observed (one agent made 46 `Bash` calls). The
  orchestrator re-runs every gate before committing anyway, so grinding to green
  inside the agent is duplicated work at quadratic cost.
- **One reviewer, handed the diff.** Read-only review agents burned 25% of all
  output tokens and wrote no code. Keep the adversarial pass — it has caught real
  bugs that two implementers and a typecheck all missed — but put `git diff` in
  the brief instead of letting the reviewer rediscover it.
- **`effort`: one tier below the session's, floored at `medium`. Never `low`** —
  low mangles output, and a mangled result costs a whole re-run.

Capping the verify loop is a ban on *grinding*, never a licence to skip testing.
Agents in this repo have a track record of **reporting passes that never
happened**, in two specific ways:

- A new `.go` file that was never added to its `BUILD.bazel` `srcs` isn't
  compiled, so the package still builds and its tests still "pass". Check that
  new sources actually landed in `srcs`.
- Bazel serves cached results, so a test target can report `PASSED` without
  running. When validating someone else's claimed pass, always
  `--nocache_test_results`, and check the named test count changed.

## Architecture

- **Frontend** (`ui/`): a React 18 + TypeScript single-page app built with Vite.
  Compiled to a **single HTML file** (`vite-plugin-singlefile`) and embedded into
  the Go binary, so distribution is one standalone static executable.
- **Backend** (`service/`): a Go server exposing the `WorkspaceService` over
  [Connect] (h2c). It handles gRPC **reflection** and **request proxying/invoke**,
  persists the workspace to disk, and hosts the scripting engine.
- **Store** (`service/store/`): a filesystem-backed collection persisted as a
  git-versionable **protojson directory tree**. The on-disk schema
  (`grpcview.store.v1`) is deliberately decoupled from the wire schema
  (`grpcview.v1`) and bridged by `convert.go`.
- **Scripting** (`service/scripting/`): a QuickJS-WASM engine (wazero) that runs
  user JS/TS — request-body/metadata evaluation, generators, middleware, and
  scenarios. **Network is on for every script**: a browser-style global `fetch`
  (a deliberate subset of WHATWG fetch — see `net.go`) is available with no grant
  and no capability manager. The **filesystem** capability is still deny-by-default
  behind a `Grant` (`node:fs`). Sources are bundled with **esbuild** before execution.

[Connect]: https://connectrpc.com

## Request authoring model

This is the core of the product; understand it before touching `ui/` or
`service/scripting/`.

**Why TypeScript:** it replaced `{{ }}` templating. Postman-class tools need a token
syntax because their bodies are *data* — text you interpolate into — which buys escaping
rules, no autocomplete, no type-checking, and no composition. grpcview's bodies are
*expressions*, so a computed value is just a call written where the value goes
(`{ userId: uuid() }`), and IntelliSense comes free from the host language. The static
and dynamic cases are therefore one gradient, not two modes: `{"userId":"u_1"}` becomes
`{"userId": uuid()}` by editing it, with no conversion step. Preserving that is why
plain protojson must run the *same* evaluation path rather than a separate one — a
protojson body that could not call a generator would be `{{ }}` all over again.

- A request **body** is authored as a bare TypeScript object literal in a Monaco
  editor. What the user edits is wrapped in a hidden canonical module —
  `export default (): RequestMessage => ( <body> )` — whose prefix/suffix lines
  the editor hides (`body-wrapper.ts`). The `RequestMessage` type is generated
  **in the browser** by `@bufbuild/protoc-gen-es` from the workspace's reflected
  `FileDescriptorSet` (`proto-types.ts`), giving full IntelliSense and
  type-checking against the selected method's input message.
- Request **metadata** is authored identically — a bare object evaluated to
  `{ [key: string]: string[] }` under a hidden `=> ( … )` wrapper
  (`metadata-wrapper.ts`, `MetadataEditor.tsx`).
- Both body and metadata strings are **evaluated on the backend in QuickJS** at
  invoke time (same machinery as scripts), so they can call generator helpers
  (e.g. `uuid()`) and reference user scripts.
- **Plain protojson is equally valid** for both, because **valid JSON is valid
  TypeScript** — a JSON object is a TS object literal in expression position, so it is
  not a second case and gets no second code path. There are two forms: a **module**
  (has `export default`) or an **expression** (anything else, wrapped in
  `export default async () => ( … )` and run on the same path). The Monaco
  hidden-wrapper form above is what the browser authors; it is not what the backend
  requires. `resolveInvokeBody` (`service/workspace/invoke.go`) is the single seam that
  applies the wrap, so every surface — UI, VS Code, CLI, MCP — inherits one behavior.
  Full contract: [`docs/design/request-body-contract.md`](docs/design/request-body-contract.md).
- **Scripts** (generators / middleware / scenarios) are authored in the Scripts
  view, bundled with esbuild, and run in the same sandbox under a grant.

### The `gv` global

Every script run — body, request metadata, folder metadata, middleware, scenario,
generators — sees one deep-frozen `gv` global, declared for the editors by a single
ambient `gv.d.ts` registered once at `file:///grpcview/gv.d.ts`
(`monaco-scripts.ts`) and assembled backend-side by `buildGvPrelude`
(`service/scripting/marshal.go`):

```ts
declare const gv: {
  metadata: { inherit(): { [key: string]: string[] } };
  request:  { params: Readonly<Record<string, unknown>> };
  invoke(path: string, params?: Record<string, unknown>): Promise<InvokeResult>;
};
```

`gv` is assembled and frozen **exactly once** (`Object.freeze` blocks later member
addition, and a second `globalThis.gv =` would clobber the first). The two
callables are hung off the containers before the single `__ff` freeze pass, which
recurses only on `typeof === "object"` and so leaves them callable. Members degrade
gracefully rather than being absent: `inherit()` is `{}` with no inheritance
context, `params` is `{}` on a top-level invoke, and `invoke` rejects when no
`Invoker` rides the ctx.

- **`gv.metadata.inherit()`** returns the already-evaluated, merged metadata of the
  node's ancestor **folder** chain. Folders carry their own metadata script
  (`Folder.draft_metadata_script`, edited via the folder-row gear); `invoke.go`'s
  `foldAncestorMetadata` walks them root→parent as an iterative Go fold, gated on
  the request's script textually mentioning `inherit(` and capped at
  `MaxFolderMetadataDepth`. Transitivity is **userland spread**: a folder that
  writes `{ ...gv.metadata.inherit(), … }` carries ancestors forward, an empty
  folder is transparent, and a non-empty folder that omits the spread is a
  deliberate barrier. Ancestor scripts are read from the **store**, so folder edits
  only take effect after saving.
- **`gv.invoke(path, params)`** runs another saved request through the same
  pipeline and resolves an `InvokeResult` (`{ok, status, body, metadata,
  requestMetadata, latencyMs}`); the target reads `params` as `gv.request.params`.
  `path` splits on `/` into display-name segments. A gRPC-status failure **resolves**
  with `ok:false` (fetch-style); it rejects only for unknown path, a streaming
  target, un-evaluable body/metadata, or the depth cap. Nested invokes do **not**
  record history. Bounded by a ctx depth counter (`gvinvoke.go`) — a depth cap only,
  with no cycle set, so self-recursive pagination still works.

## Definition sources (where schemas come from)

A workspace's services and `descriptor_set` are **derived**, never authored. They
come from a **priority-ordered list of descriptor sources** — reflection targets
and uploaded `FileDescriptorSet`s — merged by `service/workspace/sources.go`.

The layering is the whole point, so don't collapse it:

1. **Each source resolves independently** to its own `FileDescriptorSet` plus the
   list of services it serves. A reflection source resolves by dialing; an upload
   resolves by linking its committed bytes. Each resolve is cached per source
   (`.grpcview/cache/sources/<slug>.binpb`, binary, gitignored).
2. **The merged view is re-derived from those caches on every mutation** —
   `mergeSources`, walking the list front to back: the first source to define a
   proto **file** (by name) wins it, the first to serve a **service** (by full
   name) wins its list entry, later sources fill the gaps. Then the whole claimed
   set is *link-checked*, so sources that disagree about shared protos fail loudly
   instead of producing a subtly broken workspace.

Consequences worth preserving, each of which was a bug before:

- **Order is precedence, and only order.** The outcome is a pure function of the
  source list, never of which source was added or refreshed last. That is what
  makes two sources describing the *same* protos usable: gRPC reflection strips
  `source_code_info`, so a `buf build` upload of those files carries doc comments
  the live server cannot, and whoever wins decides whether hovers show them.
  `ReorderDescriptorSources` is the user-facing switch.
- **Only the added/refreshed source touches the network.** Remove and reorder are
  pure cache re-derivations, so an unreachable target can't block them. A source
  that fails to resolve stays listed with the reason in `Resolved.error` and
  contributes nothing, rather than being dropped or failing the mutation.
- **Identity is config-derived** (`store.SourceID`, the one definition of the
  format): `reflection:<address>[+tls]` or `upload:<file name>`. Re-adding the same
  id **refreshes in place at its existing priority**; a genuinely new source appends
  at lowest priority. Keying an upload by file name (not by a content hash) is
  deliberate — rebuilding the image must refresh the source it came from, not spawn
  a second one.
- **Every source has a unique, non-empty id, guaranteed at load**
  (`store.normalizeSources`, run from `readCollection`). Refresh, remove and
  reorder all address a source *by id*, so two rows sharing one id — as a manifest
  written before ids existed produces — silently retarget those operations at the
  first of them. Manifests older than identities get their ids derived, their
  legacy inline upload (`legacy_descriptor_set`) folded into `Upload`, and
  duplicate or contentless entries dropped.
- **A service's dial target is independent of who won its descriptors**:
  `Service.source` is the first *reflection* source that serves it. An upload has
  no address, so without that split, placing one first for its comments would
  strand every request it claimed with no target. The UI keeps the two questions
  visually separate too: the request header's chip names the source the **schema**
  came from (`schemaSourceFor`, off `Resolved.won_service_names`), while the target
  bar under it shows where the request is **sent**. Neither is "no source" merely
  because the other is absent.

An upload's descriptors are committed to `grpcview.json` (typed protojson) because
unlike a reflection target they cannot be re-fetched; the resolve caches are
disposable. protojson drops `buf`'s image extensions, which nothing reads —
`source_code_info` round-trips intact.

## The collection tree

`ui/src/components/tree/` is a **hand-rolled, domain-agnostic tree component** —
not a library wrapper, and it knows nothing about gRPC. `features/workspace/`
supplies the gRPC half (`request-tree.tsx`'s adapter + row renderer,
`CollectionPanel.tsx`'s callbacks). Keep that boundary: everything in
`components/tree/` must remain reusable by a second tree (the descriptor explorer
is the intended one), which is what forces every gRPC-shaped decision out into the
host.

- **One contract, two row tiers** (`types.ts`). A `TreeAdapter<T>` supplies
  `getId` / `getChildren` / `getCollapsibleState` / `getParent` / `getTreeItem`. A
  caller that supplies only that gets the **portable** tier: a default row built
  from `getTreeItem` (label, description, an abstract `IconToken`), renderable by a
  VS Code `TreeItem` too. Passing `renderRow` opts into the **rich** tier —
  arbitrary React per row, standalone-only. The request tree is rich (method-kind
  tags, hover buttons); a portable provider must avoid `renderRow`, stick to the
  `IconToken` vocabulary, and enumerate its `kind` strings.
- **The flat visible-rows array is the load-bearing decision.** `flatten.ts`
  reduces roots + the expanded set to an ordered `TreeRowModel[]` plus an id→index
  map, and *every* behavior — arrow keys, range select, drop targeting — is array
  arithmetic over it, never recursion. A node's own `"expanded"` default is
  *reported* by `flatten`, never self-applied; `resolveExpansion` folds it in
  synchronously during render (so there is no collapsed first frame) and
  `useTreeState` remembers which ids it has force-opened so a manual collapse is
  never sprung back open.
- **State is controlled, owned by zustand** (`ui-store.ts`: `treeExpanded`,
  `treeSelection`, `treeFocused`) so it survives re-renders and view switches. Each
  pair is independently controlled; omit one to fall back to internal state. The
  range-select anchor is internal by design.
- **Decisions are pure functions; `Tree.tsx` is a thin interpreter.** `keymap.ts`
  maps a keystroke (+ `isMac`) to an intent with no DOM; `dispatch.ts` maps an
  intent *or* a click *or* a twistie click to a plain `TreeAction[]`; `navigate.ts`
  does the index math; `dnd.ts` does the drop geometry. `Tree.tsx` builds events,
  measures the DOM, and applies actions in one place. New interaction behavior
  belongs in the pure module, with a unit test — the suite has **no jsdom**, which
  is exactly why the decisions are DOM-free.
- **Keyboard/mouse follow VS Code per platform**, verified against the vendored
  monaco sources in `ui/node_modules/monaco-editor/esm/vs/base/browser/ui/`
  (`listWidget.js`, `listView.js`, `abstractTree.js`) — cite them when changing
  behavior. Arrows/Home/End/PageUp/PageDown move a *logical* cursor: DOM focus
  stays on the `.tree` container with `aria-activedescendant` naming the row, never
  a roving per-row tabindex. `Enter` is platform-split (macOS renames, `cmd+↓`
  opens; elsewhere `Enter` opens), `F2` renames everywhere, `shift`+click/arrow
  extend from the anchor, `cmd/ctrl`+click toggles, `cmd/ctrl+A` selects all
  visible, `Escape` clears. Paging measures the nearest scrollable ancestor, not
  `.tree` (which has no bounded height of its own).
- **Rename is the component's** (`RenameInput.tsx`): it renders the box, validates
  against the row's visible siblings, commits on Enter/blur, cancels on Escape, and
  reports exactly once as `onRenameCommit`. The host only persists it; the server
  stays the collision authority. `TreeHandle.startRename(id)` is how an outside
  affordance (the row pencil, the context menu) starts one.
- **The context menu is the host's.** The tree selects/focuses the row and hands
  over `(nodes, ev)`; `CollectionPanel` renders `components/ui/Menu.tsx`, because
  the items are gRPC-shaped. Empty-space right-click is the panel's own handler,
  guarded on `defaultPrevented`.
- **Drag and drop is native HTML5, no library.** A row is `draggable`; every other
  drag event is delegated to the container, which recovers the row from
  `data-index` (monaco's own structure). Geometry: a folder row splits into
  quarters — outer quarters are *between-rows*, the middle half is *into* — and a
  leaf splits in half, since there is no inside of a request. `after` an **expanded**
  folder means position 0 *inside* it, because that is where the indicator line
  visibly sits. `into` washes the row; between-rows draws a 2px accent line
  **indented to the destination's depth** (a full-width line cannot say which parent
  the item lands in). The dragged set is the selection if the drag started on a
  selected row, else that row alone. The tree rejects the structurally impossible
  (into a leaf, into a dragged node's own subtree, a no-op); the host's `canDrop`
  covers only what the tree cannot see — a destination that already holds the same
  display name, whose children may be collapsed or filtered out. One `MoveItem` per
  moved item; a `new_path` resolving to the current parent is a pure reorder, so
  reorder and reparent are one call. A multi-row move is the **one batch in this app
  that is sequenced** rather than fired concurrently (each call from the previous
  one's `onSuccess`): every call carries the same `before`, so the order the server
  processes them in *becomes* the persisted sibling order. Do not "simplify" it back
  into a loop.
- **The identity hazard: `itemKey` is path+name derived**, so a rename *or a move*
  changes an item's key — and for a folder, every descendant's. Any such mutation
  must call `ui-store.ts`'s `moveSubtree(oldKey, newKey, newName)`, which prefix-remaps
  `openTabs` / `drafts` / `invokes` / `treeSelection` / `treeFocused` / `treeExpanded`.
  Getting it wrong silently detaches an open tab from its draft and last response,
  which reads as lost work rather than as a bug. There is one remapper; do not write
  a second.
- **Still outstanding: typeahead (T3).** Letter keys are deliberately unclaimed by
  `keymap.ts` — they fall through untouched — and there is no `typeahead.ts`. The
  intended behavior is VS Code's: jump focus to the next label match, 1s buffer,
  wrap-around, composing with (not replacing) the header filter box. Also unbuilt:
  compact folders (the `compactFolders` prop is accepted and does nothing), sticky
  scroll, virtualization, and the async `getChildren` promise path (`flatten` and
  `reveal` both throw loudly on a thenable rather than silently dropping a branch).

## The CLI

`grpcview` with no subcommand still serves the UI + API. Everything else is a
cobra verb in `service/cli/`, on the **same binary** — the embedded UI is 26.9 MB
of the 51.5 MB binary, so a second CLI binary would duplicate ~20 MB of Go.

```
grpcview                                serve the UI + API (default)
grpcview serve --port 10000
grpcview invoke <request-path>|<service>/<method>
grpcview describe <service>/<method>    [-o proto|json]
grpcview ls [<folder-path>]             [-o text|json]
grpcview get
grpcview sources ls | add | refresh | rm | reorder
grpcview request create | rm | mv        grpcview folder create
grpcview script ls | run                 grpcview completion bash|zsh|fish
```

The reason it exists is one verb: **run a saved request from a shell, with an exit
code that reflects the gRPC status.** The rest is in service of that.

- **`service.Run` does not own argv.** It takes `service.Options{Port}`; the CLI
  (or `dev`'s own two-line flag set) parses. The flag is `--port`: pflag reads
  Go-flag style `-port` as the shorthand cluster `-p -o -r -t`, and there is no
  alias.
- **`service/cli` must not import `//service`.** The UI embed lives in
  `//service/cmd`, and that edge would drag 26.9 MB of `embedsrcs` into every CLI
  test. `cli.Main` receives a `serve` closure instead.
- **One `Client` interface, two bindings, no autodetection.** In-process
  (`workspace.Workspace` called as a plain Go value) is the default; `--server
  addr` is the explicit opt-in to the wire. "Dial the local server if one happens
  to be listening" was rejected so that *which process wrote my history* never
  depends on whether a server was up. Unary RPCs need no adapter — the handler and
  the generated client have the same signature, asserted at compile time. Only
  streaming differs (a handler takes `*connect.ServerStream`, a client returns
  `*connect.ServerStreamForClient`, and connect cannot build the former outside a
  served request), so `Client` declares the callback shape a CLI wants and
  `workspace` exports `InvokeStream`/`InvokeSavedStream` in send-func form.
- **Exit codes are the contract.** `0` = the call returned status OK; `1` = it
  returned any other gRPC status (which arrives *inside* `Request.Response.status`
  with a nil error); `2` = grpcview's own failure, nothing invoked. That 1-vs-2
  line is exactly the Connect-error-vs-status-in-payload line the backend already
  draws, so it needs no new classification — but it does need the invariant to
  hold, which `invoke_saved_test.go` pins directly.
- **stdout is data, stderr is everything else.** Latency, status text, warnings
  and `describe`'s source id are stderr. `-o body` (default) prints nothing on a
  failed call; `-o json` prints the whole `Request.Response` either way, because
  there the status *is* the data. Streaming prints NDJSON. A mutation prints
  **nothing** and exits 0 — silence is success. No colors, no TTY detection, no
  pager, permanently. `-o` is per verb with disjoint value sets, never persistent.
- **Structured input only, never per-field flags** (`PodSpec` has hundreds of
  fields and kubectl has no `--containers-0-image`). Bodies arrive as `-f file`,
  `-f -`, or a bare pipe, and the bytes are passed through **unchanged** —
  `resolveInvokeBody` normalizes protojson and TypeScript at one seam, so `-f`
  behaves identically for `body.json` and `body.ts`. For a client-streaming or
  bidi method stdin is NDJSON (one message per line); for every other kind it is
  one message verbatim, since a TS module is multi-line. That asymmetry is the
  sharpest trap in the verb.
- **`invoke`'s argument is resolved against both interpretations** — a saved-request
  path and a `service/method` — off a single `Get` snapshot. One that matches both
  is exit 2, never a guess: catching `NotFound` from `InvokeSaved` cannot work,
  because a miss on one interpretation says nothing about the other. Paths split
  through `workspace.SplitInvokePath`, the same parser `gv.invoke` uses.
- **`InvokeSaved`/`InvokeSavedStreaming`** are the addressed counterparts to
  `Invoke`: they resolve the *saved* body, metadata script, middleware and target
  server-side, take `params` (reaching scripts as `gv.request.params`), record
  history by default, and support `dry_run`, which stops after the shared pre-send
  steps and reports the **evaluated** request without dialing. `resolveSavedRun` is
  the one place a saved request becomes an `invokeSpec`, shared with `gv.invoke`.
- **`describe` never dials.** It answers from the workspace's cached merged
  descriptor set, so it works from a box with no route to the target, and it
  reports which source it read: doc comments survive only if that source carried
  them (reflection strips `source_code_info`, a `buf build` upload keeps it), so an
  empty-comment result must be attributable. `-o json` is the protojson of a
  `FileDescriptorSet` — the descriptors themselves, not an invented flat field
  list, which would be a lossy re-encoding of a standard format.
- **Two processes can write one collection directory without a lock**
  (`Collection.mu` is in-process only). Accepted: `--server` is the opt-out and
  `--no-history` removes the only write `invoke` performs. If a lost update ever
  bites, the fix is one advisory lock in the store that benefits every surface.

## Views (no router)

The SPA has **no URL router**. `App.tsx` renders a single `AppShell` and switches
the main pane on a `zustand` store field (`activeView` in `lib/ui-store.ts`)
between three feature views:

- **Workspace** (`features/workspace/`) — the collection tree + request editor +
  response pane; the default view.
- **Sources** (`features/sources/`) — the priority-ordered definition-source list
  (see above): add / refresh / reorder / remove, with each source's contribution
  shown so a shadowed one is visible as such.
- **Scripts** (`features/scripts/`) — authoring the generator/middleware/scenario
  scripts.

Server state is fetched via `@connectrpc/connect-query` on top of
`@tanstack/react-query`; local/view state lives in `zustand`.

## Browser verification hook (editors)

Driving the app in a real browser is the preferred way to verify UI / invoke
changes. Because several Monaco editors coexist (each with its own model) and there
is no global `monaco`, the request **body** and **metadata** editors register
themselves on a `window` map keyed by model URI (`ui/src/lib/editor-debug.ts`), so
the devtools console — or a browser-automation harness — can read and drive their
exact contents without reaching into React or guessing which DOM node is which:

- `window.__grpcviewEditors["file:///grpcview/request/body.ts"]` — the body editor
- `window.__grpcviewEditors["file:///grpcview/request/metadata.ts"]` — the metadata editor

Each value is a Monaco `IStandaloneCodeEditor`: `.getValue()` reads the exact buffer
and `.setValue(src)` drives it (the latter also sidesteps Monaco's auto-closing
brackets/quotes, which corrupt naively *typed* code — set the value instead of
typing it). App code only ever WRITES the map, so it is inert in normal use. The
Scripts scratchpad editor is not registered (its model is `SCRATCH_PATH`).

## Design language

The UI targets the **Nocturne** design system (dark, compact, token-driven; a
single blurple accent `#9184d9`, Inter for UI text, JetBrains Mono for code,
Phosphor icons, outlined actions, 8px radii, ~0.7× density). Tokens live in
`ui/src/theme/` (`nocturne.css`, `app-tokens.css`) and are consumed through
Tailwind utilities plus the design-system primitives in `ui/src/components/ui/`.
The reference is the "Nocturne" Claude Design project; the migration plan and
current status are in `docs/design/ui-redesign-plan.md`.

## Build System (Bazel)

Bazel drives building, testing, proto generation (Go + TypeScript), and embedding.

### Commands

- **Build the release binary** (standalone, frontend embedded):

  ```bash
  bazel build //service/cmd
  ```

- **Build & test everything:**

  ```bash
  bazel build //...
  bazel test //...
  ```

- **Run the dev backend** (serves the API without embedding the frontend):

  ```bash
  bazel run //service/cmd/dev          # -port 10000; dev is serve-only, no verbs
  ```

- **Run the frontend dev server** (Vite):

  ```bash
  bazel run //ui:dev
  ```

- **Regenerate TypeScript proto types** (run after editing any `.proto`):

  ```bash
  bazel run //proto/grpcview/v1:grpcviewv1_ts_proto.copy
  ```

  This copies the regenerated `.d.ts` declarations into the source tree. The
  runtime `_pb` modules are Bazel-generated and not committed.

### Frontend gates

Three, and they check different things — a change to `ui/` isn't verified until
all three are green:

```bash
cd ui && ./node_modules/.bin/tsc --noEmit -p tsconfig.json  # the only real typecheck
bazel test //ui:test                                        # vitest
bazel build //ui:ui                                         # the real release bundle
```

**`bazel build //ui:ui` does not typecheck.** Vite builds with esbuild, which
strips types without checking them, so a genuine type error produces a green
build. `tsc --noEmit` is the only gate that catches it, and it is not yet a Bazel
target — it has to be run by hand.

`//ui:test` runs vitest under `environment: "node"` with no jsdom. Component
behavior is tested by rendering with `renderToStaticMarkup` and asserting on
markup; anything needing real layout, focus, or event dispatch can only be
verified in a browser (see Browser verification hook below).

## Directory Structure

```
proto/
  grpcview/v1/      Wire API: service.proto (WorkspaceService Connect RPCs) +
                    workspace.proto (messages)
  grpcview/store/v1/ On-disk persistence schema (storage.proto)
  echo/v1/          A trivial echo service used for testing invoke end-to-end
service/
  service.go        Wires up the HTTP/Connect server; logging.go alongside
  cmd/              Production entry point (main.go embeds index.html)
  cmd/dev/          Dev backend entry point
  workspace/        WorkspaceService handler (reflection, invoke, CRUD)
  store/            Filesystem-backed collection (protojson tree); convert.go
                    bridges store↔wire schemas
  scripting/        QuickJS-WASM engine, esbuild bundler, capability layer
  echo/             The echo service implementation + its cmd/ server
ui/
  src/
    App.tsx, main.tsx   App root + view switch
    components/shell/    AppShell, TopBar, Rail, StatusBar
    components/ui/       Nocturne design-system primitives (Button, Dialog, …)
    features/workspace/  Request editor, tree, response pane, body/metadata
    features/sources/    Reflection-source configuration
    features/scripts/    Script authoring
    lib/                 client.ts (transport), ui-store.ts (zustand),
                         workspace-query.ts, format.ts
    theme/               Nocturne tokens, fonts, Monaco theme
third_party/quickjs/  Vendored QuickJS-WASM build inputs
tools/                Repo tooling
docs/design/          Design docs and plans (see below)
```

## Design docs

`docs/design/` holds living design docs (e.g. `storage.md`, `scripting-ui-plan.md`,
`ui-redesign-plan.md`) and background research under `research/`. Shipped
one-off implementation plans are deleted once their work lands — the code and this
file are the source of truth, not a plan archive.

# Claude for Chrome

- Use `read_page` to get element refs from the accessibility tree
- Use `find` to locate elements by description
- Click/interact using `ref`, not coordinates
- NEVER take screenshots unless explicitly requested by the user
- Prepare and execute sequences of actions, evaluate the final result. Only go step by step, if it failed in an unobvious manner
