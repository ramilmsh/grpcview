# grpcview — `gv` scripting features plan

**Status:** **Shipped** (on `trunk` 2026-07-29). All three features landed over one
frozen `gv` global; behavior is documented in `AGENTS.md` §"The `gv` global". The
`.proto` descriptor-explorer follow-up split out of feature 2 is still unbuilt —
[`descriptor-explorer-plan.md`](../planned/descriptor-explorer-plan.md).

Three requested features, planned together because two of them share one new
scripting global:

1. **Folder metadata + inheritance** — folders carry a TS metadata script; a
   request pulls its ancestors' evaluated metadata via `gv.metadata.inherit()`.
2. **Message-shape visibility** — show the concrete TS shape of the request
   (input) and response (output) message for the selected method.
3. **`gv.invoke()`** — run any saved request from inside a script, passing kwargs
   that the target reads as `gv.request.params`.

**Companion docs:** [`descriptor-explorer-plan.md`](../planned/descriptor-explorer-plan.md)
(the follow-up `.proto`/descriptor-explorer track split out of feature 2),
[`scripting-ui-plan.md`](./scripting-ui-plan.md),
[`storage.md`](./storage.md).

**Grounding:** see `AGENTS.md` §"Request authoring model" and §"Architecture".
Requests are authored as TypeScript and evaluated in a QuickJS-WASM sandbox at
invoke time; script globals are injected as deep-frozen POJOs by
`service/scripting/marshal.go`.

---

## The unifying idea: one shared `gv` global

Features 1 and 3 both want a `gv` object, and there is exactly one correct way to
install it. Globals are injected by concatenating a **prelude** in front of the
compiled user code (`buildInputPrelude`, `marshal.go`), and every injected object
is passed through the recursive deep-freeze helper `__ff`. Because
`Object.freeze(gv)` blocks any post-hoc member addition, and a second
`globalThis.gv = …` write would clobber the first, **`gv` must be assembled and
frozen exactly once**. That single assembly site is the shared foundation (Phase
0) both features build through.

```ts
// The complete gv surface across all three features (feature 2 touches none of it):
declare const gv: {
  metadata: { inherit(): { [key: string]: string[] } };            // Feature 1 — callable, sync
  request:  { params: Readonly<Record<string, unknown>> };         // Feature 3 — frozen data
  invoke(path: string, params?: Record<string, unknown>): Promise<InvokeResult>; // Feature 3 — host callback
};
```

**Data vs. callable split.** `gv.request.params` and the inherited-metadata map
are pure data and ride the existing `writeGlobal` JSON→`__ff` frozen-POJO path.
`gv.invoke` and `gv.metadata.inherit` are **functions**, which the JSON path drops
— they are installed inside the same IIFE that assembles `gv`, hung off frozen
containers. `__ff` recurses only on `typeof === "object"`, so it deep-freezes the
containers (`gv`, `gv.request`, `gv.request.params`, `gv.metadata`) while leaving
the two functions callable (verified against `__ff` in `marshal.go`). The
inherited map returned by `inherit()` is frozen separately.

**`gv` is installed unconditionally in every run** — request body, request
metadata, folder metadata, middleware, scenario, and inline-composed generators —
so its members are always present. Members degrade gracefully rather than being
absent: `inherit()` returns `{}` where there is no inheritance context, `params`
is `{}` on a top-level user invoke, and `invoke` throws
`"invoke is not available in this context"` on the cached generator-test path.

**Cache-soundness invariant (do not regress).** The `invoke` host callback and
any non-empty `params`/inherited-metadata ride **only** the uncached
`RunRequestBody`/`RunMiddleware` path. The `RunGenerator` path is cached by
`configDigest` (`service/scripting/profiles.go`) and must always see
`params === {}`, `inherit() === {}`, and a throwing `invoke` — all deterministic,
so the cached generator prelude stays identical run-to-run.

---

## Decisions locked

