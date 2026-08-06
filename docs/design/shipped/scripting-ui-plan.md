# grpcview — scripting UI plan (create & use scripts)

**Status:** **shipped** — S1–S3 landed 2026-07-23 on `trunk` and the
"author a script, then use it in a request" loop is live. What this doc is *for* now
is the enduring decisions the shipped work rests on (§"Enduring decisions"), not a
worklist; implementation detail lives in the code and `AGENTS.md`. The deferred
S4–S6 milestones moved to [`roadmap.md`](../planned/roadmap.md).
**Companion to:** [`ui-redesign-plan.md`](./ui-redesign-plan.md) (client track) and
the QuickJS·WASM spikes ([`quickjs-wasm-spike.md`](./quickjs-wasm-spike.md),
[`quickjs-wasm-capabilities-spike.md`](./quickjs-wasm-capabilities-spike.md)).
**Reference mockups (Claude Design `gRPC Request Client Design`, id `fcd41471-c260-40d0-9b27-b8517a5606e3`):** `gRPC Workspace.dc.html` (Scripts view, Middleware tab, consent, package store, registries, grants, scenarios, environments) and `Scripting Engine - Design Plan.dc.html`. Re-fetch with `DesignSync get_file`.

---

## What shipped (S1–S3)

The "author a script, then use it in a request" loop is live:

- **S1 — Scripts CRUD + authoring view.** Scripts are committed collection state
  (`scripts/<slug>/script.json`, ordered by `Collection.scripts`), authored in the
  Scripts view for all three kinds, fully sandboxed. The engine gained the
  **entry-point calling convention** (`service/scripting/entry.go`): a generator's
  `export default(args)` and a middleware's `handle(ctx)` are *called*, not
  evaluated as a last expression.
- **S3 — Middleware in requests.** A request carries an ordered `middleware` list;
  the chain runs pre-send (`service/workspace/middleware.go`), threading
  `{body, metadata, target}` through each `RunMiddleware` call.
- **S2 — generators in requests — SUPERSEDED.** S2 originally shipped a
  `{{ name(args?) }}` token syntax plus a binding-editor modal. That mechanism was
  **replaced** by the TypeScript request-body model: a body/metadata is authored as
  typed TS and can **call saved generators directly** (they are resolved into the
  bundle at eval time), so there are no `{{ }}` tokens and no binding editor. See
  the "Request authoring model" section of `AGENTS.md`; the old backend `tokens.go`
  and UI `tokens.ts` / `BindingEditor` have been removed.

---

## Enduring decisions

1. **Sandboxed-only (for now).** Scripts run with zero host capabilities; the
   `node:fs`/`node:net` shims stay ungranted. Grants, launch consent, and
   `std/http` are **S4**.
2. **Scripts are committed collection state.** They ship over git with the
   collection (the engine's threat model assumes exactly this), living in a
   `scripts/` directory beside `tree/`, not under gitignored `.grpcview/`.
   Persistence mirrors requests: `scripts/<slug>/script.json` holds
   `Script{ meta, kind, source }`.
3. **Three kinds** — `ScriptKind = GENERATOR | MIDDLEWARE | SCENARIO`.
4. **Entry-point calling convention is foundational** — test-run, generator use,
   and middleware execution all depend on the engine calling the authored export.

---

## Current-state snapshot

- **Engine** (`service/scripting`): `Engine.RunGenerator/RunMiddleware/RunScenario(ctx, source, Grant, Input) (Result, error)`; `Grant{FS,Net}` (nil = denied), `Result{Value json.RawMessage, Logs []LogLine}`. esbuild bundles the source → an ES2022 blob; the embedded npm registry (dayjs) self-provisions.
- **Store** (`service/store`): FS-backed `Collection` — request/folder/script CRUD, history append, and descriptor state. Layout `tree/<slug>/{request,folder}.json`, `scripts/<slug>/script.json`, root `grpcview.json`, gitignored `.grpcview/`. Disk schema (`grpcview.store.v1`) is bridged to the wire schema by `convert.go`.
- **Frontend** (`ui/src`): React + `@connectrpc/connect-query` + zustand + Nocturne; `Rail` switches `activeView` (`workspace | sources | scripts`).

---

## Deferred roadmap (S4–S6)

Moved to [`roadmap.md`](../planned/roadmap.md) — S4 capabilities/grants/consent, S5
npm dependency management, S6 Scenarios + Environments. They were three sentences of
intent, which is a want and not a plan; each gets its own doc when it is picked up.

One S4 line needs care, because half of it landed elsewhere: `gv.invoke` shipped with
[`gv-features-plan.md`](./gv-features-plan.md) (2026-07-29), ungated — reaching a
saved request in the same collection is not a host capability. But it shipped for
request bodies and middleware only. **Generators still cannot invoke**, by design:
`RunGenerator` is cached by `configDigest`, so it must see a throwing `invoke` for the
cached prelude to stay identical run-to-run. "`invoke()` for generators" therefore
remains open, and it is a cache-invalidation question rather than a grant question.

---

## Cross-cutting risks (still apply)

- **Untrusted code on the invoke path** — body/metadata evaluation and generator
  calls run user JS at send time, bounded by the engine's mem/time limits and (for
  now) zero capabilities; the config-digest cache keeps repeated resolves cheap.
- **Identity is name-derived** (like requests) — a script rename changes its
  slug/key; the sidebar selection + open buffer must remap.
- **Streaming parity** — middleware (and any future pre-send resolution) must apply
  to `InvokeStreaming` as well as unary `Invoke`; the pre-send pipeline is shared.
- **Verification is Bazel + browser** — every milestone: `bazel
  build //service/cmd` clean, then drive the real flow against the isolated-`HOME`
  prod binary (and the echo server) before it is called done.
