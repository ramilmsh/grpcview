# grpcview — VS Code compatibility track

**Status:** Planning only. **Not started.**

**Goal.** Run grpcview as a VS Code extension while keeping the standalone web UI as
a first-class frontend, and borrow VS Code's *request management* model (document
identity, tabs, dirty state, quick-open) in both.

Each phase is a self-contained doc. Read this file first for the approach and the
two architecture decisions every phase depends on.

| Phase | Doc | Summary |
|---|---|---|
| 1 | [`phase-1-workspace.md`](./phase-1-workspace.md) | The workspace is the repo; collections are what's in it |
| 2 | [`phase-2-body-files.md`](./phase-2-body-files.md) | Body + metadata become real `.ts` files on disk |
| 3 | [`phase-3-type-sinks.md`](./phase-3-type-sinks.md) | Extract the type producer/sink seam |
| 4 | [`phase-4-request-management.md`](./phase-4-request-management.md) | Document identity, dirty state, preview tabs, commands |
| 5 | [`phase-5-extension.md`](./phase-5-extension.md) | The extension itself |
| 6 | [`phase-6-optional.md`](./phase-6-optional.md) | QuickJS producer, tsserver plugin, tsgo IPC, CI checking |
| — | [`body-contract.md`](./body-contract.md) | Enforcing the body/metadata export signature (spans 2 and 5) |
| — | [`../daemon.md`](../daemon.md) | One daemon per workspace — was phase 1's Decision 10; depends only on 1a |

---

## Chosen approach — custom editor + file-backed bodies

The extension binds a **custom editor** to each request, so tabs, dirty state,
Explorer, git decorations, diff and quick-open come from VS Code. Inside that editor
is the *existing* React cockpit with its Monaco body editor — so the hidden-wrapper
authoring mode (`body-wrapper.ts`) survives intact. Separately, bodies and metadata
move onto disk as real `.ts` files, which buys line diffs, `import`s from shared
modules, and a portable authoring format.

**Rejected:** a plain webview port (gives no VS Code request management, which is the
point of the exercise); replacing the body editor with VS Code's native text editor
as the *primary* path (a request would occupy up to three tabs and stop being one
thing). The native path survives as an optional "reveal body as file" escape hatch.

## What we already have (feasibility)

- **The UI is a pure Connect client.** `ui/src/lib/client.ts:5` is a single
  `baseUrl` — the whole seam between frontend and backend.
- **The backend already runs headless** (`service/cmd/dev`) with a `--port` flag
  (`service/service.go:35`).
- **The store is stateless per operation.** Every RPC calls `store.Open(ctx, name)`
  and re-reads from disk (`workspace.go:81,216,256`, `invoke.go:606,739,893,970`,
  `middleware.go:146,165`, `gvinvoke.go:115`); `Store.mu` is only a write lock.
  **Consequence: externally-edited files are already picked up on the next RPC.** A
  file watcher is needed only to *push* refreshes to a connected UI, never for
  correctness.
- **Storage is already the Bruno model** — a request is a directory with
  `request.json` (`layout.go:17`), stable slugs (`layout.go:120`), and renames that
  edit only the file and never move the directory.
- **Bodies are already canonical TS modules.** `isCanonical`/`migrateBodyToTs`
  (`body-wrapper.ts`) guarantee every persisted `draft_body` is a complete module, so
  the file split is close to `writeFile(dir/body.ts, draftBody)`.
- **Type generation is already written and pure** — `generateWorkspaceTypes`,
  `resolveLocalSymbol`, `requestMessageAlias` (`proto-types.ts`) are string-in /
  string-out, so they run unchanged in a browser page *or* in the Node extension host.

---

## Decision 1 — types are produced, not stored

The type set is a pure function of (merged `FileDescriptorSet`, per-request
`service`+`method`). One **producer** builds a stamped snapshot; N write-only
**sinks** publish it.

```ts
interface TypeSnapshot {
  digest: string;                            // descriptor-set digest — the freshness stamp
  files: Map<string, string>;                // generated "<path>_pb.ts" → TS source
  perRequest: Map<string, RequestTypeRef>;   // requestId → { symbol, importPath, dts }
}

interface TypeSink {
  publish(snap: TypeSnapshot): Promise<void>;   // write-only
  publishedDigest(): string | undefined;
}
```

**Load-bearing invariant: no sink is ever read to answer "what are the current
types."** That comes from the producer. This is what keeps two live sinks from
becoming two caches to reconcile — the failure mode that makes generated files go
stale.

| Sink | Consumer | Phase |
|---|---|---|
| `MonacoSink` (`addExtraLib`) | standalone UI + extension cockpit | 3 |
| `DiskSink` (`.grpcview/types/`) | native editors, `tsc`, AI tooling | 5, optional |
| tsserver-plugin overlay (push via `configurePlugin`) | VS Code native editors, no files | 6 |
| tsgo IPC virtual files | any tsgo editor | 6, speculative |

**Why a sink is not a dumb `write(files)`.** `requestMessageAlias()` emits a
*per-method* `declare global { type RequestMessage = … }` at a constant path
(`proto-types.ts:113`). In-memory that is correct — one editor, one active method. On
disk, N bodies each declaring it differently collide, so `DiskSink` must render the
per-request part differently (a generated `import type … as RequestMessage` line,
tool-rewritten on method change). The snapshot carries the data; the sink chooses the
rendering.

**Producers run in two hosts, sharing one pure module.** The browser page produces
for `MonacoSink`; the **extension host** (Node) produces for `DiskSink`, because
`DiskSink` must stay fresh even with no webview open. Generation is deterministic, so
both producing at once is benign — `DiskSink` writes only when the digest differs, so
no mtime churn.

## Decision 2 — staleness discipline

Generated artifacts go stale when they are *persisted* **and** generated *manually*.
Break both:

1. **Publish on the mutations that invalidate** — `AddDescriptorSource`,
   `RefreshDescriptorSource`, `ReorderDescriptorSources`, `UpdateRequest` — plus once
   on startup. This mirrors the existing rule for resolve caches ("the merged view is
   re-derived from those caches on every mutation").
2. **Stamp the output.** `DiskSink` writes a `types.stamp` carrying the descriptor
   digest and each request's `(service, method)`. Consumers verify before trusting, so
   staleness is a loud error and never a silent check against last week's shape.

## Relationship to the descriptor explorer

[`../descriptor-explorer-plan.md`](../descriptor-explorer-plan.md) has the same
*shape* — a derived artifact set keyed by descriptor digest, invalidated on source
mutation, rendered into Monaco as a virtual file set. It shares the
digest/invalidation discipline and a thin "register a virtual file set into Monaco"
helper (its `proto://<src>/<path>` models vs. type extra-libs). It does **not** share
a producer: that one is `protoprint` in Go emitting `.proto` text plus a symbol index.
Do not unify further.

## Cross-phase open questions

- **Autosave or explicit save** (phase 4)? Explicit is the VS Code-native answer and
  is forced for file-backed fields; autosave is what exists today.
- **File watcher** — needed for live refresh of a connected UI and for git
  operations, not for correctness. Which phase absorbs it?
- **Does the standalone UI keep `MonacoSink` only**, or opt into `DiskSink` for parity
  when running against a local directory?