| # | Decision | Choice | Note |
|---|----------|--------|------|
| D1 | Feature 2 approach | **TS types now** (reuse in-browser `protoc-gen-es`) | `.proto`/descriptor-explorer split to [`descriptor-explorer-plan.md`](../planned/descriptor-explorer-plan.md) |
| D2 | Folder inheritance semantics | **Spread-driven replace** ("explicit over implicit") | New requests **and folders** seeded with `{ ...gv.metadata.inherit() }` so inheritance is included by default |
| D3 | Root/workspace metadata layer | **Folder-only for v1** | Future: make root a real folder rather than a special `Collection` metadata field |
| D4 | Capabilities in folder metadata scripts | **Uniform** | Folder scripts see `gv.request.params` and may call `gv.invoke` — same capabilities as a request's own script |
| D5 | Recursion / fan-out caps | **Fixed:** `gv.invoke` depth **8**, folder-chain depth **16**, one shared wall-clock deadline | Depth-cap only, **no cycle/visited set** (keeps legitimate self-recursive pagination working) |
| D6 | Nested `gv.invoke` history | **Suppressed** (`recordHistory=false`) | The public `Invoke` RPC still records; a script fan-out must not spam N requests' histories |
| D7 | Next step after this doc | **Write docs, then stop** | No implementation this pass |

D2/D3 are the user's product choices; D4/D5/D6 are recommended defaults taken
absent objection; D1/D7 are the user's explicit direction.

---

## Phase 0 — shared `gv` foundation

The one piece features 1 and 3 share. Build it once, first; it eliminates the
install-site / double-freeze / duplicate-`d.ts` conflicts before they arise.

**Backend (`service/scripting/marshal.go`)**
- Add `Params map[string]any` and `InheritedMetadata map[string][]string` to the
  `Input` struct. `nil` normalizes to `{}` so `gv.request.params` and the inherit
  map are always present.
- Replace any per-feature `gv` assembly with **one** `buildGvPrelude(in Input)
  string`, called unconditionally from `buildInputPrelude` *after* the `env`
  `writeGlobal`. It emits a single statement that assembles the whole object and
  freezes once:

  ```js
  globalThis.gv = __ff((function () {
    var d = JSON.parse(<lit for {request:{params:<Input.Params or {}>}}>); // Feature 3 data leaf
    var m = __ff(JSON.parse(<lit for Input.InheritedMetadata or {}>));      // Feature 1 pre-computed map
    d.metadata = { inherit: function () { return m; } };                    // Feature 1 callable
    d.invoke   = <gvInvokeShim>;                                            // Feature 3 callable
    return d;
  })());
  ```

  Use `var` + `globalThis` assignment (never `const`/`let`) so it re-evaluates
  safely in the reused middleware warm-pool context (`pool.go`).

**Frontend (`ui/src/features/scripts/monaco-scripts.ts`)**
- Register **one** ambient `gv.d.ts` at a single constant URI
  (`file:///grpcview/gv.d.ts`), a plain script (no `import`/`export`) so it is
  ambient across the body, metadata, and script editors. It declares the **full
  merged** `gv` interface (all three members) plus `InvokeResult`. Whichever
  feature ships first creates the file complete; the other only edits the
  interface body. A second registration at the same URI throws Monaco "Duplicate
  definition" — never do that.

**Verify:** `bazel test //service/scripting/...` — with an empty `Input`, `gv` is
frozen (adding a member is a no-op / strict-mode throw), `inherit() === {}`,
`gv.request.params === {}`, and `gv.invoke` throws with no invoker on ctx.

---

## Feature 1 — Folder metadata + `gv.metadata.inherit()`

### Approach

Folders get their own TS metadata script (mirroring
`Request.draft_metadata_script`). `gv.metadata.inherit()` returns the **already
evaluated, merged** metadata of the node's ancestor folder chain — **pre-computed
data, not a re-entrant call**. Because the tree is acyclic and a node's inherited
value is fully determined by its ancestors, `resolveInvokeMetadata` performs an
**iterative Go fold** (no JS recursion, no C-ABI, no async): walk ancestor folders
root→immediate-parent, evaluating each folder's script through the existing
`RunRequestBody` path with the running accumulator injected as
`Input.InheritedMetadata`, replacing the accumulator with each result; finally
evaluate the request's own script with the final accumulator. `inherit()` is then
just `() => <frozen accumulator>`.

