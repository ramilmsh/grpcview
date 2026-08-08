# Design docs

Sorted by **how much of the doc is real code**, because that is the first thing you need
to know before reading one:

| Folder | Means |
|---|---|
| [`shipped/`](./shipped) | The arc is finished. Kept only for the decisions behind the code — never as a worklist. |
| [`active/`](./active) | **Stopped mid-arc** — written-out work that is genuinely unbuilt. Not "shipped, plus ideas": that is `shipped/` plus [`planned/roadmap.md`](./planned/roadmap.md). |
| [`planned/`](./planned) | Nothing built. A plan to argue with — or, in `roadmap.md`, a want with no plan behind it yet. |
| [`research/`](./research) | Background research, closed. Feeds the docs above; not a plan itself. |

Two docs sit at the top level because they are neither, and are read continuously:

- [`request-body-contract.md`](./request-body-contract.md) — **authoritative** on what a
  request body may be, across all four surfaces (web UI, VS Code, CLI, MCP). Anything in
  `docs/design/**` that contradicts it is stale.
- [`known-bugs.md`](./known-bugs.md) — defects found but deliberately not fixed, each
  because the fix needs a design call.

Every doc is written in the present tense about the code as it stood when it was written.
Read its `file.go:line` citations as the **premise of a decision**, not as a description
of trunk. For what the code does now, read `AGENTS.md`.

## shipped/

| Doc | Landed |
|---|---|
| [`storage.md`](./shipped/storage.md) | Git-versionable directory-tree storage; replaced the SQLite blob store. Also the layout reference. |
| [`cli-generator-exploration.md`](./shipped/cli-generator-exploration.md) | The whole CLI — cobra verbs on the one binary, `invoke` with real exit codes (2026-08-03). |
| [`gv-features-plan.md`](./shipped/gv-features-plan.md) | Folder-metadata inheritance, message-shape view, `gv.invoke` (2026-07-29). |
| [`typed-gv-invoke.md`](./shipped/typed-gv-invoke.md) | `gv.invoke` typed by the path it is given, editor-side (2026-08-05). Kept for D3/D4/D6, still open. |
| [`quickjs-wasm-spike.md`](./shipped/quickjs-wasm-spike.md) | The QuickJS→WASM toolchain the scripting engine is built on. |
| [`quickjs-wasm-capabilities-spike.md`](./shipped/quickjs-wasm-capabilities-spike.md) | The host-function capability layer. *Grant management is unbuilt* — everything ships fully sandboxed. |
| [`ui-redesign-plan.md`](./shipped/ui-redesign-plan.md) | The Nocturne rebuild. Kept for the design source-of-truth and the offline/single-file invariants. |
| [`scripting-ui-plan.md`](./shipped/scripting-ui-plan.md) | Scripts CRUD, authoring view, middleware in requests (S1–S3, 2026-07-23). |
| [`tree-rewrite-plan.md`](./shipped/tree-rewrite-plan.md) | The VS-Code-alike collection tree (2026-08-01). Kept for four rejected libraries and the calls each milestone forced. |
| [`vscode/phase-1-workspace.md`](./shipped/vscode/phase-1-workspace.md) | The workspace is the repo, collections are what's in it — 1a–1e (2026-08-06). One shipped phase of the [VS Code track](./active/vscode/README.md), whose phases 2–6 are still in `active/`. |
| [`mcp.md`](./shipped/mcp.md) | `grpcview mcp` — the `WorkspaceService` as MCP tools over stdio (2026-08-06). Kept for the three things the four-phase plan got wrong; the plan itself is deleted. |
| [`example-collection-fixes.md`](./shipped/example-collection-fixes.md) | Five defects an agent-only author hit building the `example` collection — all five fixed (2026-08-07). Kept for the measurements and for why the obvious fix to each was wrong. |
| [`daemon.md`](./shipped/daemon.md) | One daemon per workspace — registration file, connect-or-spawn, idle exit (2026-08-08). Kept for the naming/token/unix-socket calls it closes and the two deferrals it names. |

## active/

| Doc | Left to build |
|---|---|
| [`codebase-audit-plan.md`](./active/codebase-audit-plan.md) | Phases 0–3 + wave 0 done; **W1–W3 and the doc-reconcile phase not run.** |
| [`vscode/`](./active/vscode) | **Phases 2–6**, all planning. Phase 1 shipped and moved to `shipped/vscode/`; the README here is the track map. Includes [`body-contract.md`](./active/vscode/body-contract.md), which spans phases 2 and 5. |

## planned/

| Doc | What it plans |
|---|---|
| [`invoke-from-the-store.md`](./planned/invoke-from-the-store.md) | Send resolves the saved request server-side instead of shipping the editor buffer. The RPC now exists; the UI migration does not. |
| [`descriptor-explorer-plan.md`](./planned/descriptor-explorer-plan.md) | A navigable read-only `.proto` browser — field numbers and comments the TS-shape view cannot recover. |
| [`script-imports.md`](./planned/script-imports.md) | A script is a `.ts` file imported as `@/path/from/root`. Deletes `script.json`, `ScriptKind`, the generator prelude, and `gv` as a global. |
| [`cross-collection-invoke.md`](./planned/cross-collection-invoke.md) | `gv.invoke("//identity:Auth/Login")` — Bazel-style labels over per-collection slugs, never paths. Sibling of `script-imports.md`. |
| [`workspace-diagnostics.md`](./planned/workspace-diagnostics.md) | `grpcview check` — one pass reporting broken imports, invoke paths, slugs and sources. The precondition for letting the other two tracks break references. |
| [`roadmap.md`](./planned/roadmap.md) | **Wants, not plans** — the backlog the three shipped UI/scripting/tree docs used to carry. Each item gets its own doc when picked up. |

## Housekeeping

A doc moves folder when its status changes, and a `shipped/` doc is **deleted** once
nothing in it is worth keeping — the code and `AGENTS.md` are the source of truth, not a
plan archive. The reason to keep one is that it is the only record of a decision: which
libraries were rejected and why, which calls the build forced, what must not be violated.

**A multi-phase track sorts per doc, not as a block.** The VS Code track is the worked
example: phase 1 in `shipped/vscode/`, the daemon it spun off in `shipped/`, phases 2–6 in
`active/vscode/`. The track README is the map and must stay wherever the unbuilt phases
are, so a reader lands on the open work.

**A shipped plan does not stay `active/` because it ends in a wishlist.** Move the
leftovers to `roadmap.md` and the doc to `shipped/` — otherwise `active/` fills up with
finished work and stops meaning anything. And when carrying leftovers, re-check them
against trunk first: of the three docs sorted this way on 2026-08-06, five "remaining"
items had already shipped.
