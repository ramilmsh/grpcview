# grpcview — scripting UI plan (create & use scripts)

**Status:** Draft · 2026-07-23
**Companion to:** the *Scripting Engine — Design Plan* (engine Phase 0–7), [`next-steps.md`](./next-steps.md) (N1–N4, done), [`ui-redesign-plan.md`](./ui-redesign-plan.md) (client track).
**Reference mockups (Claude Design project `gRPC Request Client Design`, id `fcd41471-c260-40d0-9b27-b8517a5606e3`):** `gRPC Workspace.dc.html` (Scripts view, binding editor, Middleware tab, consent, package store, registries, grants, scenarios, environments) and `Scripting Engine - Design Plan.dc.html`. Re-fetch with `DesignSync get_file`.

---

## 0. Why this plan exists

The QuickJS·WASM **engine is done** through the design's **Phase 1 (core)** and **Phase 2 (esbuild + npm)**: three execution profiles (`RunGenerator`/`RunMiddleware`/`RunScenario`), a two-gate capability system (`node:fs`/`node:path`/`node:net` shims), a digest-keyed generator cache, a warm middleware pool, and dayjs wired into a Monaco editor with IntelliSense. It is reachable from exactly one RPC — `RunScript` — which the current `ScriptsView` uses as a **scratchpad**: one buffer, scenario profile, **no capabilities, no inputs, no persistence, no script kinds**.

So *script processing* exists but you cannot **create** a script (nothing persists) or **use** one (nothing wires a script into a request). This plan sequences the milestones that close that gap — it is the convergence the other two plans defer to: engine **Phase 6 (Management UI)** ∪ UI-plan **Phase 5 (generators / `{{ }}` tokens / binding editor)**.

---

## 1. Roadmap at a glance

| # | Milestone | Delivers | Status |
|---|---|---|---|
| **S1** | **Scripts CRUD + authoring view** | *create* scripts | **✅ DONE 2026-07-23** |
| **S2** | **Generators in requests** — `{{ }}` tokens + binding editor | *use* (values) | **✅ DONE 2026-07-23** |
| **S3** | **Middleware in requests** — attach + run-before-invoke | *use* (rewrite) | **NEXT** |
| S4 | Capabilities · grants · launch consent · `std/http` | security | deferred |
| S5 | npm dependency management (Dependencies · Add-package · Store · Registries) | packages | deferred |
| S6 | Scenarios view · Environments view | test/env | deferred |

**This pass builds S1–S3** — the coherent "author a script, then use it in a request" loop. **S4–S6** (the management/security/package surface, scenarios, environments) are each their own multi-agent effort and are sketched in §6.

Each milestone follows the established N1–N4 shape: storage-proto → wire-proto → backend/engine → connect-query → UI, checkpoint-committed on `trunk`, built with Bazel and e2e-verified against the self-reflecting prod binary (isolated `HOME`).

---

## 2. Confirmed decisions (this pass)