**Semantics (D2 — spread-driven replace):** transitivity is userland object
spread. A folder that writes `{ ...gv.metadata.inherit(), … }` carries its
ancestors forward; an **empty** folder is a transparent passthrough (accumulator
unchanged); a **non-empty** folder that **omits** the spread is a deliberate
**barrier** that whole-replaces (drops ancestor keys it does not re-emit). A
redefined key whole-replaces the inherited array (standard JS spread). This
matches the user's `{ ...inheritMetadata(), customMetadata: foo() }` example and
is "explicit over implicit": inheritance happens because you spread it.

**Default seed:** new requests **and folders** seed their metadata editor with
`export default async (): Promise<Metadata> => ({ ...gv.metadata.inherit() })`, so
everything inherits by default and a folder is transparent-by-default.

**Efficiency gate:** the fold runs `O(depth)` fresh QuickJS instantiations, so it
runs **only** when the request's own metadata script textually references
`inherit` (regex `\binherit\s*\(`). Otherwise skip the fold entirely
(`inherit() → {}`). Bound pathological trees at `MaxFolderMetadataDepth = 16`
(exceeding it → `FailedPrecondition`, not a hang). Each folder eval is
`isJSONObject`-validated and its error is wrapped to name the offending folder
path.

### Data-model changes
- `grpcview/v1/workspace.proto` — add `string draft_metadata_script = 2;` to
  `message Folder`.
- `grpcview/store/v1/storage.proto` — add `string draft_metadata_script = 3;`
  to on-disk `message Folder` (forward/backward tolerant; no `schema_version` bump).
- `service/store/fs.go` — set `DraftMetadataScript` in `readItem`'s `kindFolder`
  branch (folders are assembled inline; no `convert.go` change).
- `service/store/store.go` — add `type FolderPatch struct { DraftMetadataScript
  *string }` (nil = unchanged, empty = clear), mirroring `RequestPatch`.

### Backend changes
- `service/store/fs.go` — add `Collection.FolderMetadataChain(ctx, path)
  ([]string, error)` (ordered ancestor scripts root→leaf; tolerant of
  `ErrItemNotFound`/`ErrNotAFolder`) and `Collection.UpdateFolder(ctx, parent,
  name, FolderPatch)` (mirrors `UpdateRequest`).
- `service/workspace/invoke.go` — add a `path []string` param to
  `resolveInvokeMetadata`; add `MaxFolderMetadataDepth`, `mentionsInherit`, and a
  `foldAncestorMetadata(ctx, workspaceName, path, allGens)` helper. Refactor
  `metadataStructFromJSON` into `metadataListsFromJSON` (map form, used by the
  fold) + `structFromMetadataLists` (Struct form, used by the final request) so
  fold and request share one normalizer.
- `service/workspace/workspace.go` + `grpcview/v1/service.proto` — add an
  `UpdateFolder(UpdateFolderRequest{workspace_name, repeated path, item_name,
  optional draft_metadata_script}) → UpdateFolderResponse{Workspace}` RPC and
  handler, mirroring `UpdateRequest`.

### Frontend changes
- `metadata-wrapper.ts` — add `defaultMetadataModule()` returning the
  `{ ...gv.metadata.inherit() }` seed.
- `RequestWorkspace.tsx` — seed new/empty-metadata requests with
  `defaultMetadataModule()` (replacing the old empty/migrate fallback) so it is
  sent even before first save.
- `TreeView.tsx` / `CollectionPanel.tsx` — a folder-row gear button opening a
  `Dialog` that hosts the existing `<MetadataEditor>`, seeded from the folder's
  `draftMetadataScript` (or the default), persisted via a new `updateFolder`
  mutation (`ui/src/lib/workspace-query.ts`).
- Regenerate TS stubs after the proto change:
  `bazel run //grpcview/v1:grpcviewv1_ts_proto.copy`.

### Phases
1. **Folder metadata field + store round-trip/chain.** Proto fields, `fs.go`
   read, `FolderPatch`, `UpdateFolder`, `FolderMetadataChain`. *Verify:*
   `bazel test //service/store/...` — round-trip + `FolderMetadataChain(['a','b'])`
   returns two scripts root→leaf; missing path → `ErrItemNotFound`.
