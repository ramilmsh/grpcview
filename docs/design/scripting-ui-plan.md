# grpcview — scripting UI plan (create & use scripts)

**Status:** S1–S3 shipped (2026-07-23, on `trunk`); **S4–S6 deferred.** This doc
now tracks the *remaining* roadmap (S4–S6) plus the enduring decisions the shipped
work rests on. Implementation detail for the shipped milestones lives in the code
(and `AGENTS.md`), not here.
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

| # | Milestone | Delivers |
|---|---|---|
| S4 | Capabilities · grants · launch consent · `std/http` | security |
| S5 | npm dependency management (Dependencies · Add-package · Store · Registries) | packages |
| S6 | Scenarios view · Environments view | test/env |

- **S4 — Capabilities · grants · consent.** Productionize `std/http` (opt-in,
  host-scoped) + `exec` (binary allowlist); digest-pinned per-script grants (local,
  never committed); the Capabilities subtab (requested-vs-granted, scope,
  worst-case); the **launch-consent** modal (foreign-origin/changed-digest
  trigger); the **Grants** management view; `invoke()` for generators reaching
  other requests. Unblocks the mockup's `auth.bearer`/`vault.token` examples.
- **S5 — npm dependency management (GUI package manager).** Dependencies subtab
  (direct + resolved tree, integrity/tamper), **Add-package** modal (registry
  search → pin by digest), **Package store** (content-addressed, prune,
  re-verify), **Registries** (scope mapping, priority, keychain tokens). Rides the
  engine's existing npm resolver/store.
- **S6 — Scenarios + Environments.** Scenarios view (list + editor + results tree +
  call chain); Environments view (`env`/`vars`/`secrets`, resolution against them,
  `.expires_at` caching) — which also enriches generators.

---

## Cross-cutting risks (still apply)

- **Untrusted code on the invoke path** — body/metadata evaluation and generator
  calls run user JS at send time, bounded by the engine's mem/time limits and (for
  now) zero capabilities; the config-digest cache keeps repeated resolves cheap.
- **Identity is name-derived** (like requests) — a script rename changes its
  slug/key; the sidebar selection + open buffer must remap.
- **Streaming parity** — middleware (and any future pre-send resolution) must apply
  to `InvokeStreaming` as well as unary `Invoke`; the pre-send pipeline is shared.
- **Verification is Bazel + browser** — every milestone: `env GOPROXY=off bazel
  build //service/cmd` clean, then drive the real flow against the isolated-`HOME`
  prod binary (and the echo server) before it is called done.
