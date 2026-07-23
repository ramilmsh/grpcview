# grpcview — detailed next-steps plan

**Status:** Draft · 2026-07-20 · **N1–N4 all DONE & green (2026-07-21)** — see per-milestone status blocks below
**Companion to:** [`ui-redesign-plan.md`](./ui-redesign-plan.md) (client/UI track) and the *Scripting Engine — Design Plan* + [`quickjs-wasm-spike.md`](./quickjs-wasm-spike.md) / [`quickjs-wasm-capabilities-spike.md`](./quickjs-wasm-capabilities-spike.md) (engine track).
**Purpose:** Now that the UI redesign reached a complete, shippable **Phase 0 + Phase 1** and the scripting engine finished its **spikes (green, "GO")**, this doc sequences and details the next four milestones.

---

## 0. Two tracks, one sequence

grpcview is being built on two parallel tracks that only converge later:

| Track | Roadmap doc | State today |
|---|---|---|
| **A — gRPC client (UI)** | `ui-redesign-plan.md` (Phase 0–8) | Phase 0 + 1 **done & shipping**. Rest need backend work. |
| **B — scripting engine** | *Scripting Engine — Design Plan* (its own Phase 0–7) | Phase 0 **spikes done & green**. Phase 1 (engine core) is next. Produces **no UI** until the engine's Phase 6 (Management UI) — which *is* the UI plan's "Phase 7 Scripts view". |

The two tracks converge at **environments/variables** (UI plan Phase 5) and **generators / `{{ }}` tokens / the parameter-binding editor** in the workspace mockup — those are scripting surfaces (`vars`/`secrets`/`env` are exactly the engine's inert host objects). Nothing there should be built until the engine core + esbuild exist.

**Chosen sequence (this doc details each):**

```
N1  Scripting engine core        (Track B — engine Phase 1)                            ✅ DONE 2026-07-20
N2  Close client gaps            (Track A — rename, remove-source, descriptor upload)  ✅ DONE 2026-07-21  (844f2cd / c980ff7 / a3d5210)
N3  Streaming                    (Track A — UI plan Phase 3)                           ✅ DONE 2026-07-21  (bb01751)
N4  Request history              (Track A — UI plan Phase 4)                           ✅ DONE 2026-07-21  (6bffd7a)
```

Deferred behind these (unchanged from the source plans): full Definition-Sources management (needs a per-source-attribution data-model decision — see §6), environments/variables, engine phases 2–7, scenarios, auth/middleware/options, multi-collection, ⌘K, Git.

---

## 1. Current state snapshot (verified 2026-07-20)

**Client (Track A).** `bazel build //service/cmd` is green — the release binary that embeds the UI is up-to-date. The `ui/src` tree matches the planned feature-based architecture (`features/workspace`, `features/sources`, `components/shell`, `components/ui`, `theme/`, `lib/`). connect-query is the data layer; zustand holds UI-only state. Every deferred control is disabled with an explanatory tooltip.

**Backend surface (`WorkspaceService`).** `Get`, `AddDescriptorSource` (**reflection branch only**; descriptor-set returns `Unimplemented`), `CreateFolder`, `CreateRequest`, `DeleteRequest`, `UpdateRequest` (**no `name`**), `Invoke` (**unary only**; streaming returns `Unimplemented`). `Workspace.services[]` is a **flat merged list**, not attributed per source. `Request.history[]` is modeled but never populated.

**Engine (Track B).** `service/scripting/engine.go` is a working spike: compile-once `Runtime` → fresh/pooled `Instance`; inner (`JS_SetMemoryLimit`) + outer (wazero page ceiling) memory bounds; wall-clock interrupt via `context` deadline; `[tag u8][len u32 LE][payload]` marshalling ABI; JS throw → `*JSError`; a two-gate capability system (bundle-time injection + call-time Go enforcement) with `node:fs`/`node:path`/`node:net` shims. `bazel test //service/scripting:scripting_test` is green (16 tests). **Not imported by any RPC** — standalone.

---

## N1 — Scripting engine core  *(engine Phase 1)*

> **Status: DONE & green (2026-07-20).** `bazel test //service/scripting:scripting_test` passes all 16 spike tests + 14 new engine-core tests. The C ABI became a persistent-context state machine (`qjs_new`/`qjs_eval(async)`/`qjs_pump`/`qjs_result(as_json)`/`qjs_dispose`, plus a `host_console` sink), and the Go package split into `engine.go` / `marshal.go` / `profiles.go` / `pool.go`. All six build items landed: structured inert-input globals, structured JSON output + buffered console, the host-driven async job pump, three profiles (`Engine.RunGenerator`/`RunMiddleware`/`RunScenario`), a warm pool with interrupt-aware discard + a long-lived-context option, and error line/stack propagation into `*JSError`.
>
> **Benchmark (`BenchmarkMiddleware`, Apple arm64).** Warm pool, **fresh context per invoke** (full isolation): **~1.23 ms/op**. Warm pool, **long-lived reused context**: **~305 µs/op** (~4×; the ~800 µs QuickJS bootstrap paid once, JS globals shared).
>
> **Decisions settled.** (1) *Warm-pool policy* — **default to fresh-context** (isolation, safe for arbitrary scripts); long-lived is opt-in (`WithLongLivedMiddleware`) for latency-critical, re-entrant scripts. (2) *Long-lived `JSContext`* — the 4× win is real but re-evaluating a script in a reused context redeclares top-level `const`/`let` → a hazard; **defer as the default until esbuild (Phase 2) wraps scripts in a re-entrant entry point.** (3) *Process isolation* — **stays deferred** (Phase 7); the WASM bounds hold. **Two QuickJS gotchas worth remembering** (now in code comments): `JSPromiseStateEnum` is *unsigned* so `JS_PromiseState`'s `-1` for a non-promise must be handled by exact-state matching, not `ps < 0`; and `JS_EVAL_FLAG_ASYNC` fulfils its promise with `{value: <completion>}`, so the host unwraps one `.value` layer (a statement-terminated script yields `{}`, QuickJS's empty-completion form).

