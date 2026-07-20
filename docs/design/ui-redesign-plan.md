# grpcview UI redesign — implementation plan

**Status:** Phase 0 + Phase 1 **DONE & shipping** (commit `3869cf7`; `bazel build //service/cmd` green) · 2026-07-20
**Next steps:** see [`next-steps.md`](./next-steps.md) — the sequenced, detailed plan for what follows Phase 1 (scripting engine core → close client gaps → streaming → history).
**Goal:** Rebuild `ui/` to match the *gRPC Workspace* design (Nocturne design system), starting fresh. The current UI is a partial, light-themed prototype; its **behavior and server wiring are mostly correct** and are the reference for technical details, but the **UI/UX is being replaced**.
**Constraint the user set:** *Phase 1 covers ONLY the currently-implemented backend endpoints.* Everything the design shows that needs new backend work is explicitly deferred to a later phase.

---

## 1. Source-of-truth references

| What | Where |
|---|---|
| Target design (interactive) | Claude Design project **"gRPC Request Client Design"** → `gRPC Workspace.dc.html` (the workspace mockup) and `Scripting Engine - Design Plan.dc.html` (future scripting roadmap). |
| Design system | **Nocturne** — dark, compact, Inter + a single blurple accent. Embedded in the project under `_ds/nocturne-*/styles.css` (+ `readme.md`). This file is the token + component-class source of truth. |
| Icons | **Phosphor** (`ph`, `ph-bold`, `ph-fill` weights). The mockup loads them from unpkg — **we must bundle them** (see §3). |
| Existing code (reference, being replaced) | `ui/src/**` — mine it for the *how*, not the *look*. Key files called out per concern in §7. |

The mockup HTML (both `.dc.html` files) and the Nocturne `styles.css` have been pulled locally during planning; re-fetch anytime with the design tool (`DesignSync get_file`, project `fcd41471-c260-40d0-9b27-b8517a5606e3`).

---

## 2. Currently-implemented backend surface (the Phase-1 budget)

`WorkspaceService` (`proto/grpcview/v1/service.proto`), all **unary Connect RPCs**, single workspace hardcoded to `"default"`:

| RPC | Does | Notes / limits |
|---|---|---|
| `Get` | Returns the `Workspace` snapshot: root `Item` tree, `sources[]`, and `services[]` (each `Method` carries `input.schema` = a JSON Schema `Struct`). | The schemas power Monaco. `services[]` is **flat**, not attributed per-source. |
| `AddDescriptorSource` | Adds a source. **Only the `reflection` branch (`Server{host,port,tls?}`) is implemented** — the `descriptor_set` (uploaded bytes) branch returns `Unimplemented` server-side (`workspace.go`), even though the proto and the old modal offer it. | **No remove**, no reorder, no priority, no versioning. |
| `CreateFolder` | New folder at `path`. | |
| `CreateRequest` | New request at `path` with `service`+`method`. | |
| `DeleteRequest` | Delete item at `path`/`item_name`. | Used for both requests and folders. |
| `UpdateRequest` | Patch `service`/`method`/`draft_body`/`draft_metadata`. | **No `name`** field → **rename is unsupported** (store already warns). |
| `Invoke` | Executes **one unary RPC**. Resolves target = explicit `target` or the workspace's **first reflection source**; reflects the target fresh to resolve the method; returns `status`, response JSON `bytes`, request+response metadata, `latency`, `timestamp`. | `invoke.go` returns **`Unimplemented` for client/server streaming**. gRPC-level call failures come back as `status` data (not a Connect error). |

**Data modeled but NOT wired:** `Request.history[]` exists in `workspace.proto` but nothing populates it and `Invoke` doesn't persist it — treat history as *not implemented* for Phase 1.

**Everything else the design shows is out of scope for Phase 1** (see §5).

---

## 3. Hard constraints (do not violate)