2. **`gv` seam** (Phase 0). *Verify:* covered by Phase 0.
3. **Inheritance fold in invoke.** `foldAncestorMetadata`, gated + depth-capped +
   per-folder validation; pass `msg.GetPath()` at both `resolveInvokeMetadata`
   call sites. *Verify:* `bazel test //service/workspace/...` — additive spread,
   nested transitive, barrier (non-empty no-spread drops ancestors), override
   (child redefines a key), gate (no `inherit()` → folders not evaluated), and a
   broken-folder error that names the folder.
4. **`UpdateFolder` RPC + handler.** *Verify:* `bazel test //service/workspace/...`
   — update then Get shows the script; clearing removes it.
5. **Frontend: default seed, folder editor, IntelliSense.** *Verify:* browser
   (prod binary on `:10000`, isolated `HOME` per the verify memory) — new request
   opens on `{ ...gv.metadata.inherit() }` with `gv` autocomplete; set a folder's
   metadata, save, create a child request, invoke, confirm inherited + own
   metadata; nested folders show transitive inheritance.

### Risks / contracts
- **Barrier footgun (contract, not a bug):** deleting the spread from a non-empty
  folder script silently drops ancestor metadata. The editor default seeds the
  spread to make additive the easy path.
- **Live-vs-persisted:** the request's own metadata is evaluated from the live
  editor buffer; ancestor folder scripts are read from the **store** — folder
  edits take effect only after **save**. Surface this in the folder editor.
- Renaming a folder while a stale tab holds the old path makes the chain resolve
  to `ErrItemNotFound`; invoke swallows it as "no inheritance" (matches
  `RequestMiddleware` tolerance) rather than failing.
- Folder metadata runs through `RunRequestBody` (uncached) — never route it
  through the cached `RunGenerator` path.

---

## Feature 3 — `gv.invoke()`

### Approach

`gv.invoke` is a **fetch-style host callback**, modeled end-to-end on the existing
`fetch` bridge (`__grpcview_net_fetch`, `net.go`). Because `service/scripting` is a
leaf package that cannot import `service/workspace`, the invoke work is supplied
as a **ctx-carried closure** (`scripting.WithInvoker`), mirroring how `Grant` and
`withSink` already ride the ctx. The guest shim marshals `{path, params}` to JSON,
calls the synchronous host function, and wraps the result in `Promise.resolve`
(mandatory — the pump reports `ErrUnsettled` for a promise still pending after a
microtask drain, so the Go round trip blocks under the hood).

**Re-entry** reuses the real pipeline: extract steps 1–13 of `Workspace.Invoke`
(the resolve-target → body → metadata → middleware → dial → send → decode block,
**including the streaming guard**) into `invokeUnary(ctx, spec invokeSpec)`. The
public `Invoke` RPC builds a spec (`params=nil`, `recordHistory=true`) and wraps
the result. The `gv.invoke` closure (`scriptInvoker`) resolves the path to a saved
request, builds a child spec (`params` set, `recordHistory=false`), and calls the
**same** `invokeUnary`. `invokeUnary` sets `ctx = scripting.WithInvoker(...)` at
the top and threads it through body/metadata/middleware, so a nested `gv.invoke`
from **any** of those re-enters automatically. Because the streaming guard lives
inside `invokeUnary`, both callers reject streaming with one check.

**Addressing:** `"path/to/query"` splits on `/` into **display-name** path
segments (`findByName` already matches display names; matches the frontend's
`itemKey`/`keyOf` slash paths like `"UserService/GetUser"`). Last segment = item
name. A name containing a literal `/` is unreachable — acceptable for v1.

**Recursion guard (D5):** a single ctx-carried depth counter
(`WithInvokeDepth`/`invokeDepthFromContext`), cap **8**; exceeding it rejects the
promise. **No visited/cycle set** — a depth cap alone prevents infinite recursion
and stack blow-up while still allowing a legitimate self-recursive request that
pages via a next-page token.

