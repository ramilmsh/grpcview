# Project Overview

`grpcview` is a gRPC client — a Bruno/Postman/Insomnia-like tool for exploring, calling and testing APIs, except lazer-focused on gRPC services.

This client is released a single statically linked binary (similar to grpcui). The binary contains:

1. The go server itself + cli and mcp clients
2. The frontend files (preprocessed React application)
3. WASM distribution of quickjs, which executes the javascript

All of those external components are embeded

## Project Stage - Pre-release

There are NO live users yet, NO live deployments, NO need for backwards compatibility/migrations/upgade paths and so on. Do NOT use deprecated and reserved keywords in protobuf, no incompatibility is possible

## Conventions

**Be terse.** Always find the shortest way to say something.

## Working in this repo

- **Bazel workspace. Never run bare `go` compiler, everything is available via bazel**.
- **`bazel clean --expunge` is not a fix** — Bazel is hermetic, and correct, it has no stale cache. Treat "bazel cache is stale" hypothesis similarly to "compiler has a bug" hypothesis. Not impossible, but very rare. Retry, or `bazel fetch @broken_repo`; a repo hook pauses to confirm.
- **When shipping ship straight to trunk** - no feature branches, I'm the only developer
- **Never push without explicit per-push approval** — I want to review every line of code you add, NEVER push without explicitly being asked to do so

## Delegating to background agents

When working on a task, do NOT execute the task in the main conversation thread, always split it into parts and delegate execution to background agents

## Verify through MCP or the CLI, not the browser

**MCP first, CLI second, browser last** — all three share the backend, any backend bug can be debugged identically by either, but MCP and CLI are much faster

## Architecture

- **Frontend** (`ui/`): Written in React, with extensive use of monaco editor. It uses generated tanquery hooks to communicate with backend. Uses esbuild for preprocessing
- **Backend** (`service/`): Go server, implements all the core functionality (from invoking requests to running scripts), no meangful logic must exist outside of it
- **Store** (`service/store/`): Storage layer for the workspace information. It is currently implemented to store files on disk marshalled as protojson, in order to improve readability and make it easy to commit them to a shared repository.
- **Scripting** (`service/scripting/`): Is the scripting engine, which executes copious amount of javascript involved in preparing requests. It is backed by quickjs (compiled to WASM) with wazero for execution. It is meant to incrementally emulate nodejs/browser runtime, as more functionality is added. For now, it only supports `fetch` API

## Build System (Bazel)

```bash
bazel test //... # to verify that everything works, it rebuilds any affected artifact and reruns any affected test. It is meant to be the only verification you need that everything builds and all tests pass
bazel run //service/cmd # to run the binary
```

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
  daemon/           Registration file, spawn lock, connect-or-spawn, browser launch — AGENTS.md
  wire/             The one Client interface + local/remote bindings
  workspace/        WorkspaceService handler (reflection, invoke, CRUD) — AGENTS.md
  store/            Filesystem-backed collection (protojson tree); convert.go
                    bridges store↔wire schemas — AGENTS.md
  scripting/        QuickJS-WASM engine, esbuild bundler, capability layer
  cli/              Cobra verbs, one binary — AGENTS.md
  mcp/              MCP-over-stdio server — AGENTS.md
  wsroot/           Workspace root discovery, user config/cache dirs, trust list
  echo/             Echo service implementation + its cmd/ server
ui/                 Frontend build gates — AGENTS.md
ui/src/
  App.tsx, main.tsx     App root + view switch — AGENTS.md
  components/shell/     AppShell, TopBar, Rail, StatusBar
  components/tree/      Domain-agnostic tree — AGENTS.md
  components/ui/        Nocturne design-system primitives
  features/workspace/   Request editor, tree, response pane, body/metadata
  features/sources/     Reflection-source configuration
  features/scripts/     Script authoring
  features/daemons/     Machine-wide daemon inventory (ServerService)
  lib/                  client.ts, ui-store.ts (zustand), workspace-query.ts, format.ts
  theme/                Nocturne tokens, fonts, Monaco theme — AGENTS.md
third_party/quickjs/  Vendored QuickJS-WASM build inputs
tools/                Repo tooling; releasing — AGENTS.md
docs/design/          Design docs and plans — docs/design/README.md
```

## Design docs

`docs/design/` is sorted by how much of each doc is real code —
[`docs/design/README.md`](docs/design/README.md) is authoritative on the layout and rules.
