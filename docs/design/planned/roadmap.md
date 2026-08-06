# Roadmap — wanted, not yet planned

The backlog that used to be stapled to the end of three shipped plans
([`ui-redesign-plan.md`](../shipped/ui-redesign-plan.md) §4,
[`scripting-ui-plan.md`](../shipped/scripting-ui-plan.md) S4–S6,
[`tree-rewrite-plan.md`](../shipped/tree-rewrite-plan.md) T3/T7/T8). Those docs describe
finished arcs; this is the part of them that was still a wishlist, collected so the wishlist
is in one place and the shipped docs stop reading like worklists.

**Nothing here is planned.** Each item is a paragraph of intent, not a sequence you can
execute — when one is picked up it gets its own doc in this folder. The distinction
matters: [`daemon.md`](./daemon.md) and [`invoke-from-the-store.md`](./invoke-from-the-store.md)
are *plans* with decisions and steps; these are *wants*.

**Statuses below were re-verified against trunk on 2026-08-06**, and several items the old
docs listed as remaining had in fact shipped — see the last section, which is the reason
this file exists rather than a straight copy.

---

## Scripting — the three deferred milestones

Carried from `scripting-ui-plan.md`, which shipped S1–S3 (scripts CRUD + authoring view,
middleware in requests). Everything today runs **fully sandboxed with zero host
capabilities**, and these are what change that.

- **S4 — Capabilities, grants, launch consent.** Productionize `std/http` (opt-in,
  host-scoped) and `exec` (binary allowlist); digest-pinned per-script grants, kept local
  and never committed; the Capabilities subtab (requested-vs-granted, scope, worst case);
  the launch-consent modal (triggered by a foreign origin or a changed digest); the Grants
  management view. The capability *mechanism* is already proven and in the engine —
  [`quickjs-wasm-capabilities-spike.md`](../shipped/quickjs-wasm-capabilities-spike.md) —
  so this is the UI and the grant store, not the sandbox.
- **S5 — npm dependency management.** A GUI package manager over the engine's existing npm
  resolver/store: the Dependencies subtab (direct + resolved tree, integrity/tamper), the
  Add-package modal (registry search, pin by digest), the package store
  (content-addressed, prune, re-verify), and Registries (scope mapping, priority, keychain
  tokens).
- **S6 — Scenarios and Environments.** The Scenarios view (list, editor, results tree, call
  chain) — `ScriptKind.SCENARIO` and `Engine.RunScenario` exist, no view does. And the
  Environments view: `env`/`vars`/`secrets` and their resolution, with `.expires_at`
  caching, which also enriches generators. Note the old `{{ }}` token + binding-editor
  design for this was **superseded** by calling generators directly from typed TS bodies.

## The collection tree — three orphan milestones

Carried from `tree-rewrite-plan.md`, whose own closing note demanded exactly this: it is
the only record of these, and it refuses to be deleted until they live somewhere that
survives.