**Return shape** (`InvokeResult`) is a fetch-like POJO derived from
`Request.Response`, keeping the product's decoded-JSON vocabulary (no
`Duration`/`Struct`/`Any` leakage):

```ts
type InvokeResult = {
  ok: boolean;                              // status.code === 0
  status: { code: number; message: string };
  body: unknown | null;                     // decoded response JSON, or null on failure
  metadata: Record<string, string[]>;       // merged response header+trailer
  requestMetadata: Record<string, string[]>;// what was actually sent
  latencyMs: number;
};
```

A gRPC-status **failure resolves** `ok:false` (fetch-style), not a reject. Rejects
only for: unknown path / not-a-request, a **streaming** target (unary-only in v1),
a body/metadata that won't evaluate or fit, or the depth cap.

### Data-model changes
- `service/scripting/marshal.go` — `Input.Params map[string]any` (Phase 0). **No
  proto or on-disk change:** params originate from a script at runtime, never the
  wire; the return value is an ephemeral POJO, never persisted.

### Backend changes
- `third_party/quickjs/qjs_wasm.c` — mirror the three fetch sites: env import
  `host_invoke`, a `js_host_invoke` shim, and a `__grpcview_invoke` binding in
  `register_globals`. Rebuilds via the hermetic zig-cc genrule (no checked-in
  blob).
- `service/scripting/engine.go` — export `host_invoke` in `registerHostModule`;
  add the `Invoker` type + `WithInvoker`/`invokerFromContext` and
  `WithInvokeDepth`/`invokeDepthFromContext` ctx seams.
- `service/scripting/invoke.go` (new) — `hostInvoke(ctx, mod, stack)` mirroring
  `hostNetFetch`: read the envelope, pull the invoker off ctx (nil → `tagThrow`
  "invoke is not available in this context"), call it, `writeResult`
  tagValue/tagThrow. Bytes-in/bytes-out so `scripting` stays workspace-agnostic.
- `service/scripting/marshal.go` — the `gvInvokeShim` + frozen-`gv` assembly
  (Phase 0).
- `service/workspace/invoke.go` — extract `invokeUnary(ctx, invokeSpec)` (with the
  streaming guard inside); rewrite public `Invoke` to build a spec and wrap;
  thread `params` into `resolveInvokeBody`/`resolveInvokeMetadata`/
  `applyRequestMiddleware` as `scripting.Input.Params`; gate history on
  `spec.recordHistory`.
- `service/workspace/gvinvoke.go` (new) — `scriptInvoker(workspaceName)
  scripting.Invoker`: parse envelope, split path, check `depth < 8`,
  `store.ResolveRequest`, build child spec (`recordHistory=false`), thread
  `WithInvokeDepth`, call `invokeUnary`, marshal `InvokeResult`.
- `service/store/fs.go` — `ResolveRequest(ctx, parent, name) (*Request, error)`
  factoring the resolve+`findByName`+kind-check block duplicated across
  `RequestMiddleware`/`AppendHistory`/`Delete`.

### Frontend changes
- `monaco-scripts.ts` — the `gv.request.params` + `gv.invoke` + `InvokeResult`
  declarations in the single `gv.d.ts` (Phase 0). Because the wrapper modules are
  async, `await gv.invoke(...)` type-checks.
- *(Deferred to v1.1)* path autocomplete: a `RequestRef` string-literal union built
  from the tree, dispose-and-re-add into a refined `gv.d.ts`. The plain `string`
  signature is fine for v1.

### Phases
1. **Scripting bridge + `gv` globals** (leaf package). C sites, `Input.Params`,
   `gvInvokeShim`, `hostInvoke`, ctx seams. *Verify:* `bazel test
   //service/scripting/...` with a stub echo invoker — `await gv.invoke('a/b',{x:1})`
   resolves; `params` visible/frozen; no invoker → throws.
