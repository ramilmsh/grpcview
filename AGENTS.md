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
- **Prefix every Bazel/Go command with `env GOPROXY=off`** so nothing tries to
  fetch modules. Offline builds are green; a command that wants the network is a
  bug in the command, not a missing dependency.
- The default shell here is fish. For commands that need bash semantics or inline
  env vars, wrap them: `env GOPROXY=off bash -c '...'`.

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
  scenarios — under a **capability grant** (filesystem/network access is
  deny-by-default). Sources are bundled with **esbuild** before execution.

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

## Views (no router)

The SPA has **no URL router**. `App.tsx` renders a single `AppShell` and switches
the main pane on a `zustand` store field (`activeView` in `lib/ui-store.ts`)
between three feature views:

- **Workspace** (`features/workspace/`) — the collection tree + request editor +
  response pane; the default view.
- **Sources** (`features/sources/`) — configuring the gRPC reflection sources /
  descriptor state for the collection.
- **Scripts** (`features/scripts/`) — authoring the generator/middleware/scenario
  scripts.

Server state is fetched via `@connectrpc/connect-query` on top of
`@tanstack/react-query`; local/view state lives in `zustand`.

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
Remember the `env GOPROXY=off` prefix on every command.

### Commands

- **Build the release binary** (standalone, frontend embedded):

  ```bash
  env GOPROXY=off bazel build //service/cmd
  ```

- **Build & test everything:**

  ```bash
  env GOPROXY=off bazel build //...
  env GOPROXY=off bazel test //...
  ```

- **Run the dev backend** (serves the API without embedding the frontend):

  ```bash
  env GOPROXY=off bazel run //service/cmd/dev
  ```

- **Run the frontend dev server** (Vite):

  ```bash
  env GOPROXY=off bazel run //ui:dev
  ```

- **Regenerate TypeScript proto types** (run after editing any `.proto`):

  ```bash
  env GOPROXY=off bazel run //proto/grpcview/v1:grpcviewv1_ts_proto.copy
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