1. **Offline, single-file, embedded.** Prod build = `vite-plugin-singlefile` → one `index.html` embedded into the Go binary (`service/cmd/main.go`) and served with no network. Therefore:
   - **Bundle fonts** (Inter, JetBrains Mono) as self-hosted `@font-face` (woff2 in the bundle) — **not** `fonts.googleapis.com`.
   - **Bundle Phosphor** via `@phosphor-icons/react` (or self-hosted webfont) — **not** the unpkg `<link>`s in the mockup.
   - **Bundle Monaco + its workers.** `@monaco-editor/react` (used by *both* the request editor and the response viewer) loads Monaco from a **CDN by default**, and the current app has **no** `loader.config`/worker setup — so it likely only works online (latent bug: verify the embedded binary with the network off). The rewrite must call `loader.config({ monaco })` to use the bundled `monaco-editor`, and set `self.MonacoEnvironment.getWorker` with Vite `?worker` imports (`editor.worker`, `json.worker`) so the workers are bundled, not fetched.
   - No runtime CDN/`fetch` to third parties.
2. **Unary only.** Do not build streaming UI (message cards loop, "streaming"/"stop", msg-count > 1, method-kind tags S←/CS/B⇄) until the backend gains streaming (Phase 3).
3. **Single workspace `"default"`.** No collection switcher wired; the top-bar collection control is display-only in Phase 1.
4. **No rename / no remove-source / no reorder.** Don't offer controls the backend can't honor; where the design shows them, omit or disable with a tooltip and track under §11.
5. **Dev vs prod origin.** Dev: Vite on its own port, backend on `:10000` (`client.ts` hardcodes `http://127.0.0.1:10000`). Prod: same-origin. Keep the transport override working for dev; prefer same-origin/relative in prod.

---

## 4. Nocturne tokens → app theme

Vendor Nocturne `styles.css` **as-is** into the app (it is self-contained: `:root` tokens + component classes `.btn/.field/.input/.card/.tag/.nav/.table/.dialog`), with two edits:
- Replace its Google-Fonts `@import` with bundled `@font-face`.
- Add an **app layer** for the workspace-only vars the mockup defines on top of Nocturne.

Core tokens (from `_ds/nocturne-*/styles.css`):

```
--color-bg:#161826  --color-surface:#232532  --color-text:#e9e9ed
--color-accent:#9184d9  --color-accent-2:#a7a1db
--color-divider: color-mix(in srgb,#e9e9ed 16%,transparent)
neutral/accent/accent-2 ramps: --color-{role}-100..900   (perceptual OKLCH steps)
--font-heading/--font-body: "Inter"; heading weight 500
--space-1..8: 2.8 5.6 8.4 11.2 16.8 22.4 (density 0.7)  --radius-sm/md/lg: 4/8/14
--shadow-sm/md/lg (edge + ambient dark)
```

App layer (from the workspace mockup's `<style>`, add to our theme):

```
--mono:"JetBrains Mono",ui-monospace,SFMono-Regular,Menlo,monospace
--panel:#1b1d2b  --panel-2:#1f2130  --line:var(--color-divider)
--ok:#5bbf97   --ok-bg:rgba(91,191,151,.14)
--err:#d1737d  --err-bg:rgba(209,115,125,.14)
--warn:#d9b46a --warn-bg:rgba(217,180,106,.14)
```

**Tailwind:** extend `tailwind.config.js` to reference these CSS vars so utilities and Nocturne classes agree — e.g.

```js
theme: { extend: {
  colors: { bg:'var(--color-bg)', surface:'var(--color-surface)', panel:'var(--panel)',
    'panel-2':'var(--panel-2)', line:'var(--color-divider)', text:'var(--color-text)',
    accent:'var(--color-accent)', ok:'var(--ok)', err:'var(--err)', warn:'var(--warn)',
    neutral: { 100:'var(--color-neutral-100)', /* …900 */ },
    'accent-r': { 300:'var(--color-accent-300)', 800:'var(--color-accent-800)', 900:'var(--color-accent-900)' } },
  fontFamily: { sans:['var(--font-body)'], heading:['var(--font-heading)'], mono:['var(--mono)'] },
  borderRadius: { sm:'4px', DEFAULT:'8px', lg:'14px' },
  boxShadow: { sm:'var(--shadow-sm)', DEFAULT:'var(--shadow-md)', lg:'var(--shadow-lg)' },
}}
```

Prefer Nocturne's component classes (`.btn.btn-primary`, `.input`, `.card`, `.tag`, `.table`, `.dialog`) for anything they cover; use Tailwind utilities (bound to the same vars) for layout and the workspace-specific chrome the mockup builds with inline styles (rail, tree rows, subtabs, tokens, etc. — those custom classes live in the mockup's `<style>` block and should be ported into our theme CSS).

