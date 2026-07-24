# grpcview UI redesign — status & remaining roadmap

**Status:** the Nocturne rebuild has **shipped**. `ui/` was rebuilt from the
light-themed prototype into the dark, compact Nocturne UI, and the core
request → invoke → response loop is live and browser-verified. This doc now records
the design source-of-truth, the enduring invariants, and the **remaining** UI
roadmap; step-by-step build detail for shipped phases lives in the code, not here.

**Companion:** [`scripting-ui-plan.md`](./scripting-ui-plan.md) (scripts track,
S4–S6 remaining) and the QuickJS·WASM spikes.

---

## 1. Design source of truth

| What | Where |
|---|---|
| Target design (interactive) | Claude Design project **"gRPC Request Client Design"** (id `fcd41471-c260-40d0-9b27-b8517a5606e3`) → `gRPC Workspace.dc.html` (workspace mockup) and `Scripting Engine - Design Plan.dc.html`. Re-fetch with `DesignSync get_file`. |
| Design system | **Nocturne** — dark, compact, Inter + a single blurple accent `#9184d9`. Now vendored in-repo as the token/component source of truth: `ui/src/theme/nocturne.css` (+ `app-tokens.css`, `monaco-nocturne.ts`). |
| Icons | **Phosphor** via `@phosphor-icons/react` (bundled, not CDN). |

Core palette (see `ui/src/theme/` for the full token set): `--color-bg #161826`,
`--color-surface #232532`, `--color-text #e9e9ed`, `--color-accent #9184d9`;
app layer `--panel #1b1d2b`, `--ok #5bbf97`, `--err #d1737d`, `--warn #d9b46a`;
`--mono "JetBrains Mono"`; density 0.7, radii 4/8/14.

---

## 2. Enduring invariants (do not violate)

1. **Offline, single-file, embedded.** Prod build = `vite-plugin-singlefile` → one
   `index.html` embedded into the Go binary (`service/cmd/main.go`) and served with
   no network. Therefore **everything is bundled**: fonts (Inter, JetBrains Mono as
   self-hosted `@font-face`), Phosphor icons, and **Monaco + its workers**
   (`loader.config({ monaco })` + `self.MonacoEnvironment.getWorker` via Vite
   `?worker` imports). No runtime CDN/`fetch` to third parties — verify the embedded
   binary with the network off.
2. **Single workspace `"default"`.** The backend keys by `workspace_name`, but only
   `"default"` is used; the top-bar collection control is display-only until a
   multi-collection switcher lands.
3. **Dev vs prod origin.** Dev: Vite on its own port, backend on `:10000`
   (`lib/client.ts` hardcodes `http://127.0.0.1:10000`). Prod: same-origin. Keep the
   dev transport override; prefer same-origin/relative in prod.

---

## 3. What shipped

The rebuild delivered the Nocturne shell and the full unary flow, and later tracks
carried it well past the original Phase 0/1 budget:

- **Nocturne foundation** — vendored tokens, bundled fonts/icons, offline Monaco
  with the Nocturne theme, the primitives in `components/ui/`, and the
  `AppShell`/`TopBar`/`Rail`/`StatusBar` chrome.
- **Data layer** — `@connectrpc/connect-query` v2 over `@tanstack/react-query` for
  all server reads/writes; `zustand` (`lib/ui-store.ts`) holds UI state only
  (`activeView`, open tabs, drafts). **No router** — the rail drives `activeView`.
- **Workspace flow** — collection tree (create/delete/**rename** folders &
  requests), open-request tabs, method header with service/method selectors,
  **editable + persisted per-request target** (host/port/TLS), and the response
  pane (status, latency, timestamp, body, merged metadata).
- **TypeScript request authoring** — the Message and Metadata tabs are **TS
  editors**, not JSON. Bodies are typed in-browser against the reflected descriptor
  set (`@bufbuild/protoc-gen-es`); metadata is authored as evaluated JS. This
  **replaced** the old proto→JSON-Schema mechanism entirely (the `Message.schema`
  field and the Monaco `jsonDefaults` schema wiring are gone).
- **Sources** — add **and remove** reflection sources (`AddDescriptorSource` /
  `RemoveDescriptorSource`) via the Sources view.
- **Streaming** — `InvokeStreaming` RPC + handler (server/client/bidi) on the
  backend.
- **History** — `Invoke`/`InvokeStreaming` append best-effort run history
  (`AppendHistory`, capped at 50/request); re-run reconstructs body/metadata.
- **Scripts & middleware** — the Scripts view (S1) and per-request middleware
  chains (S3); see `scripting-ui-plan.md`.

---

## 4. Remaining roadmap

Each item needs design + (mostly) backend work; plan when reached.

- **Sources management (full).** Beyond add/remove: the full sources table with
  priority/reorder, per-source service attribution, freshness/versioning, collision
  resolution, and new source types (descriptor-set upload — currently
  `Unimplemented` server-side — buf registry, proto files with import resolution).
- **Streaming UI.** Surface `Method` streaming-kind in the tree/tabs (S←/CS/B⇄) and
  build the message-cards loop / stop / msg-count on top of the shipped
  `InvokeStreaming` backend.
- **History UI.** A response Timeline / run-history panel over the persisted
  `Request.history[]` (backend is done).
- **Environments & variables.** `env`/`vars`/`secrets` and their resolution — see
  `scripting-ui-plan.md` S6. (Note: the old `{{ }}` token + binding-editor design
  was **superseded** by calling generators directly from typed TS bodies.)
- **Auth / Options / Variants** request-pane tabs (middleware already shipped).
- **Capabilities, grants, consent, npm package manager** — `scripting-ui-plan.md`
  S4–S5.
- **Scenarios** runner — `scripting-ui-plan.md` S6.
- **Cross-cutting:** multi-collection switcher, ⌘K search, Git integration,
  Settings.

---

## 5. Verification recipe

Bazel only — this is a Bazel workspace; never `go build`/`go test`/`npm`.
`.envrc` already exports `GOPROXY=off` (direnv), so commands run offline as-is.

- **Frontend dev:** `bazel run //ui:dev` (Vite; backend expected at
  `:10000`). Must go through Bazel — the generated proto *runtime* JS comes from the
  `//proto/grpcview/v1:grpcviewv1_ts_proto` dep (only the `.d.ts` are committed), so
  a bare `pnpm vite` can't resolve `@grpcview/*`.
- **Backend dev (no embed):** `bazel run //service/cmd/dev`.
- **Release/embedded binary:** `bazel build //service/cmd` — run it
  and confirm the UI loads offline (fonts/icons/Monaco bundled).
- **End-to-end Invoke check:** the prod binary **reflects itself** on `:10000`. Run
  it with an **isolated `HOME`** (fresh store), add a `reflection localhost:10000`
  source, and invoke a `WorkspaceService` unary method (e.g. `Get`) to exercise the
  full request → invoke → response path.

Each change: build clean, then drive the actual affected flow in the browser (not
just typecheck) before calling it done.