**Goal.** Turn the spike's `RunScript(src, grant, memLimit) → string` into a production execution package that marshals structured I/O, runs async code, and serves the three script kinds under hard bounds — with clean error propagation, all proven by a benchmark. No RPC/UI wiring yet.

**What the spike already gives us (do not rebuild):** wazero integration, both memory bounds, the wall-clock interrupt, the tagged-buffer ABI, JS→Go error surfacing, and the capability gates. This milestone builds *on top* of that seam.

**Builds (the delta over the spike):**

1. **Structured input marshalling — inert host objects.** Inject read-only, flat POJOs the scripts read: `request{body,metadata,target}`, `vars`, `secrets`, `env`. Serialize Go → JSON and define them as frozen globals in the bundle prelude (`globalThis.request = Object.freeze(JSON.parse("…"))`). No Go object-graph is reachable — the guest only ever sees copied bytes. (Extends `Bundle`/the prelude in `engine.go`.)
2. **Structured output.** Return a typed result (JSON value + logs), not just a `String()`-ified scalar. Extend the result envelope's `tagValue` payload to carry a JSON document; add a `console` inert-ish sink (buffered in the host, returned alongside the value).
3. **Async / job pump.** QuickJS has Promises + a job queue. Add a guest entry point (`qjs_pump_jobs` / `qjs_eval_async` in `third_party/quickjs/qjs_wasm.c`) and a Go loop that calls `JS_ExecutePendingJob` until the top-level Promise settles **or** the wall-clock deadline fires. This is the foundation the (currently stubbed) `net` capability's Promise+ticket pattern will use.
4. **Three execution profiles** — thin wrappers over the core, differing only in bounds + reuse policy:
   - **generators** — async, result **cached by config digest** (code + resolved deps + capability imports);
   - **middleware** — per-invoke, **warm-pooled**, latency-sensitive;
   - **scenarios** — long-running, many awaits, larger time budget.