---

## 5. Design → phase map

| Design surface | Backing today? | Phase |
|---|---|---|
| Nocturne theme, fonts, icons, primitives | n/a (frontend) | **0** |
| Top bar (brand, collection name, target/conn indicator), left rail, bottom status bar | partial (single ws, sources count, last latency) | **0/1** (display-only bits) |
| Collection tree (folders/requests, filter, new folder/request, delete) | ✅ | **1** |
| Open-request tabs | frontend-only state | **1** |
| Method header: service/method selectors, Invoke | ✅ (`UpdateRequest` svc/method, `Invoke`) | **1** |
| Target/address bar (resolved host:port + TLS badge) | ✅ read-only from first reflection source | **1** |
| Request **Message** tab (Monaco + per-method JSON schema) | ✅ | **1** |
| Request **Metadata** tab | ✅ (`draft_metadata` Struct) | **1** |
| Response: status code/name, latency, timestamp, JSON body, request+response metadata | ✅ (unary) | **1** |
| Add source — **reflection** host:port+TLS | ✅ `AddDescriptorSource` (reflection branch only) | **1** (modal) |
| Add source — descriptor-set upload | ❌ returns `Unimplemented` | later (implement server-side) |
| Monaco offline (bundle editor + workers) | n/a — CDN default today | **0** (required before either editor) |
| Request **name inline-edit / rename** | ❌ no RPC | later (needs `UpdateRequest.name` or Rename RPC) |
| Duplicate request, reorder, move | ❌ | later |
| **Definition Sources view** (table w/ priority, freshness, versions, collisions, buf/proto-files, source detail) | partial (add+list only) | **2** (needs remove/reorder/version RPCs + new source types) |
| **Streaming** (S←/CS/B⇄, message cards, stop, msg count) | ❌ unary only | **3** (streaming `Invoke` + `Method` streaming kind) |
| **Request history / timeline / run history** | ❌ modeled, not populated | **4** (persist in `Invoke`, expose) |
| **Environments, variables, secrets, tokens `{{ }}`, generators, binding editor** | ❌ | **5** |
| **Auth profiles, Middleware, Options, Variants** tabs | ❌ | **6** |
| **Scripts view + package manager (store/registries/grants), launch consent, add-package** | ❌ (QuickJS·WASM engine is still a spike — see scripting plan doc) | **7** |
| **Scenarios** runner | ❌ | **8** |
| Multi-collection switcher, ⌘K search, Git integration, Settings | ❌ | cross-cutting, later |

---

## 6. Proposed frontend architecture

Start over under `ui/src/`, porting logic (not styling) from the current components. Suggested layout:

```
ui/src/
  main.tsx                      # mount
  App.tsx                       # providers (TransportProvider + QueryClient) + AppShell + view routing
  theme/
    nocturne.css                # vendored Nocturne styles.css (fonts bundled)
    app-tokens.css              # --panel/--ok/--err/--warn/--mono + ported mockup classes
    fonts/                      # Inter + JetBrains Mono woff2
    monaco-nocturne.ts          # defineTheme() matching the palette
  lib/
    client.ts                   # connect transport (ported); also feeds TransportProvider
    ui-store.ts                 # zustand — UI state ONLY (activeView, openTabs, activeTabKey, editor drafts)
  components/ui/                # primitives over Nocturne classes: Button, IconButton, Input,
                                #   Tag, Card, Dialog, Tabs, SegMenu, Tooltip, Kbd
  components/shell/             # TopBar, Rail, StatusBar, AppShell
  features/workspace/
    CollectionPanel.tsx  TreeView.tsx  RequestTabs.tsx
    MethodHeader.tsx  TargetBar.tsx
    RequestPane.tsx  (MessageTab.tsx = Monaco, MetadataTab.tsx)
    ResponsePane.tsx (ResponseStatusBar.tsx, MessagesTab.tsx, MetadataTab.tsx)
  features/sources/
    SourcesView.tsx  AddSourceModal.tsx
```

**Data layer — connect-query (verified against the live backend, 2026-07-20).** Use **`@connectrpc/connect-query` v2 + `@tanstack/react-query`** for all server reads/writes — this replaces the current hand-rolled zustand server actions (which used the plain Connect client and a "call + `loadWorkspace()`" reload). The generated method descriptors are in `@grpcview/v1/service-WorkspaceService_connectquery` (`get`, `createFolder`, `createRequest`, `deleteRequest`, `updateRequest`, `addDescriptorSource`, `invoke`).

- Wrap the app: `<TransportProvider transport={transport}>` (transport from `lib/client.ts`) **outside** `<QueryClientProvider>`.
- Reads: `useQuery(get, { workspaceName: "default" })` → `data.workspace`.
- Writes: `useMutation(createFolder|…|invoke)`. Every mutation returns the fresh `Workspace`, so on success either seed the `get` cache with `queryClient.setQueryData(createConnectQueryKey({schema:get,transport,input:{workspaceName},cardinality:'finite'}), …)` **or** simply invalidate/refetch the `get` query. A smoke test confirmed `useQuery(get)` + `useMutation(createFolder)` with `onSuccess: refetch` round-trips correctly (`CreateFolder → 200`, `Get → 200`, tree updates).

**zustand holds UI state ONLY** (`activeView` = `workspace`|`sources`, `openTabs` client-side, `activeTabKey`, in-progress editor drafts) — never server data. `Invoke` results live in the mutation state (keyed per request/tab), not a manual `responses` map.

Routing: the current `react-router` setup is thin (`/` → `/workspace`). The rail can drive an in-memory `activeView` instead of routes; keep router only if deep-linking is wanted. Recommend dropping router in Phase 1 for simplicity (revisit when multi-collection lands).

---

## 7. Technical details to preserve (reuse from existing code)