1. **Sandboxed-only.** Scripts run with **zero host capabilities** — the `node:fs`/`node:net` shims stay ungranted. The Capabilities tab shows the "fully sandboxed" empty state. Grants, launch consent, and `std/http` are **S4**. This keeps S1–S3 free of the digest-pinned-grant/consent machinery.
2. **Scripts are committed collection state.** They ship over git with the collection (the engine's threat model assumes exactly this), so they live in the **committed tree**, not under gitignored `.grpcview/`. New `scripts/` directory, sibling of `tree/`.
3. **Persistence mirrors requests.** `scripts/<slug>/script.json` holds `Script{ meta, kind, source }` with `source` an inline UTF-8 string — exactly how `request.json` stores `draft_body`. `grpcview.json` (`Collection`) gains `repeated string scripts` for ordering. (Splitting `source` into a sibling `.ts` for nicer diffs is a mechanical follow-up, not this pass.)
4. **Three kinds.** `ScriptKind` = `GENERATOR | MIDDLEWARE | SCENARIO`. S1 authors all three; S2 *uses* generators; S3 *uses* middleware; a full Scenarios runner is S6 (scenarios are still authorable + test-runnable in S1).
5. **Entry-point calling convention (engine, lands in S1).** Saved scripts run under the authored contract shown in the mockup, **not** last-expression eval:
   - **generator** — `export default (…args) => value` (sync or async)
   - **middleware** — `export function handle(ctx) { … return ctx }` (or `export default`)
   The ad-hoc scratchpad path (`RunScript` with no kind) keeps last-expression eval, so the existing scratchpad is unchanged.
6. **Token syntax (S2).** `{{ name(args?) }}` — a reference to a generator by (possibly dotted) name, optionally called with JSON-literal args. Resolved server-side in the invoke path, pre-send.
7. **Digest is display-only this pass.** The header `cfg:…` chip is a short client-side hash of the source, for orientation. The real config digest that a grant binds to is **S4**.

---

## 3. Current-state snapshot (verified 2026-07-23)

- **Engine** (`service/scripting`): `Engine.RunGenerator/RunMiddleware/RunScenario(ctx, source, Grant, Input) (Result, error)`. `Grant{FS,Net}` (nil = denied), `Input{Request{body,metadata,target}, Vars, Secrets, Env}`, `Result{Value json.RawMessage, Logs []LogLine}`. Bundler = esbuild → global-evaluable ES2022 blob; **value = last top-level expression** today. Embedded npm registry (dayjs) self-provisions.
- **Store** (`service/store`): FS-backed `Collection` with `CreateRequest/CreateFolder/UpdateRequest/Delete/Move/AppendHistory/PutDescriptorState`. Layout: `tree/<slug>/{request,folder}.json`, root `grpcview.json`, gitignored `.grpcview/`. Disk schema (`grpcview.store.v1`) is deliberately distinct from the wire schema; `convert.go` bridges them.
- **Service** (`service/workspace`): `WorkspaceService`. `RunScript` runs the scenario profile with empty `Grant`/`Input`. Owns the shared `*scripting.Engine`.
- **Frontend** (`ui/src`): React + `@connectrpc/connect-query` + zustand + Nocturne theme. `Rail.tsx` switches `activeView` (`workspace | sources | scripts`). `ScriptsView.tsx` = the scratchpad. `ui-store.ts` = UI state (tabs/drafts/invokes). `workspace-query.ts` = the data layer (every mutation returns the fresh `Workspace` and re-seeds the `Get` cache).
- **Codegen (Bazel only):** Go proto is built + embedded by `go_proto_library` (no source copy). TS proto is copied into the source tree by `bazel run //proto/grpcview/v1:grpcviewv1_ts_proto.copy` after any `.proto` edit; new `WorkspaceService` RPCs are auto-included in the generated connect-query hooks. Never run `go`/`npm`/`pnpm` directly.

---

## S1 — Scripts CRUD + authoring view  *(create)*

> **Status: DONE & green (2026-07-23).** `bazel build //service/cmd //ui:ui` + `//service/{scripting,store,workspace}` tests all pass; e2e-driven in the browser against the isolated-`HOME` prod binary. Scripts persist as committed collection state (`scripts/<slug>/script.json` + `grpcview.json` `scripts[]` ordering, verified on disk). The engine gained the **entry-point calling convention** (`service/scripting/entry.go`): a generator's `export default(args)` and a middleware's `handle(ctx)` are *called* (async-settled via the existing pump), while the ad-hoc scratchpad path keeps last-expression eval — `configDigest` now folds in generator args. New backend files `service/store/scripts.go`, `service/workspace/scripts_test.go`; the Scripts view was rebuilt (`ui/src/features/scripts/ScriptsView.tsx`, +`ui-store`/`workspace-query` wiring). Verified live: created a generator (`export default`→ISO string) and a middleware (`handle(ctx)`→mutated ctx + console capture), saved (on-disk round-trip), survived a reload, and deleted (disk + manifest cleanup), zero console errors. **Note:** the scratchpad folded into the Scenario kind (a Scenario test-run = last-expression eval); a brand-new script opens with a per-kind starter skeleton; the header digest is a display-only client hash (the grant-binding digest is S4).

**Goal.** Persist scripts in the collection and author them in a redesigned Scripts view matching the mockup: create, list, filter, rename, edit, save, delete, and **test-run** any of the three kinds — fully sandboxed.

**Proto — `proto/grpcview/store/v1/storage.proto`:**
- `enum ScriptKind { SCRIPT_KIND_UNSPECIFIED=0; SCRIPT_KIND_GENERATOR=1; SCRIPT_KIND_MIDDLEWARE=2; SCRIPT_KIND_SCENARIO=3; }`
- `message Script { ItemMeta meta=1; ScriptKind kind=2; string source=3; }`
- `Collection`: add `repeated string scripts=5;` (ordered script slugs).

**Proto — `proto/grpcview/v1/workspace.proto`:**
- Mirror `ScriptKind`.
- `message Script { string name=1; ScriptKind kind=2; string source=3; }`
- `Workspace`: add `repeated Script scripts=5;`.

**Proto — `proto/grpcview/v1/service.proto`:**
- `CreateScript(workspace_name, name, kind) → {Workspace}`
- `UpdateScript(workspace_name, name, optional string source, optional string new_name) → {Workspace}`
- `DeleteScript(workspace_name, name) → {Workspace}`
- Extend `RunScriptRequest` with `optional ScriptKind kind=2;` — test-run evaluates the **current editor buffer** under that kind's profile (unset ⇒ scenario/scratchpad, unchanged).

**Backend — `service/store` (`layout.go`/`fs.go`/`convert.go`):** a `scripts/` directory beside `tree/`; `scripts/<slug>/script.json`; `CreateScript/UpdateScript/DeleteScript/list`, slug/rename/ordering reusing the request machinery (`slugify`, `uniqueSlug`, `renameMeta`, order reconcile). `Load` populates `Workspace.scripts` (converted, ordered).

**Backend — `service/workspace/workspace.go`:** the three RPC handlers (thin adapters: mutate store → reload → return `Workspace`), errors mapped via `toConnectError`. `RunScript` selects the profile from `kind`.

**Engine — `service/scripting` (the delta):** the **entry-point calling convention** (decision §2.5). Generator profile calls the module's `export default` with args and yields its return as `Result.Value`; middleware profile calls `handle`/`export default` with the `ctx` (built from `Input`) and yields the returned ctx. Mechanism: bundle with a synthetic entry that captures the export to a known global, then a profile postlude invokes it — the async job pump already settles the returned Promise. Scenario/scratchpad keep last-expression eval. New `*_test.go` covering both conventions + a JSON round-trip.

**Frontend — `ui/src/features/scripts/ScriptsView.tsx` (rebuild to the mockup):**
- **Left sidebar (280px):** filter input + "+" (new script); **Middleware** / **Generators** / **Scenarios** sections with kind-icon rows (`ph-arrows-split` / `ph-function` / …); footer chip "QuickJS·WASM · sandboxed". *Omit the Manage section (store/registries/grants) — S4/S5.*
- **Detail pane:** header = kind tag + editable name (`EditableName`) + display digest chip + **Test run** / **Save** / **Delete** (no "Run"/consent this pass); subtabs **Code** / **Dependencies** / **Capabilities**.
  - **Code:** the Monaco editor (reuse `monaco-scripts.ts` setup), imports-chip row, ⌘↵ = Test run, ⌘S = Save; footer "compiles · QuickJS·WASM".
  - **Dependencies:** the "No dependencies" empty state (S5).
  - **Capabilities:** the "Fully sandboxed" empty state (S4).
- **Test-run output:** a collapsible panel reusing the current `OutputPane` (value / console / error).
- **New-script flow:** small dialog (kind + name) → `CreateScript` → open in editor.
- **State/data:** scripts are server data (react-query, ride the `Get` snapshot); editor buffers are UI state. Extend `ui-store` (selected script, `scriptDrafts`, active script subtab) and `workspace-query` (`useCreateScript`/`useUpdateScript`/`useDeleteScript`/test-run; seed cache from returned `Workspace`).

**Verification.** `bazel build //service/cmd` + `bazel test` for touched packages green; regen TS proto. E2e against the isolated-`HOME` prod binary: create a `uuid` generator and a `sign` middleware, save, confirm `scripts/<slug>/script.json` on disk, reload → they persist, rename follows, edit + save round-trips, Test-run shows the value/logs, delete removes them.

---

## S2 — Generators in requests: `{{ }}` tokens + binding editor  *(use — values)*

> **Status: DONE & green (2026-07-23).** Two commits — backend `c689919`, frontend next. `bazel build //... //ui:ui` + touched tests green; verified end-to-end against the echo server (`//service/echo/cmd`): a `Unary` request body `{"message": {{ mkmsg() }}, "count": 1}` with generator `mkmsg = () => "resolved-42"` invoked `0 OK` and the echo returned `{"message":"echo: resolved-42"}` — the token resolved server-side and the target received the generated value (also confirmed directly over the Connect API). **Backend** (`service/workspace/tokens.go`): `{{ name(args?) }}` resolved pre-send in both unary + streaming via `RunGeneratorUncached` (values vary per invoke); body tokens splice raw JSON in value position, whole-value metadata tokens coerce to string; string-aware `}}` scan; unknown/throwing generator → `FailedPrecondition` naming the token. **Frontend**: Monaco decorations chip `{{ … }}` in the body + a "N tokens resolve" footer, whole-token metadata values render as clickable chips, and clicking a token opens the **binding-editor modal** (edit the generator's source, resolved-preview Test run, a display-only caching selector) — a missing generator offers a create flow. **Known limitation:** Monaco's JSON validator flags `{{ … }}` as invalid JSON (the footer shows "N errors") even though the request resolves and invokes fine — a token-aware validator / pre-substitution is a follow-up. Deferred as planned: `invoke()`, `env`/`vars`/`secrets`, variants, and enforced caching policies.

**Goal.** Reference a generator from a request body or metadata via `{{ name(args?) }}`; on invoke, resolve each token by running its generator and splice the result; edit/preview the bound generator in the binding-editor modal.

- **Token model + resolver (backend, `service/workspace/invoke.go`):** scan `body` (JSON value positions) and metadata values for `{{ … }}`; for each, resolve the leading dotted name to a saved **generator**, run it via `RunGenerator` (cached by config digest) with the parsed args; splice the returned value as JSON into the body (metadata values coerced to string). Applies to both unary `Invoke` and `InvokeStreaming`, pre-send. Return the resolutions so the UI can show "N tokens resolved". A missing generator / thrown token ⇒ `FailedPrecondition` with the token named.
- **Scope this pass:** **pure** generators (`uuid()`, `now("-24h")`, hashing, static transforms). **Deferred:** `invoke()` reaching other requests (a capability → S4), `env`/`vars`/`secrets` inputs (environments model → S6), message **variants**, and expiry-based caching.
- **Binding editor (frontend, modal):** open on a token click (Message/Metadata/Auth tabs). Script editor for the generator, **resolved preview** (Test run), and a **caching** selector — "By inputs" (default = the engine's config-digest cache) and "Every invoke" (bypass); "Until value expiry" deferred. Save writes the generator via `UpdateScript`.
- **Token rendering (frontend):** render `{{ … }}` as the `.tok.gen` chip inside the Message editor and Metadata values (mockup §L266–297); footer "N tokens resolve".
- **Verification.** Author `uuid`/`now`; put `{"trace_id": {{ uuid() }}, "since": {{ now("-24h") }}}` in a request; invoke against the echo server (`//service/echo/cmd`); confirm the echoed body carries a real UUID + ISO timestamp; edit via the binding editor and re-run.

---

## S3 — Middleware in requests: attach + run-before-invoke  *(use — rewrite)*

**Goal.** Attach ordered middleware to a request; run the chain before the call so each can rewrite body/metadata/target.

- **Proto:** `storage.v1.Request` + `grpcview.v1.Request` gain `repeated string middleware` (ordered script names); `UpdateRequestRequest` gains a middleware-list patch (`repeated string middleware` + a set-flag, matching the existing optional-patch pattern).
- **Backend (`invoke.go`):** before send (after token resolution), run each **enabled** attached middleware via `RunMiddleware` with `Input{Request:{body,metadata,target}}`, in order; thread the returned ctx (body/metadata/target) into the next and finally into the call. Applies to unary + streaming. Surface a per-middleware failure as `FailedPrecondition` naming the script.
- **Frontend — Middleware tab (`RequestPane`, mockup §L316–328):** list attached middleware with drag-reorder + enable checkbox + detach; "Attach middleware" picker over the collection's middleware scripts; the subtab count badge = attached count.
- **Deferred:** middleware capabilities (fs/http → S4), folder-inherited middleware, live target rewrite preview.
- **Verification.** Author a middleware that sets `x-signature`/`x-tenant` metadata; attach to a request; invoke against the echo server; confirm the echoed metadata carries the injected headers, in order; disable → header gone.

---

## 6. Deferred milestones (sketch)

- **S4 — Capabilities · grants · consent.** Productionize `std/http` (opt-in, host-scoped) + `exec` (binary allowlist); digest-pinned per-script grants (local, never committed); the Capabilities subtab (requested-vs-granted, scope, worst-case); the **launch-consent** modal (foreign-origin/changed-digest trigger); the **Grants** management view; `invoke()` for generators. Unblocks the mockup's `auth.bearer`/`vault.token` examples.
- **S5 — npm dependency management (GUI package manager).** Dependencies subtab (direct + resolved tree, integrity/tamper), **Add-package** modal (registry search → pin by digest), **Package store** (content-addressed, prune, re-verify), **Registries** (scope mapping, priority, keychain tokens). Rides the engine's existing npm resolver/store.
- **S6 — Scenarios + Environments.** Scenarios view (list + editor + results tree + call chain); Environments view (`env`/`vars`/`secrets`, `{{ }}` resolution against them, `.expires_at` caching) — which also enriches S2 generators.

---

## 7. Cross-cutting decisions & risks

- **Entry-point convention is foundational** — test-run (S1), generator resolution (S2), and middleware execution (S3) all depend on the engine calling the authored export. Land it first, in S1, with tests.
- **Token resolution runs untrusted generator code on the invoke path** — bounded by the generator profile's mem/time limits and (this pass) zero capabilities; the config-digest cache keeps repeated resolves cheap.
- **Identity is name-derived** (like requests) — a script rename changes its slug/key; the sidebar selection + open buffer must remap (reuse the `renameItem` lesson from N2a).
- **Streaming parity** — S2 token resolution and S3 middleware must apply to `InvokeStreaming` as well as unary `Invoke`; factor the pre-send pipeline so both share it.
- **Verification is Bazel + browser** — every milestone: `bazel build //service/cmd` clean, then drive the real flow against the isolated-`HOME` prod binary (and the echo server for S2/S3) before it is called done.