2. **Workspace re-entry, addressing, depth guard** (load-bearing). `ResolveRequest`,
   `invokeUnary` extraction, param threading, `scriptInvoker`. *Verify:* `bazel
   test //service/workspace/... //service/store/...` — request A body does
   `await gv.invoke('B',{id:7})`; assert B ran with `gv.request.params.id===7` and
   A consumed B's response (proves a fresh child wazero instance runs to completion
   inside the parent's suspended host call on one goroutine); plus depth-cap,
   not-found, streaming-reject, and no-nested-history tests.
3. **Frontend IntelliSense + end-to-end browser check.** *Verify:* browser — `gv.`
   autocompletes `request.params`/`invoke` with no squiggles; invoking A end-to-end
   returns a response that demonstrably used `gv.invoke(B)` + `gv.request.params`.

### Risks
- **Load-bearing:** nested wazero re-entrancy — the parent instance is suspended
  inside `host_invoke` while a fresh child instance runs on the same goroutine.
  The fetch path proves synchronous host callbacks; confirm the child tears down
  cleanly before the parent resumes (Phase 2 gate).
- **Multiplied I/O:** each nested `invokeUnary` re-loads the workspace and
  re-dials/re-reflects its target. Backstopped only by the depth cap + shared
  deadline.
- **Shared deadline:** a legitimately slow 3-level invoke can be cut off by the
  top-level profile timeout; may warrant a larger explicit budget later.
- **Two `request` objects:** the pre-existing `globalThis.request`
  (body/metadata/target) vs. `globalThis.gv.request.params`. Keep them separate;
  do **not** fold params into the legacy global.

---

## Feature 2 — Message-shape visibility (TS types)

### Approach (D1 — reuse in-browser `protoc-gen-es`)

Fully decoupled from `gv` — no backend, proto, store, or gv changes.
`generateWorkspaceTypes(descriptorSet)` (`proto-types.ts`) **already** runs
`protoc-gen-es` over the whole workspace FDS and returns generated `_pb.ts` for
**every** message, input and output alike (memoized by descriptorSet identity in a
`WeakMap`). Only the input alias is wired today (`Editor.tsx`). Output coordinates
already ride the wire (`Method.output{package,name,file}`, set in `convertService`
symmetric to input). So surfacing both shapes needs no new generation machinery —
just a symbol resolver, a text slicer, and a read-only modal.

Show the `<Message>Json` protojson type — exactly what the body is authored as and
the response is decoded as (WKTs as string forms, int64 as string, oneof unions,
snake_case JSON aliases).

### Frontend changes
- `proto-types.ts` — factor the full-name symbol scan out of `requestMessageAlias`
  into `resolveLocalSymbol(content, pkg, name): string | null` (**no** naive
  fallback). `requestMessageAlias` keeps its existing fallback on `null` so the
  **live body editor is byte-for-byte unchanged**. Add exported
  `messageTypeText(files, pkg, name, file): { symbol, text } | null` that returns
  `null` for WKT file coords / missing file / unresolved symbol, else
  brace-counts the **single** `export type <Sym>Json = { … };` block (referenced
  types shown as bare identifiers; whole-file fallback if slicing fails).
- `TypesModal.tsx` (new) — a read-only modal (reusing the `Backdrop`/`Dialog`
  primitive) rendering two labelled monospace `<pre>` blocks, "Request —
  `<inputName>`" and "Response — `<outputName>`". Explicit states: loading (first
  open may trigger the first `protoc-gen-es` run), empty-descriptorSet → "schema
  unavailable", **WKT note** (e.g. `google.protobuf.Empty` — common for responses),
  whole-file fallback, and unavailable.
- `MethodHeader.tsx` — an `onShowTypes` prop + a small `Code` icon button, gated on
  `request.service && request.method`.
- `RequestWorkspace.tsx` — owns modal open/close; passes `descriptorSet` +
  `activeMethod.input`/`activeMethod.output` to `TypesModal`.

### Phases
1. **`resolveLocalSymbol` refactor + `messageTypeText`.** *Verify:* ui build +
   browser — the live body editor still shows `RequestMessage` IntelliSense
   (proves the alias is unchanged); `messageTypeText` returns a single block for a
   known method and `null` for a `google.protobuf.Empty` output.