| Concern | Reference file | What to carry over |
|---|---|---|
| **Monaco + per-method JSON schema** | `components/Editor.tsx` | Register `method.input.schema` for each `service.method` under URI `grpcview://schemas/<pkg>.<svc>/<method>` via `monaco.languages.json.jsonDefaults.setDiagnosticsOptions({validate, schemaValidation:"error", schemas})`; switch the editor **model** per selected method so the right schema validates. Keep `formatOnType/Paste`, `minimap:false`, ⌘S = format. **Change:** register a Nocturne Monaco theme instead of `vs-dark`. |
| **Transport** | `lib/client.ts` | `createConnectTransport({ baseUrl, useBinaryFormat:true })`. Keep dev baseURL `http://127.0.0.1:10000`; make it same-origin in prod. |
| **Server calls** | `lib/store.ts` (as reference) | **Do NOT port the fetching mechanism** — that becomes connect-query hooks (§6). Do port the pure logic: `convertToItemWithPath` (flatten `Item` tree → `ItemWithPath`) and `itemKey` (stable per-request identity for tab/response keying), and keep the "editor local state is source of truth while editing" pattern (debounce `updateRequest` on body/metadata edits — the old code fired it every change). |
| **Metadata ⇄ Struct** | `components/MetadataEditor.tsx` | `objectToRows`/`rowsToObject`/`metadataValueToString`; `-bin`+base64 is a backend concern (`invoke.go`) — the UI passes strings and shows a hint. **Known asymmetry to preserve/fix deliberately:** a multi-valued (list) header is comma-joined for display and saved back as one string, so lists don't round-trip. (The current editor has **no** per-row enable checkbox — the design's checkbox is a new feature.) |
| **Response rendering** | `components/ResponsePanel.tsx` | gRPC `CODE_NAMES` map, `latencyLabel`, `timestampLabel`, `prettyBody` (decode bytes → pretty JSON), memoized pretty-print, request/response metadata listing. Note the backend **merges header+trailer** into one response-metadata set (`invoke.go`), so a separate "Trailers" tab isn't possible until the backend splits them. |
| **Monaco loading (offline)** | `Editor.tsx`, `JsonViewer.tsx`, `main.tsx` | Both editors use `@monaco-editor/react` with **no** loader/worker config → CDN. Bundle Monaco + workers in Phase 0 (see §3) *before* building either editor. |
| **Read-only JSON view** | `components/JsonViewer.tsx` | response body viewer (Monaco, `readOnly`, `wordWrap:"on"`). Shares the bundled Monaco + theme with the request editor. |
| **Method picker filter** | `components/RequestSelectorModal.tsx` | two-pane service→method picker; `useMemo` filter over service name/package/method name — reuse for the method-header selectors. |
| **Add source** | `components/AddSourceModal.tsx` | reflection (host/port) + descriptor-set file upload → `AddDescriptorSourceRequest` oneof. |
| **Service/method picker** | `components/RequestSelectorModal.tsx` | choose `Service`+`Method` from `services[]`. Reuse for the method-header selectors. |
| **Tree** | `components/TreeView.tsx` | expand/collapse, select-request, add/delete, folder nesting. Restyle to `.treerow`; drop rename until backend supports it. |

---

## 8. Phase 0 — Foundation (no behavior change)

Build the shell and design system so Phase 1 features drop in cleanly. No new server calls.

**Steps**
1. **Bundle fonts + icons.** Add Inter + JetBrains Mono woff2 under `theme/fonts/` with `@font-face`; add `@phosphor-icons/react`. Remove all CDN links. Verify they resolve in the *embedded* build (offline).
2. **Vendor Nocturne.** Copy `styles.css` → `theme/nocturne.css` (swap font `@import` for bundled faces). Add `theme/app-tokens.css` with `--panel/--panel-2/--line/--mono/--ok/--err/--warn(+bg)` and port the mockup's custom classes (`.rail-btn`, `.treerow`, `.subtab`, `.tok`, `.mtag`, `.iconbtn`, `.chip`, `.kbd`, `input.bare`, scrollbar styling). Import both in `main.tsx`; delete Tailwind's old light styling from `index.css` (keep `@tailwind` layers + full-height root, `overflow:hidden`).
3. **Tailwind theme.** Extend config per §4 (colors/fonts/radius/shadow bound to CSS vars). `dark`-by-default (the ground is dark; no toggle needed).
4. **Monaco: make it offline-safe, then theme it.** First (see §3): `loader.config({ monaco })` so the bundled `monaco-editor` is used instead of the CDN, and `self.MonacoEnvironment.getWorker` with Vite `?worker` imports (`editor.worker`, `json.worker`). Then `theme/monaco-nocturne.ts`: `monaco.editor.defineTheme('nocturne', {...})` — base `vs-dark`, bg `#1b1d2b`, accent selections, JetBrains Mono. Both the request editor and the response viewer use it. **Gate:** confirm the editor renders in the *embedded* binary with the network disabled.
5. **UI primitives** (`components/ui/`): thin React wrappers over Nocturne classes — `Button` (primary/secondary/ghost/danger/icon), `Input`, `Tag`, `Card`, `Dialog` (over `.dialog-backdrop/.dialog`), `Tabs`/`Subtab`, `Kbd`, `Tooltip`.
6. **App shell** (`components/shell/`): `AppShell` = column of `TopBar` / (`Rail` + content) / `StatusBar`.
   - `TopBar`: brand (`ph-broadcast` accent chip + "grpcview"); collection control shows **"default"** (static, `caret-down` disabled with "single collection" tooltip); target/connection indicator (green dot + host of first reflection source, or "no source"); Search/gear rendered **disabled** (or omitted) — track under §11.
   - `Rail`: Workspace (`ph-tree-structure`) + Definition sources (`ph-stack`) active and switch `activeView`; Scripts/Scenarios/Environments/Git/History **omitted** in Phase 1 (add as the backend lands).
   - `StatusBar`: real bits only — `N sources`, `last invoke <latency>`; omit git/`QuickJS·WASM ready`/`vN` until those exist.

