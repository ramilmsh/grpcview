# Project Overview

`grpcview` is a gRPC client — a Postman-like tool for exploring and calling gRPC
services. It reflects a server's schema, lets you author requests, and invokes
them, all from a single self-contained binary.

The defining design decision: **requests are authored as TypeScript, not JSON.**
A request body is a typed TS object literal, checked in-browser against the
selected method's input message; request metadata is authored the same way; and
both are *evaluated* (in a sandboxed JS engine) at invoke time, so they can call
into user-defined scripts. There is no JSON-schema layer — an earlier design that
converted proto descriptors to JSON schemas has been removed entirely.

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

**Cache-soundness invariant:** the invoker and any non-empty `params`/inherited
metadata ride only the **uncached** `RunRequestBody`/`RunMiddleware` path. The
`RunGenerator` path is cached by `configDigest` (`profiles.go`, which reads only
`{Vars, Secrets, Env, Args}`) and must keep seeing `params === {}`, `inherit() ===
{}`, and a throwing `invoke`. Never route folder metadata through `RunGenerator`.

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
  bazel run //service/cmd/dev
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