5. **Warm-instance pool with interrupt-aware discard.** An interrupted instance must be dropped and replaced (spike risk note). Also: the ~800 µs/eval cost is the QuickJS *context* bootstrap, not `Instantiate` — so for middleware, evaluate a **long-lived-`JSContext`** shim entry point (perf vs shared-JS-state trade — see decision below).
6. **Error line info.** Propagate QuickJS error line/stack into `*JSError` (raw guest lines now; esbuild sourcemaps that map to the author's original line come in engine Phase 2).

**Files.** `service/scripting/engine.go` (split into `engine.go` / `marshal.go` / `profiles.go` / `pool.go` as it grows); `third_party/quickjs/qjs_wasm.c` (+ its `BUILD.bazel` / patch if new exports need a bump — follow the version-bump note in `quickjs-wasm-spike.md`); new `service/scripting/*_test.go` + a `Benchmark*`.

**Verification.** `bazel test //service/scripting:scripting_test`; add a benchmark and record warm-pool + long-lived-context latency (this milestone's key open number). **Exit (from the design):** all three profiles run bounded, with clean error propagation, under a benchmark.

**Decisions to settle here:**
- **Warm-pool policy** (reuse vs isolation) — settle from the measured latency.
- **Long-lived `JSContext` for middleware** — the ~800 µs bootstrap disappears but JS state is shared across invokes; decide whether middleware tolerates that.
- **Process isolation** (engine Phase 7) stays deferred *unless* these numbers say WASM bounds are insufficient.

**Dependencies:** engine Phase 0 (done). **Does not block** any N2–N4 client work — it can run fully in parallel with them.

---

## N2 — Close client gaps  *(rename · remove-source · descriptor upload)*

> **Status: DONE & green (2026-07-21).** All three slices shipped and e2e-verified against the self-reflecting prod binary (isolated `HOME`); `bazel build //service/cmd` + touched-package tests green.
> - **N2a rename** (`844f2cd`) — `optional string name` on `UpdateRequestRequest`; the FS store rewrites `meta.name` on the **stable slug** (no on-disk move, contra the "Watch" note below) guarding `ErrAlreadyExists`; UI `EditableName` inline-edit + `ui-store.renameItem` remaps `openTabs`/`drafts`/`invokes`/`activeKey` so the open tab/draft/response survive. **Folder rename deferred** (needs a distinct RPC + a subtree rekey).
> - **N2b remove-source** (`c980ff7`) — `RemoveDescriptorSource(workspace_name, index)`; drops the source, re-resolves the merged `services[]` from the remaining sources, persists via `PutDescriptorState`. Extracted `convertService` / `resolveReflectionServices` / `resolveServicesFromSources` / `mergeService`. Out-of-range → `InvalidArgument`, re-reflect failure → `Unavailable`.
> - **N2c descriptor-set upload** (`a3d5210`) — implemented the `DescriptorSet` branch via `resolveDescriptorSetServices` (jhump `CreateFileDescriptorsFromSet` → shared `convertService` + `mergeService`), wired into **both** `AddDescriptorSource` and `resolveServicesFromSources` (the latter previously skipped descriptor-set sources, so remove/re-resolve would have dropped them); `AddSourceModal` upload enabled. The spec's "extract the schema-conversion helper" was already done by N2b.

Three independent, small proto+backend+UI slices. Each closes a gap a user hits immediately in the shipped Phase-1 UI and is currently a disabled/tooltip'd control.

### N2a — Rename request (and folder)
- **Proto** (`proto/grpcview/v1/service.proto`): add `optional string name = 8;` to `UpdateRequestRequest`. (A dedicated `RenameItem` RPC is the alternative; extending `UpdateRequest` is smaller and covers the common case.)
- **Backend**: `service/workspace/workspace.go` `UpdateRequest` → map `Msg.Name` into `store.RequestPatch{Name}`; `service/store/*` apply the rename. **Watch:** the FS-backed store keys items by name/path, so a rename moves the on-disk entry — reuse the store's move/rename path, guard `ErrAlreadyExists`.
- **Frontend**: `MethodHeader.tsx` (inline-edit the name; drop the "rename unsupported" tooltip), `TreeView.tsx` (rename affordance on the row). **Identity caveat:** `itemKey` (`lib/format.ts`) is name-derived, so a rename changes a request's key — `ui-store.ts` must remap `openTabs`/`drafts`/`invokes` from the old key to the new one on a successful rename, or the open tab/draft/response detach.
- **Scope:** request rename first (most-felt). Folder rename is a subtree move — include only if the store's rename already handles folders cleanly; otherwise track as a small follow-up.

### N2b — Remove source
- **Proto**: new `RemoveDescriptorSourceRequest{ string workspace_name; int32 index; }` + `RemoveDescriptorSourceResponse{ Workspace workspace; }` + the RPC on `WorkspaceService`. (Sources have no id; the array index matches the displayed order — simplest stable handle until Phase-2 gives sources real identity.)
- **Backend**: `workspace.go` new handler — drop `ws.Sources[index]`, then **re-resolve `services[]`** from the remaining reflection sources (the flat list can't be un-merged otherwise), and persist via the existing `coll.PutDescriptorState(sources, services)`. **Watch:** re-reflection is a network round-trip per remaining reflection source; do it server-side and surface failures. (This is a stopgap; per-source attribution in Phase 2 removes the re-reflect.)
- **Frontend**: `features/sources/SourcesView.tsx` (per-row remove + confirm dialog), `lib/workspace-query.ts` (add `removeDescriptorSource` mutation, seed cache like the others).

### N2c — Descriptor-set upload
- **Proto**: none — the `descriptor_set` bytes branch already exists in `AddDescriptorSourceRequest`.
- **Backend**: `workspace.go` implement the `AddDescriptorSourceRequest_DescriptorSet` case (currently returns `Unimplemented`): parse the uploaded `FileDescriptorSet` (protobuf), walk services/methods, convert input schemas via the **existing** `inspector.ConvertMessage` → `structpb` path, merge into `ws.Services` with the same replace-by-identity logic. **Refactor:** extract the schema-conversion block shared with the reflection branch into a helper.
- **Frontend**: `features/sources/AddSourceModal.tsx` — enable the file-upload UI (already present but disabled), read the file to bytes, send `source:{case:"descriptorSet", value: bytes}`. `workspace-query.ts` `addDescriptorSource` already handles it.

**Verification (all three).** Drive against the self-reflecting prod binary with an isolated `HOME` (per `ui-redesign-plan.md` §12): add a `reflection localhost:10000` source; rename a request and confirm the tab/draft follow; remove the source and confirm services clear; re-add via a descriptor set exported from the same server and confirm services load.

---

## N3 — Streaming  *(UI plan Phase 3)*

> **Status: DONE & green (2026-07-21, `bb01751`).** All four call shapes end-to-end; `bazel build //...` + `bazel test //...` green; e2e drove unary/server/client/bidi against a new echo server.
> - **Transport decision — server-streaming, NOT the bidi shape suggested below.** `@connectrpc/connect-web` can't stream a request body from a browser, so `InvokeStreaming` is `rpc InvokeStreaming(InvokeStreamRequest) returns (stream InvokeStreamResponse)`: client messages are supplied up-front and the backend maps them onto the target's real kind over full gRPC. **Limitation:** client/bidi targets have no live interleave (compose-then-send).
> - `Method` gained `client_streaming`/`server_streaming` (+ `output`), populated once in `convertService` (serves reflection + descriptor-set). `resolveMethod` factored out of unary `Invoke`. Frontend: kind tags (U/S←/C→/B⇄), `MessagesTab`, streamed message cards + live count + Stop.
> - **Streaming test target built:** `//service/echo/cmd` (proto `echo/v1`, all four kinds + reflection) — resolves the prerequisite flagged below (the self-reflecting binary is unary-only).
> - **Bug caught by e2e, not unit tests:** `service/logging.go` `WrapStreamingHandler` returned `nil` (unary-only stub); since this is the repo's first streaming RPC, connect wrapped the handler into nil → panic on every streaming call. Fixed + `service/logging_test.go` regression.

**Goal.** Server-, client-, and bidi-streaming invokes, with the tree/tabs showing the real method kind.

- **Proto** (`workspace.proto`): add streaming kind to `Method` — `bool client_streaming = 4; bool server_streaming = 5;` (populate from reflection's `IsClientStreaming`/`IsServerStreaming` in **both** `AddDescriptorSource` branches). Add a streaming invoke RPC to `service.proto` — recommend a **bidi** `rpc InvokeStreaming(stream InvokeStreamRequest) returns (stream InvokeStreamResponse)` that carries: an opening message (service/method/target/metadata), then client messages, then a close; responses carry per-message payloads + a final status/trailers. Keep unary `Invoke` as-is.
- **Backend**: `service/workspace/invoke.go` — replace the streaming `Unimplemented` with a Connect streaming handler that maps the reflected method's kind onto the four call shapes. Larger lift; factor the target-resolution + reflection code shared with unary `Invoke`.
- **Frontend** (`features/workspace/`): method-kind tags (`U`/`S←`/`CS`/`B⇄`) replacing the hardcoded `MethodKindTag kind="u"` in `MethodHeader`/`TreeView`/`RequestTabs`; a message-cards loop, send-more/stop controls, and a message count in `ResponsePane` (mockup reference: the "streamed messages" block, workspace mockup ~L330–386). New `MessagesTab` variant for multi-message.
- **Verification.** The self-reflecting binary is **unary-only**, so a **streaming test target is required** — stand up a small streaming echo service (or reflect a known streaming server) before this can be driven end-to-end. Flag this as a prerequisite task.

---

## N4 — Request history  *(UI plan Phase 4)*

> **Status: DONE & green (2026-07-21, `6bffd7a`).** History persisted, browsable, re-runnable; `bazel build //...` + `test //...` green; e2e — 3 invokes survived a process restart + page reload, re-run repopulated + fired.
> - **No proto change** (`History` already carries status/latency/timestamp; `Get` already returns `Request.history[]`). Store `AppendHistory` caps at 50 (drops oldest, logs) and writes the **gitignored `.grpcview/history/<slug>/history.json` sidecar** (never `request.json`; survives rename via slug key). `recordHistory` hooks both unary returns + the streaming terminal frame; ad-hoc invokes record nothing.
> - UI **Timeline** subtab in `ResponsePane` (status chip + latency + timestamp, newest-first), reachable post-reload; select repopulates draft+metadata, re-run fires. **`ClearHistory` skipped** (optional). **Limitation:** streaming history records terminal status/metadata + the first request message only, not streamed payloads.

**Goal.** Persist each invoke and let the user browse/re-run past calls. The data model already exists.

- **Proto**: `History` + `Request.history[]` already defined in `workspace.proto`. `Get` already returns them inside each `Request`. Optionally add `rpc ClearHistory(...)`; a re-run needs no new RPC (re-use `Invoke` with the historical request).
- **Backend**: `service/workspace/invoke.go` — after an invoke completes (success **or** gRPC-status failure), append a `History{request, response}` to the target request and persist via the store; **cap** the list (e.g. last N) and `log()` the cap. The `Request.Response` shape `Invoke` already returns maps almost 1:1 onto `History.Response`.
- **Frontend** (`features/workspace/ResponsePane.tsx` + store): add a **Timeline/History** subtab listing past runs (status chip + latency + timestamp); selecting one re-populates the editor draft + metadata and can re-invoke. Expose history via `workspace-query.ts` (it rides along on the `Get` snapshot already).
- **Verification.** Invoke a unary method several times against the isolated-`HOME` prod binary; confirm history persists across a reload (filesystem store) and that re-run repopulates and fires.

---

## 6. Cross-track decisions & deferred work

- **Per-source service attribution (blocks full Sources management, UI plan Phase 2).** The design's Sources view (priority/reorder, collisions, freshness, versions, buf/proto/descriptor types, live-streaming reflection) all assume the backend knows *which source contributed which service*. Today `services[]` is flat and merged. This is a **foundational data-model decision** — resolve it before Phase 2. N2b's "remove ⇒ re-reflect everything" is the deliberate stopgap until then.
- **Environments/variables ↔ scripting convergence.** UI plan Phase 5 (`env`/`vars`/`secrets`, `{{ }}` resolution, generators, the binding editor) overlaps the engine's inert host objects and the generator script kind. Sequence it **after** engine core (N1) + esbuild (engine Phase 2) so generators have a runtime.
- **Streaming test target** (N3 prerequisite) — the app can't dogfood streaming against itself. **✅ Resolved by N3:** `//service/echo/cmd` (echo server, all four kinds + reflection).
- **Rename changes `itemKey` identity** (N2a) — the UI must remap client-side keyed state; keep in mind for any future name-derived keying. **✅ Handled** via `ui-store.renameItem` (remaps `openTabs`/`drafts`/`invokes`/`activeKey`).
- **Folder rename** (deferred from N2a) — needs a distinct `RenameItem`/`UpdateFolder` RPC + a subtree rekey of every descendant's name-derived key (`UpdateRequest` rejects non-requests). Small follow-up.
- **`ClearHistory` RPC + clear affordance** (skipped in N4 as optional) — small follow-up if the history UI wants a clear button.
- **Descriptor-set-only workspaces have no Invoke target** (noticed in N3) — `resolveTarget` returns only the first *reflection* source, so a workspace whose services come solely from a descriptor set lists methods but can't invoke (`FailedPrecondition`). Pre-existing gap.
- **Engine phases 2–7** (esbuild, capability productionization, npm deps, Monaco IDE, Management UI, hardening) proceed on the engine track per its own plan; the **Management UI** (engine Phase 6) is where Track B finally surfaces as the workspace mockup's Scripts/Registries/Grants views.

---

## 7. Verification recipe (unchanged — Bazel only)

Per `ui-redesign-plan.md` §12: `bazel run //ui:dev` (needs backend on `:10000`), `bazel run //service/cmd/dev` (backend, no embed), `bazel build //service/cmd` (release/embedded), `bazel test //service/scripting:scripting_test` (engine). End-to-end client checks use the self-reflecting prod binary with an isolated `HOME`. Each milestone: build clean, then **drive the actual flow** (browser for client work; benchmark for the engine) before calling it done.