**Done when:** the app builds (`bazel run //ui:dev`), renders the dark shell with rail + top bar + status bar, fonts/icons load offline in the embedded build, and Monaco shows the Nocturne theme. No functionality yet beyond view switching.

---

## 9. Phase 1 — Workspace: request → invoke → response (implemented endpoints only)

Milestones are session-sized; each ends buildable and verifiable.

**1.1 Collection panel** (`features/workspace/CollectionPanel.tsx`, `TreeView.tsx`)
- Header: filter `input.bare` (client-side substring filter over request/folder names), `folder-plus` + `plus` icon buttons.
- Tree of `rootItems` (from store): folder rows (caret + `ph-fill ph-folder` + count), request rows using `.treerow`; hover reveals row actions → **Delete** (`DeleteRequest`); **New request** under a folder (opens method picker), **New folder**.
- Method-kind tag (`.mtag`): show **`U`** for all (unary) — the `Method` proto carries no streaming flag and invoke is unary-only. (Real kinds arrive in Phase 3.)
- Selecting a request sets it active + opens a tab.
- **Omit:** duplicate, rename, drag-reorder (no backend).

**1.2 Open-request tabs** (`RequestTabs.tsx`)
- Client-side `openTabs` in the UI store; tab bar per mockup (kind tag + name + close). Purely frontend; no persistence needed.

**1.3 Method header + target bar** (`MethodHeader.tsx`, `TargetBar.tsx`)
- Name: display the request name; **read-only** (rename unsupported) — or omit inline edit. Track rename under §11.
- Service selector + method selector: dropdowns populated from `services[]`; on change persist via `UpdateRequest{service, method}` and re-point Monaco's model/schema. Reuse `RequestSelectorModal` logic.
- Source/target chip: show which source resolves the schema/target (first reflection source) — read-only in Phase 1.
- **Invoke** button (`btn-primary`, `ph-fill ph-play`) → `store.invoke`.
- Target bar: render resolved `host:port` from the first reflection source + **TLS** badge (`Server.tls` set) or "insecure". Timeout/compression/options **omitted** (not in model). No token chips.

**1.4 Request pane — Message + Metadata only** (`RequestPane.tsx`)
- Subtabs: **Message**, **Metadata**. (No Auth/Middleware/Options/Variants in Phase 1.)
- `MessageTab`: the Monaco editor (port `Editor.tsx` wiring exactly, Nocturne theme). Footer: schema-validity line from Monaco markers (`valid <InputType>` / error count) + `JSON · UTF-8`.
- `MetadataTab`: port `MetadataEditor.tsx` (rows, enable checkbox, add row) → persists `draft_metadata`.

