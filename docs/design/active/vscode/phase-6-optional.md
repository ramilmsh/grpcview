# Phase 6 — optional and speculative

**Prereqs:** [phase 3](./phase-3-type-sinks.md) for the sink seam.
See [`README.md`](./README.md) for the producer/sink model.

Nothing here is required for the extension to ship. Each item is recorded with its
actual justification, because two of them are weaker than they first appear.

## `protoc-gen-es` under QuickJS

**What:** move type generation into the Go binary, running `protoc-gen-es` inside the
existing QuickJS-WASM engine (bundled by the existing esbuild integration). One
producer serving every sink, in-process, single static binary preserved.

**Why it is *not* foundational.** This was originally scoped as phase 0 on the premise
that VS Code needs types on disk. It does not: the primary editing path is Monaco in a
webview, where in-memory `addExtraLib` already works, and the extension host is Node
and can run `protoc-gen-es` directly. That leaves two genuine but modest
justifications:

- **Deletes browser hackery** — `ui/src/features/workspace/vendor/bufbuild-stubs.ts`
  plus the `typescript` / `@typescript/vfs` no-op aliases in `ui/vite.config.ts`, which
  exist solely to make a codegen plugin run in a page.
- **Shrinks the single-file HTML.** Note the lazy chunk at `proto-types.ts:12` saves no
  *shipped* bytes — `vite-plugin-singlefile` inlines dynamic imports, so it only defers
  parse.

**Feasibility signal:** generation is a pure function with no I/O, and the `typescript`
imports `protoc-gen-es` pulls in are already stubbed out on the `target=ts` path
(`proto-types.ts:8`), so the dependency surface is smaller than it looks. Worth a
throwaway spike before committing.

## tsserver-plugin sink

**What:** ship a small tsserver plugin via `contributes.typescriptServerPlugins`. The
plugin wraps the `LanguageServiceHost` with an overlay of virtual files; the extension
pushes the generated sources in via
`getExtension('vscode.typescript-language-features').exports.getAPI(0).configurePlugin(...)`.
This is how Volar serves virtual files to tsserver.

**Payoff:** native tsserver IntelliSense on `body.ts` with **nothing written to disk** —
it removes the need for `DiskSink` in the editor case entirely, and removes the
"unresolved `RequestMessage`" wart from [phase 2](../../shipped/vscode/phase-2-body-files.md).

**Why a REST endpoint cannot do this instead:** TypeScript's host interface is
**synchronous** — `readFile`, `fileExists`, `readDirectory` return values, not promises,
and are called during module resolution. Nothing can await a fetch there. The content
must already be in memory when tsserver asks, so the model is push, not pull. The
transport can still be the existing Connect API; the consumer has to be an in-memory
overlay.

**Costs:**

- VS Code-specific — the contribution point is. A plugin loaded via tsconfig `plugins`
  works in other editors but has no channel to the backend.
- **`tsc` ignores `plugins` entirely** (they decorate the language *service*, not the
  program `tsc` builds), so this contributes nothing to CI.
- Known shelf life: `microsoft/typescript-go` cannot dynamically link Go plugins, so
  the plugin model is slated for replacement (see below).

## tsgo IPC sink — speculative

**What:** `microsoft/typescript-go` proposes an IPC-based API replacing the plugin
model, with "opening and managing virtual files within projects" among its capabilities
([#2824](https://github.com/microsoft/typescript-go/issues/2824)). grpcview would
register its generated types as virtual files over IPC.

**Why it is the right long-term shape:** editor-agnostic (any editor driving tsgo's
LSP), no dynamic linking, and grpcview stays a *client* of the type server — the same
posture as the extension talking Connect. It may also close the CI gap that plugins
structurally cannot.

**Why not to plan against it:** all packages are `internal/` (including
`internal/vfs/{osvfs,iovfs,cachedvfs}`, exactly what one would want to wrap), the README
states the API is "not ready", and #2824 is exploratory, opened Feb 2026, milestone
Post-7.0. Landing the API is also not sufficient — it only helps users whose editor has
moved off TS 5.x tsserver.

Note the push model does not change: tsgo will not pull from a REST endpoint on demand,
so whatever is built for the plugin sink is largely reusable here.

*(Unrelated: `koltyakov/tsgo` is a third-party TS-execution library for Go, not
Microsoft's compiler. Easy to conflate when searching.)*

## CI type-checking

`tsc` has **no pre-generation hook** — no `prebuild` equivalent, `--build` orders
*projects* rather than commands, and `plugins` are ignored. `ts-patch` enables emit
transformers but injecting new source files from one is unsupported hackery; patching
the compiler to fix a codegen ordering problem is a bad trade.

Two real routes:

1. **Custom `CompilerHost`** — drive the compiler API with generation and checking as
   one in-process step. No persisted artifact, so staleness is structurally impossible.
   Needs Node (or tsgo, someday).
2. **In-webview check** — Monaco's TS worker already holds types freshly generated from
   the current descriptor set, so a "check all requests" action is never stale by
   construction. **Nearly free given what already exists**; do this first.

## Deferred: check files and the AST lint

Layers 2 and 3 of [`body-contract.md`](./body-contract.md) depend on `DiskSink`, so they
land here at the earliest. Layer 4 (runtime) is not optional and ships in phase 2.
