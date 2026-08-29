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

## Documentation

**Be terse.** Every doc in this repo (this file, nested `AGENTS.md`s, `docs/design/`) states
facts, not narration — no preamble, no restating what the code already shows, no hedging.
Prefer a `file.go:line` pointer over a paragraph.

This file covers what's cross-cutting. Domain detail lives in an `AGENTS.md` next to the
code it describes (see Directory Structure below) — check there before duplicating
something here.

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

## Architecture

- **Frontend** (`ui/`): React 18 + TS SPA, Vite, single HTML file,
  embedded into the Go binary. See `ui/AGENTS.md`.
- **Backend** (`service/`): Go server, `WorkspaceService` over Connect
  (h2c) — reflection, invoke, disk persistence, scripting.
- **Store** (`service/store/`): filesystem-backed collection, protojson
  tree; on-disk schema (`grpcview.store.v1`) decoupled from wire
  (`grpcview.v1`), bridged by `convert.go`. See `service/store/AGENTS.md`.
- **Scripting** (`service/scripting/`): QuickJS-WASM (wazero), user JS/TS.
  Network always on; filesystem deny-by-default behind a `Grant`
  (`node:fs`); sources bundled with esbuild first. Request-authoring
  contract (body/metadata scripts, `grpcview:` modules, import sigils) is
  in `service/workspace/AGENTS.md`.

## Build System (Bazel)

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

Releasing: `tools/AGENTS.md`. Frontend-specific gates (typecheck/vitest/bundle): `ui/AGENTS.md`.

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