**1.5 Response pane** (`ResponsePane.tsx`)
- Status bar: `code + name` tag (ok = `--ok-bg/--ok`, else `--err`), `latency`, `timestamp`; **no** streaming indicator / msg-count (unary). Copy + download (client-side) of the body.
- Subtabs: **Messages** (the single response body via `JsonViewer`, Nocturne-styled) + **Metadata** (request metadata + response metadata; backend already merges header+trailer, so one combined "Metadata" tab — a separate "Trailers" tab waits until the backend splits them). Timeline **omitted**.
- States: loading ("Invoking…"), grpcview-side error (`responseErrors`), gRPC status error (render `status.message`), empty ("No response yet").

**1.6 Add-source modal + wiring** (`features/sources/AddSourceModal.tsx`)
- Port the **reflection** add-source flow (host:port + optional TLS) as a Nocturne dialog. Adding a reflection source is what unlocks services/schemas/target, so it's required for the core flow. **Descriptor-set upload is not implemented server-side** (`Unimplemented`) — omit it or show it disabled with a tooltip; track under §11.

**1.7 Minimal Definition Sources view** (`features/sources/SourcesView.tsx`)
- Rail "Definition sources" → a **list** (not the full mockup table) of current `sources[]`: type tag (reflection — the only working type today), target/label, and an "Add source" button. **Omit** priority/#, freshness, versions, collisions, source-detail, buf/proto-files — all need backend work (Phase 2).

**Phase 1 done when:** with a reflection source added, the user can browse the collection, pick a request, edit body (schema-validated) + metadata, Invoke a unary method, and read status/body/metadata — all in the Nocturne UI, and it works in the **embedded** binary offline.

---

## 10. Later phases (need backend work — plan each when reached)