2. **`TypesModal` + `MethodHeader` launch.** *Verify:* browser — select a unary
   method → Types shows correct input+output shapes; switching methods updates
   both; an `Empty` output shows the WKT note; an unreachable source shows "schema
   unavailable".
3. *(Optional)* contextual launch from the body-footer "valid `<type>`" chip.

### Notes / open items
- **Surface preference** (non-blocking): a single symmetric modal (chosen, simplest
  — one data path for input+output) vs. an always-visible "Schema" subtab in
  `ResponsePane` for output + a body-side peek. Confirmed decoupled from `gv` —
  keep it off the shared `gv.d.ts` path.
- **Fidelity:** the TS view drops proto field **numbers** and **comments**. That is
  the entire reason for the follow-up descriptor-explorer track (D1) — see
  [`descriptor-explorer-plan.md`](../planned/descriptor-explorer-plan.md).

---

## Cross-feature interactions

- **`inherit()` inside an `invoke()` (the load-bearing case).** When
  `gv.invoke('A/B', {id:7})` runs B, the child `invokeUnary` folds **B's** folder
  chain exactly as a direct invoke would. Params do **not** change *which* folders
  are walked (fixed by B's tree position) — they only change *what* the folder/
  request scripts compute. Inheritance is a function of **tree position**; params
  are a function of the **caller**. They compose with no special-casing.
- **Params reach inherited folder scripts (D4).** `foldAncestorMetadata` passes the
  same `params` into every ancestor folder script's `Input`, so a folder can do
  `{ ...gv.metadata.inherit(), authorization: ['Bearer ' + gv.request.params.token] }`.
- **Folder scripts can call `gv.invoke` (D4).** `gv` is installed in every run and
  the invoker is on the threaded ctx, so a folder metadata script may
  `await gv.invoke(...)` (e.g. to fetch a token). Bounded by the same invoke-depth
  cap. Accept and document; add no code to prevent it.
- **Two independent caps (D5).** `gv.invoke` nesting is bounded by the ctx depth
  counter (8); folder-chain depth by `MaxFolderMetadataDepth` (16). A folder script
  that itself calls `gv.invoke` consumes the invoke budget, not the folder budget.
  Both share one wall-clock deadline.
- **Order of evaluation.** Pipeline order is body → metadata → middleware → send. A
  `gv.invoke` from a **body** cannot observe this request's own resolved metadata;
  `inherit()` only affects the metadata stage.

---

## Build sequencing

The doc numbers features by the user's request; the recommended **build** order is
by ascending risk and data-flow dependency:

1. **Phase 0 — shared `gv` foundation.** Kills the install-site/freeze/`d.ts`
   conflicts. Owned by whoever starts, with the documented extension slot for the
   other feature.
2. **Feature 2 — anytime / in parallel.** Fully decoupled, cheap, zero
   Go/proto/bazel/`gv` changes. Unblocks nothing, blocked by nothing.
3. **Feature 1 — second.** Lower-risk backend (no C-ABI/wasm rebuild): a Go fold +
   folder schema + `UpdateFolder`. Delivers immediate value and establishes the
   `resolveInvokeMetadata(path, params)` plumbing.
4. **Feature 3 — last (heaviest).** C binding + wasm rebuild, `invokeUnary`
   extraction, ctx invoker + depth guard, nested re-entrancy. Landing last means
   the `gv` seam and `gv.d.ts` already exist, and its `invokeUnary` extraction
   cleanly absorbs Feature 1's already-present `resolveInvokeMetadata(path, params)`
   fold.

---

## Deferred / follow-ups

- **Descriptor explorer** (`.proto` view with field numbers + comments) — the
  jhump/`protoprint` track split out of feature 2 →
  [`descriptor-explorer-plan.md`](../planned/descriptor-explorer-plan.md).
- **Root as a real folder** (D3) — rather than a special `Collection` metadata
  field, unify root into the folder model later so it participates in inheritance
  without a special case.
- **Streaming `gv.invoke`** — unary-only in v1; streaming targets reject.
- **`gv.invoke` path autocomplete** — a `RequestRef` union in `gv.d.ts`.
- **Configurable caps / larger invoke-tree budget** (D5) — only if a real workload
  hits the fixed limits.