- **T3 — Typeahead.** 1s buffer, wrap-around, match highlight, and it must **compose with
  the filter box rather than replace it** (that plan's "Deliberate deviations" #3). Wholly
  unbuilt: letter keys are unclaimed by `ui/src/components/tree/keymap.ts` and there is no
  `typeahead.ts`.
- **T7 — Optional polish.** Compact folders (behind a flag), sticky scroll, and
  virtualization via `@tanstack/react-virtual` **only if** a real collection ever makes it
  necessary. Deliberately not speculative.
- **T8 — Async children.** Turn on the `getChildren` promise path: children cache, loading
  placeholders, stale-response guards, `invalidate`. Owned by the descriptor-explorer track
  ([`descriptor-explorer-plan.md`](./descriptor-explorer-plan.md)) and on its critical path
  — the first lazy portable provider stops being shareable without it. Build it when that
  provider exists, not before.

## Client UI

Carried from `ui-redesign-plan.md` §4, minus everything that has since shipped.

- **Two more descriptor-source kinds.** A buf registry source, and proto files with import
  resolution. Reflection, bazel labels and uploaded descriptor sets all ship today.
- **Source freshness and collision resolution.** `RefreshDescriptorSource` exists, but
  nothing records or surfaces *when* a source was resolved or what version it was, and
  service-name collisions between sources are resolved by the priority merge without ever
  being shown.
- **Auth / Options / Variants request-pane tabs.** The pane has Message, Metadata and
  Middleware (`RequestPane.tsx`); these three are unbuilt, and auth in particular overlaps
  the environments/secrets work in S6.
- **⌘K search and Settings.** Both are present in `TopBar.tsx` as **disabled buttons**
  labelled "not available in Phase 1" — placeholders with nothing behind them.
- **Git integration.** [`storage.md`](../shipped/storage.md) locks in "the UI is
  **git-aware** (backend serves git status/diff/history to the frontend, prefer
  `go-git`)", and [`research/go-git.md`](../research/go-git.md) is the closed research for
  it. No RPC and no UI exist. Monaco's bundled `DiffEditor` is the intended diff surface.

## The MCP server — four leftovers

Carried from the MCP track, which shipped on 2026-08-06; the decisions behind it are in
[`shipped/mcp.md`](../shipped/mcp.md), and the behaviour is `AGENTS.md` §"The MCP server".

- **`bytes` → `string` for the four fields holding JSON text.**
  `Request.Response.response`, `History.Request.body`, `History.Response.response` and
  `InvokeStreamingResponse.message` all carry UTF-8 JSON, never arbitrary binary, so
  protojson base64-encodes them and every consumer decodes. Verified live: `invoke` over
  MCP returns `"eyJtZXNzYWdlIjoi…"` where the value is `{"message":"echo: hi from mcp"}`.
  Blast radius is the store schema, `convert.go`, `invoke.go`'s two assignment sites, and
  the UI's decoders. `AddDescriptorSourceRequest.descriptor_set` and
  `Collection.descriptor_set` stay `bytes` — those really are binary.
- **`history` dominates every collection-returning response.** `get_collection` on the
  repo's own `example` collection is 168 KB, of which `history` is 158 KB across 15
  requests. That lands in the calling agent's context on every mutation too, since they all
  return a `Collection`. Stripping it in the shim is one line in the existing
  `stripDescriptorSets` walk, but it is a **capability trade-off, not a pure win**: no other
  tool exposes history, so stripping removes an agent's only access to it. Decide the
  trade, don't fix it by reflex.
- **`invoke_stream`.** The plugin skips streaming methods, so this is the one tool that
  cannot be generated and needs a hand-written schema. Shape:
  `invoke_stream(spec, messages[]) -> {messages[], status, …, truncated}`, draining to
  completion under an explicit frame cap, byte cap and timeout, all three named in the
  description. "Run to completion, return everything" is the only faithful collapse of a
  stream into a request/response tool call. Do it when a real session is blocked by
  `invoke`'s `Unimplemented`, not before.
- **`find_method(query)`.** Substring search over the collection's services, returning
  matches instead of the whole list. A four-surface RPC (UI quick-open, CLI `--grep`, VS
  Code palette), not an MCP-layer concern. `services` is only 4% of a response today, so
  this is about ergonomics on a large API rather than payload size.

---

## Already shipped — items these docs still listed as remaining

Re-verified on 2026-08-06. Recording them because each was a stale "remaining" bullet, and
a backlog that lies about what is left is worse than no backlog.

| Was listed as remaining | Actually shipped |
|---|---|
| Streaming UI (kind in tree/tabs, message cards, stop, msg count) | `methodKind` tags in the tree, tabs and method picker (`lib/format.ts:139`); `MessagesTab` composes client-streaming/bidi; stop is an `AbortController` per key (`RequestWorkspace.tsx:230-261`) |
| History UI (a Timeline over `Request.history[]`) | The response pane's History subtab + `HistoryTimeline` with re-run (`ResponsePane.tsx:236-355`) |
| Multi-collection switcher | Switch, create and rename collections from the top bar (`collection-switcher.ts`, `NewCollectionDialog`, `RenameCollectionDialog`) |
| Sources: priority/reorder, per-source attribution | `ReorderDescriptorSources` RPC + the sources table — the definition-sources track (2026-07-29) |
| Descriptor-set upload ("currently `Unimplemented` server-side") | Uploads land as content-addressed blobs with a refresh recipe; a bazel label is a source kind too |