- **Phase 2 — Definition Sources management.** Full sources table + source-detail panel. Needs: `RemoveSource`, reorder/priority + collision resolution semantics, per-source service attribution, freshness/live-reflection status, capture versioning; new source types (buf registry, proto files with import resolution).
- **Phase 3 — Streaming.** Server/client/bidi. Needs: streaming `Invoke` (Connect streaming) + `Method` streaming-kind in the schema so the tree/tabs can show S←/CS/B⇄; then the message-cards loop, "streaming"/stop, msg-count.
- **Phase 4 — History.** Populate `Request.history[]` in `Invoke` (or a `ListHistory` RPC); response "Timeline" tab, run history, re-run.
- **Phase 5 — Environments & variables.** `env`/`vars`/`secrets`, `{{ token }}` resolution, generators, the parameter-binding editor. Large model + resolver work.
- **Phase 6 — Auth / Middleware / Options / Variants** request-pane tabs. Model + backend for auth profiles, middleware chain, call options, message variants.
- **Phase 7 — Scripting + package manager.** Scripts view, store/registries/grants, launch consent, add-package. Depends on the **QuickJS·WASM scripting engine** (currently a spike — see `docs/design/quickjs-wasm-*.md` and the design's *Scripting Engine - Design Plan*).
- **Phase 8 — Scenarios** runner.
- **Cross-cutting:** multi-collection switcher (backend already keys by `workspace_name`), ⌘K search, Git integration, Settings.

---

## 11. Consolidated backend gaps surfaced by the design

Track these; each unblocks UI the mockup shows:
- `UpdateRequest.name` (or a `RenameItem` RPC) — request/folder rename.
- `AddDescriptorSource` **descriptor-set branch** (currently `Unimplemented`) — until it lands, reflection is the only working source type.
- `RemoveSource`, source reorder/priority, collision-resolution model, per-source service attribution, source versioning, new source types (buf, proto files).
- Metadata multi-value fidelity: a faithful editor needs a row model that preserves list values (the editor currently collapses lists to a comma-joined string on save; the backend already expands a list → multiple headers).
- Streaming `Invoke` + `Method` streaming-kind.
- History persistence in `Invoke` (+ expose).
- Duplicate/move item.
- Multi-workspace listing/switch (`workspace_name` plumbing exists; only `"default"` used).
- Call options (deadline/compression/max-recv) in the request model.
- The entire environments/variables/scripting/scenarios surface.

---

## 12. Per-phase verification recipe

Bazel only (this is a Bazel workspace — never `go build`/`go test`/`npm`):
- **Frontend dev:** `bazel run //ui:dev` (Vite; backend expected at `:10000`). **Must go through Bazel** — the generated proto *runtime JS* comes from the `//proto/grpcview/v1:grpcviewv1_ts_proto` dep (only the `.d.ts` are committed for the IDE), so a bare `pnpm vite` can't resolve `@grpcview/*`. (Use `…:grpcviewv1_ts_proto.copy` to materialize generated TS into the tree if needed.)
- **Backend dev (no embed):** `bazel run //service/cmd/dev`.
- **Release/embedded binary:** `bazel build //service/cmd` — then run it and confirm the UI loads offline (fonts/icons bundled).
- **End-to-end Invoke check:** the prod binary **reflects itself** on `:10000` (it serves `grpcview.v1.WorkspaceService` with reflection). Run it with an **isolated `HOME`** (fresh filesystem-backed store), add a `reflection localhost:10000` source, and invoke a `WorkspaceService` unary method (e.g. `Get`) to exercise the full request→invoke→response path in the real app.

Each milestone: build clean, then drive the actual affected flow in the browser (not just typecheck) before calling it done.

---

## 13. Appendix — connect-query data layer (verified 2026-07-20)

Before committing the plan to connect-query, a throwaway `/cq-trial` page was run in the real app (`bazel run //service/cmd/dev` on `:10000` + `bazel run //ui:dev`) against a live, isolated `cq-trial` workspace. The trial was removed after; this records the confirmed result and the exact pattern so a session doesn't have to re-derive it.

**Evidence**

| Check | Outcome |
|---|---|
| `useQuery(get, {workspaceName})` | `status: success`; rendered real server data (backend auto-created the workspace via `Get`→`EnsureCreated`). |
| `useMutation(createFolder, {onSuccess: refetch})` | write fired, then refetch; tree went **0 → 1** item. |
| Network (Chrome) | `POST /grpcview.v1.WorkspaceService/CreateFolder → 200`, then `POST /grpcview.v1.WorkspaceService/Get → 200`, via the Connect transport to `127.0.0.1:10000`. |
| Console / build | No errors; clean Vite + Bazel build; connect-query import (`@grpcview/v1/service-WorkspaceService_connectquery`) resolved through the Bazel `grpcviewv1_ts_proto` dep. |

**Confirmed minimal pattern**

```tsx
// App.tsx — providers (TransportProvider OUTSIDE QueryClientProvider)
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { TransportProvider } from "@connectrpc/connect-query";
import { transport } from "@/lib/client";
const queryClient = new QueryClient();
// <TransportProvider transport={transport}>
//   <QueryClientProvider client={queryClient}> … </QueryClientProvider>
// </TransportProvider>

// any component — generated method descriptors are the first arg
import { useQuery, useMutation } from "@connectrpc/connect-query";
import { get, createFolder } from "@grpcview/v1/service-WorkspaceService_connectquery";

const workspaceQuery = useQuery(get, { workspaceName: "default" });
const ws = workspaceQuery.data?.workspace;

const createFolderMut = useMutation(createFolder, {
  // every mutation returns the fresh Workspace → refetch (or setQueryData) keeps the tree in sync
  onSuccess: () => workspaceQuery.refetch(),
});
// createFolderMut.mutate({ workspaceName: "default", path: [], itemName: "x" });
```

Versions confirmed: `@connectrpc/connect-query@2.2.0`, `@tanstack/react-query@5.101.2`, `@connectrpc/connect@2.1.2`.
