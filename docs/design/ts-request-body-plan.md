# TypeScript request bodies — feasibility + plan

**Question (user):** the request body editor is JSON today. Could it be **TypeScript/JavaScript** instead — so we inject the generator function definitions straight into the editor (autocomplete, call them directly) and drop the `{{ }}` token escaping — while **preserving the autocomplete + error detection** we get today from the proto-derived JSON Schema?

**Verdict: feasible, and most of the hard infrastructure already exists.** The scripting engine already returns arbitrary-object JSON from a script run, already has an `export default` entry-point convention, and already bundles TS + resolves virtual modules via esbuild plugins. Monaco already runs a TypeScript language service with `.d.ts` injection (the Scripts editor). The invoke path already has a shared, list-shaped pre-send body pipeline with a clean insertion point. The genuinely new code is bounded: a thin client-side step that runs `protoc-gen-es` in-browser to type the body (**proven** — §2.5/§4.4), an esbuild resolver that exposes workspace generators to a body script, `body_language` plumbing, and a Monaco editor-mode switch. No blockers found.

Two findings reshape the design: the "functions" are saved generators (§2), and pillar B's type source is now proven to be the real `protoc-gen-es` generator running client-side (§2.5).

**Progress:** T1 shipped + browser-verified (commit `ce4e74d`, 2026-07-23) — details in §5. Next up: T2 (typed body).

---

## 0. Decisions locked (2026-07-23) + design deltas

The four §6 questions are answered. Deltas below override the earlier sections where they differ.

1. **Composition: full — call saved generators, and type them.** Build real generator-calling (not a builtins-only MVP), and the injected declarations should be **typed TS** (parse each generator's `export default` signature → typed params/return), not `any`. *Mechanism shift (see #4):* to suit the hidden-wrapper UX, generators are exposed as **ambient globals**, not `import { … } from "grpcview:generators"`. Assembled via a synthetic esbuild prelude — for each generator `import __g from "grpcview:gen/<name>"; globalThis.<name> = __g;` — bundled ahead of the body so a bare-object body can call `mkmsg()` with no import line. This rides the engine's existing **last-expression eval** path (prelude + `(<body expr>)`; the trailing expression is the value), not the `export default` convention.
2. **Type source: reuse the Connect / protobuf-es generator, don't hand-roll a walker. — PROVEN in-browser 2026-07-23 (§2.5).** The repo already generates TS proto types with `protoc-gen-es`. Reuse **that** generator rather than inventing WKT/int64/enum/oneof rules in `inspector.go`. The concrete path is now proven end-to-end in a real browser: ship the reflected `FileDescriptorSet` to the client, run `@bufbuild/protoc-gen-es` **in-browser** with `json_types=true`, then `ts.transpileDeclaration` → pure `.d.ts`. This **supersedes §4.4's earlier "backend Go walker" idea** — pillar B is client-side and reuses the real generator, so there is zero mapping logic to maintain.
3. **Rollout: TS is the default; migrate existing.** JSON stays only as an escape-hatch mode. Migration: an existing object body renders **as-is** under the hidden wrapper (a JSON object literal is already a valid TS expression — visually seamless); existing **body** `{{ name(args) }}` tokens rewrite mechanically to `name(args)` calls; **metadata** `{{ }}` stays this pass. *Sequencing:* the global flip to TS-default + token→call migration happens **with T3** (composition), so the rewritten calls actually resolve; before T3, requests that use `{{ }}` remain JSON. New requests can default to TS from T1 (they have no tokens yet).
4. **Authoring: hidden wrapper (bare-object look), with an "expand to module" escape hatch.** Default view is a bare object `{ … }` that looks like JSON to the eye until you call a function; the `RequestMessage` typing + generator globals are injected invisibly and it's evaluated as a last-expression. Power users can expand to a full `export default (): RequestMessage => ({ … })` module (statements, explicit imports) — which uses the entry-point convention. *Downside audit (user asked "if there are no downsides"):* the only real costs are (a) Monaco two-model **position-mapping** for the hidden wrapper (implementation complexity, spike it early), and (b) a bare expression can't hold setup statements — fully mitigated by the expand escape hatch. No capability is lost. If the position-mapping proves janky, fall back to the explicit `export default` form as the default.

**Execution: ultracode.** Implement via multi-agent workflows, staying in the loop between phases. The **understand/de-risk** phase had two cruxes: (a) protobuf-es-generator-at-runtime reuse — **RETIRED, proven by the §2.5 spike**; (b) the hidden-wrapper Monaco position-mapping spike — **still open**, do it before T4. Then per-phase implementation workflows. *Lesson carried from the S1 attempt:* do **not** force a StructuredOutput schema on a large implementation agent (it hit the retry cap and aborted though the code was fine) — heavy implementation agents return free-text reports; structured output is reserved for small research/review findings.

---

## 1. How it works today (grounded)

**Body editor — JSON + proto-derived JSON Schema.**
- `ui/src/features/workspace/Editor.tsx` — a Monaco model fixed at `language="json"`, URI `grpcview://request/body.json`. Autocomplete + error detection come entirely from Monaco's JSON language service, fed a schema via `monaco.languages.json.jsonDefaults.setDiagnosticsOptions({ schemas:[{ fileMatch:[MODEL_URI], schema }]})` (`Editor.tsx:58-74`). The "N errors" footer reads Monaco **markers** (`onDidChangeMarkers` → `getModelMarkers`, `Editor.tsx:88-100`).
- `ui/src/features/workspace/MessageTab.tsx` — wraps the editor; the "N tokens resolve" line (`:28`) and the hard-coded `JSON · UTF-8` label (`:68`).
- The schema is **generated on the Go backend**, not the client: `inspector/inspector.go` walks the reflected protobuf descriptor → JSON Schema (`ConvertMessage`, `convertFieldType:43-100`, `additionalProperties:false` at `:102-121`, keys use `JSONName()`). Shipped to the client as a `google.protobuf.Struct` on `Method.Input.schema` (`service/workspace/workspace.go:347-364`). **The client has no field/type model of the message — only that opaque JSON-Schema object + the type name.**

**Body → sent message, on invoke.**
- `service/workspace/invoke.go`. Unary (`:46-126`): normalize body → `resolveInvokeTokens` (`:66`) → `applyRequestMiddleware` (`:74`) → `reqMsg.UnmarshalJSON([]byte(body))` (protojson, `:80-83`) → send. Streaming (`:159-361`) is identical but the unmarshal is a per-message loop (`:188-199`).
- Token/middleware steps are **shared** and operate on a `[]string` of bodies. `resolveInvokeTokens` (`tokens.go:132`) scans `{{ name(args?) }}`, resolves each name to a saved generator, runs it via `Engine.RunGeneratorUncached` (`tokens.go:285-295`), and **splices the raw-JSON return value** into the body at the token's byte offsets (`tokens.go:183-204`).

**Scripting engine (the reusable machinery).**
- `Engine.RunGeneratorUncached(ctx, source, Grant{}, Input{Args})` returns `Result{ Value json.RawMessage }` — **arbitrary-object JSON**, exactly what a whole request body needs (`profiles.go:195`, `marshal.go:35`). 5s timeout, fully sandboxed (empty `Grant`).
- Entry-point convention (`entry.go`): a source that declares `export default` is bundled as an IIFE capturing exports to `__grpcview_entry`, then a postlude calls `.default(...args)` (`generatorPostlude:41-50`). The bundler (`bundler.go`) transpiles TS, resolves the module graph, and already hosts **virtual-module resolver plugins** — the embedded-npm resolver (`registryResolverPlugin`) and the capability shims are exactly the pattern we'd mirror to expose workspace generators.

**Monaco TypeScript IntelliSense (already wired, for the Scripts editor).**
- `ui/src/theme/monaco-nocturne.ts` bundles the TS worker offline. `ui/src/features/scripts/monaco-scripts.ts` sets `typescriptDefaults` compiler options (`:35-63`), turns diagnostics on, and injects types with `addExtraLib` (dayjs + an ambient env `.d.ts`, `:83-122`). Model URIs are `file://` so Node-style resolution works. This is the whole surface we need for typing the body.

---

## 2. The finding that shapes everything: **the "functions" are saved generators**

There are **no `uuid()` / `now()` builtins**. The engine injects only `console`, `request`, `vars`, `secrets`, `env` (`marshal.go:94-107`); the only vendored npm package is `dayjs` (`npm.go` — registry is a closed allowlist). So `{{ uuid() }}`, `{{ now() }}`, `{{ mkmsg() }}` all resolve to **user-authored saved generators**, looked up by name on the Go side.

**Consequence:** "inject the function definitions into the editor so you can call them directly" *is* "let a body script call the workspace's saved generators." To reach parity with `{{ }}`, a TS body **must** be able to call saved generators. So **script composition is a required pillar of this feature, not an optional extra** — and it's the single biggest piece of new engine work. (Today generators never call each other; each `{{ }}` token runs one generator in isolation.)

Token names are already constrained to identifier-safe `[A-Za-z_][A-Za-z0-9_]*` (`tokens.go` grammar), so every generator usable as a token already has a name that is a valid JS identifier — composition can reuse those names verbatim.

---

## 2.5. Spike: pillar B proven in-browser (2026-07-23)

The one crux for pillar B — "can we reuse the real `protoc-gen-es` generator client-side to get correct proto types, instead of hand-rolling a mapping?" — was de-risked with a **working browser experiment** (artifacts in `scratchpad/spike/`: `spike.mjs`, `build.mjs`, `index.html`, `descriptor.binpb`). Result: **yes, end to end, in a real Chrome tab** (`isBrowser: true`, `hasNodeProcess: false`).

**Grounding on the source first.** In `@bufbuild/protoplugin`, only `run-node.js` touches Node (`process`/stdin/stdout) — that's the `bin/` CLI adapter, which we don't import. The generation path (`create-es-plugin.js` → `schema.js` `createSchema` → `createFileRegistry` → `generated-file.js` string buffers) is pure JS; the plugin's public seam is `createEcmaScriptPlugin(...).run(CodeGeneratorRequest): CodeGeneratorResponse`, in-memory, no disk. (User's read of the source was correct.)

**What the spike ran & proved**, against grpcview's own 130KB `FileDescriptorSet` (built with `buf`; WKT-heavy — `struct`/`timestamp`/`any`):
- **STEP 1 — `protocGenEs.run(req)` executed client-side** → 4 `_pb.ts`, **every one with `json_types` present**. `StatusJson` came out as `{ code?: number; message?: string; details?: AnyJson[] }` — the correct protojson shape, WKT mapped (`repeated Any`→`AnyJson[]`, `int32`→`number?`). This is precisely what the lossy JSON Schema can't express.
- **STEP 2 — `ts.transpileDeclaration` (TS 5.9.3, in-browser)** → pure `.d.ts` for all 4 files: **`diags=0`, no runtime JS** (no `messageDesc()`/`fileDesc()`/base64/arrows). `export declare const StatusSchema: GenMessage<Status, { jsonType: StatusJson }>` with the base64 `fileDesc` blob gone.

**Proven recipe (client-side):**
1. Ship the reflected `FileDescriptorSet` to the client (already available from reflection / descriptor-set upload).
2. Build a `CodeGeneratorRequest` in memory:
   ```ts
   { fileToGenerate: [<our proto/… files>], protoFile: fds.file, sourceFileDescriptors: [], parameter: "target=ts,json_types=true" }
   ```
   `sourceFileDescriptors: []` is **required** — `run()` does `request.sourceFileDescriptors.find(...)`.
3. `protocGenEs.run(req)` → `_pb.ts` with `export type FooJson = {…}`.
4. `ts.transpileDeclaration(content, { compilerOptions: { declaration: true } })` per file → pure `.d.ts`. This is the isolated-declarations transpiler: **no lib, no host, no fs** — so the fs-bound `@typescript/vfs`/`createDefaultMapFromNodeModules` path is never needed (an earlier worry, now dead). 0 diagnostics because `protoc-gen-es` annotates every export explicitly.
5. `addExtraLib` the `.d.ts` into Monaco (see §4.6).

**Bundling caveat (proven, mechanical).** `create-es-plugin.js` *statically* imports `transpile.js` → `typescript` + `@typescript/vfs` (which uses `fs`). So a browser bundle of the plugin only compiles with a **~10-line esbuild stub** aliasing `@typescript/vfs` + node builtins to a no-op (never executed on `target=ts`). The spike's bundle built **0 errors / 0 warnings**.

**Cost & scope notes for integration:**
- `typescript` is ~10MB in the bundle. Mitigations: the ui already depends on `typescript ^5.6.3`; run the `.d.ts` emit lazily / in a worker; or simplest — **feed `_pb.ts` straight to Monaco** (it type-checks but never executes it, so the runtime consts are inert and no separate `.d.ts` emit is needed). Use `transpileDeclaration` only if we want a literal, minimal `.d.ts`.
- **Messages only** (per user): we use *only* `@bufbuild/protoc-gen-es` — no `protoc-gen-connect-es`/`-connect-query`. It does still emit a `GenService` **descriptor** const (not a client) for service-bearing files; trivially trimmed if "messages only" must be literal, and irrelevant for body typing (we use the `…Json` types).

---

## 3. The three pillars & how each maps to existing infra

| Pillar | What it delivers | Reuses | New code |
|---|---|---|---|
| **A. Evaluate a TS body → JSON** | body is a script; its return value is the message | `RunGeneratorUncached` (arbitrary-object JSON), `entry.go` `export default` convention, the shared `[]string` pre-send pipeline | a `resolveInvokeBody` step; `body_language` field/plumbing |
| **B. Type the body (autocomplete + errors)** | message shape as a TS type → completions + excess-property/type errors (replaces JSON-Schema validation) | Monaco TS service (`monaco-scripts.ts` pattern), `addExtraLib`; **the real `protoc-gen-es` generator** | run **`protoc-gen-es` in-browser** over the reflected descriptor + `transpileDeclaration` (proven §2.5/§4.4), editor-mode switch |
| **C. Compose (call saved generators)** | generators in scope as typed functions, called directly — the `{{ }}` replacement | esbuild virtual-module resolver pattern (`registryResolverPlugin`), IIFE capture | a **workspace-generators resolver plugin** + a generated ambient `.d.ts` |

---

## 4. Design

### 4.1 Authoring model
The TS body is a module using the **existing** entry-point convention — consistent with how S1 generators are already authored:

```ts
import { mkmsg } from "grpcview:generators"   // pillar C; optional

export default (): RequestMessage => ({
  message: mkmsg(),          // ← saved generator, typed, autocompleted
  count:   42,
})
```

- `RequestMessage` is an **ambient type generated per selected method** and injected via `addExtraLib` (replaced when the method changes). The `: RequestMessage` return annotation makes TS type-check the returned object literal and offer key completions — this is what preserves "autocomplete + error detection," now via TS instead of JSON Schema.
- A plain-value default (`export default { … }`) is also supported (the body postlude handles function-or-value). Exact wrapper form (return-annotated arrow vs `satisfies RequestMessage` vs a hidden wrapper for a "bare object" feel) is locked to the hidden wrapper in §0 #4, with the return-annotated arrow as the fallback.
- **Metadata stays `{{ }}` this pass** (per-value strings; TS-ifying metadata is a separate design).

### 4.2 Backend: evaluate the body (pillar A)
Add a shared `resolveInvokeBody(ctx, workspaceName, bodies []string) ([]string, error)` and call it **before `resolveInvokeTokens`** at the two shared sites (`invoke.go:66` unary, `:177` streaming). When `body_language == BODY_LANGUAGE_TYPESCRIPT`:
- run each body as a generator via `RunGeneratorUncached` (uncached ⇒ `uuid()`/`now()` vary per invoke; sandboxed empty `Grant`);
- take `Result.Value` (raw JSON object) as the body;
- the existing `UnmarshalJSON` (`invoke.go:80/195`) and `json.Valid` middleware gate (`middleware.go:106`) then apply to the produced JSON unchanged.
- A throw / non-object return → `FailedPrecondition` naming the request (mirrors token/middleware error policy).

When `body_language == JSON` (default) the step is a no-op and today's path runs verbatim — **JSON mode and `{{ }}` are untouched.**

### 4.3 Backend: `body_language` plumbing (fully mapped)
A scalar enum, so the plain `optional`-pointer patch idiom (no set-flag). Mirror the existing `ScriptKind` two-schema-enum precedent:
- `proto/grpcview/v1/workspace.proto` — `BodyLanguage` enum + `Request.body_language` (field 8; `Request` is `:81-100`).
- `proto/grpcview/v1/service.proto` — `UpdateRequestRequest.optional BodyLanguage body_language` (field 11; message `:67-85`).
- `proto/grpcview/store/v1/storage.proto` — mirror the enum + `Request.body_language` (field 7; message `:62-72`).
- `service/store/convert.go` — both request converters (`:20-41`) + an enum bridge like `diskToWireScriptKind` (`:56-80`).
- `service/store/store.go` `RequestPatch` (`:62-70`) — `BodyLanguage *…`; `fs.go` `UpdateRequest` guard (`:204`) + apply next to `DraftBody` (`:230-231`).
- `service/workspace/workspace.go` UpdateRequest handler (`:440-457`) — thread `BodyLanguage`.

### 4.4 Client-side type generation (pillar B) — proven in-browser (§2.5)
The message `.d.ts` is generated **on the client** by running the real `@bufbuild/protoc-gen-es` generator in-browser against the reflected descriptor — reusing the exact generator the repo already uses at build time, not a hand-rolled walker and **not** a Go backend step. The mechanism, recipe, evidence, and caveats are in §2.5. Summary of the moving parts for this feature:
- **Input:** the reflected `FileDescriptorSet` for the target service (ship it to the client alongside the existing schema, or reuse the descriptor-set the client already has). No new backend generator.
- **Generate:** `protocGenEs.run({ fileToGenerate, protoFile: fds.file, sourceFileDescriptors: [], parameter: "target=ts,json_types=true" })` → `_pb.ts` (has `RequestMessageJson` = the protojson shape). Optionally `ts.transpileDeclaration` → minimal `.d.ts`.
- **Type name:** the body is typed against the generated `…Json` type for the method's input message (protojson shape — matches what `UnmarshalJSON`/protojson accepts on the wire).
- **Bundling:** stub `@typescript/vfs` + node builtins in the ui's esbuild/Vite config (~10 lines); or lazy-load the generator in a worker to keep it off the main bundle.

**Why the real generator, not the shipped JSON Schema:** the current JSON Schema is lossy/incorrect for exactly the cases types care about — WKTs (`Timestamp`/`Duration`/`Any`/wrappers/`Struct`) expand to raw `{seconds,nanos}` instead of protojson string forms; `int64/uint64` are `integer` not the string protojson round-trips; enums lose forms; oneofs lose mutual-exclusivity; snake-case aliases are absent. `protoc-gen-es` with `json_types=true` emits the **correct** protojson shape with zero mapping code for us to own. (MVP fallback if we ever want to avoid shipping the generator: derive a `.d.ts` from the JSON Schema on the client and inherit its gaps — not recommended now that the real path is proven trivial.)

### 4.5 Backend: compose with saved generators (pillar C)
Add an esbuild resolver plugin (mirroring `registryResolverPlugin`, `bundler.go:364`) that resolves a synthetic specifier `grpcview:generators`. Its `OnLoad` returns a generated module that, for each workspace generator, inlines the source and re-exports its `default` under the generator's name:

```ts
import __g0 from "grpcview:gen/mkmsg"; export const mkmsg = __g0;   // OnLoad(grpcview:gen/mkmsg) → mkmsg's source
```

esbuild bundles the whole graph in one pass; each generator keeps its own module scope; a generator's own `dayjs`/npm imports compose transitively. The call shape lines up: token `{{ mkmsg(42) }}` → `default(42)` equals import `mkmsg(42)` → `default(42)`. The set of generators is assembled per-run from the workspace (same source `tokens.go`/`middleware.go` already load) and handed to the engine. A matching ambient `.d.ts` (`declare module "grpcview:generators" { export function mkmsg(...a: any[]): any; … }`) gives the editor completions; arg/return typing is `any` in v1 (parse generator signatures later).

### 4.6 Frontend: the editor
- When `body_language == TS`, mount the body editor as `language="typescript"` on a distinct `file://` model URI (Node resolution; avoid colliding with the Scripts sandbox's shared `SCRATCH_PATH`/`console`/dayjs libs). Reuse the `monaco-scripts.ts` setup pattern.
- On method change: dispose + re-`addExtraLib` the generated `RequestMessage` type (and the `grpcview:generators` ambient decl). `typescriptDefaults` is **global** (no JSON-style `fileMatch`), but only one body editor is active at a time, so a dispose/re-add on method switch is sufficient. (The generated proto `.d.ts` graph from §4.4 is added the same way; feeding the `_pb.ts` directly is the simplest form since Monaco never executes it.)
- Error footer: reuse the marker-reading block from `Editor.tsx:88-100` verbatim (markers are language-agnostic).
- A per-request toggle (the `JSON · UTF-8` label at `MessageTab.tsx:68` becomes a `JSON ⇄ TypeScript` switch) persisted via `UpdateRequest.body_language`.

**Bonus:** the known S2 wart — Monaco's JSON validator red-flagging `{{ }}` as invalid JSON — simply doesn't exist in TS mode (no `{{ }}`). TS mode is effectively the real fix for it.

---

## 5. Phased plan (each phase independently shippable + browser-verified, S1–S3 rhythm)

- **T1 — Backend eval + `body_language` + raw TS toggle. ✅ DONE — commit `ce4e74d`, 2026-07-23, browser-verified.** Pillar A + §4.3, plus a minimal (untyped) TS editor toggle so the eval path is verifiable end-to-end. *Verify:* a TS body `export default () => ({ message: "hi-" + Math.random() })` invokes and the echo server returns it; JSON requests unaffected.
  - *Delta from plan:* `body_language` was also added to `InvokeRequest`/`InvokeStreamRequest` (field 9) — the invoke path reads editor state off the wire, not the saved store `Request`, so the toggle is carried on the invoke payloads (preserving "a send never depends on a prior UpdateRequest landing"). In-browser proof used **self-reflection** (`WorkspaceService.Get` with `{ workspaceName: "def"+"ault" }` → `0 OK`, workspace returned) rather than an echo server; mode + body persist across reload; JSON path unaffected.
  - *Review fixes folded in:* footer token chip hidden in TS mode; footer error count reset on the JSON⇄TS flip (a clean destination model fires no marker event). *Deferred:* history re-run doesn't snapshot `body_language` (a history-feature gap, not T1).
- **T2 — Typed body (pillar B).** Run `protoc-gen-es` in-browser over the reflected descriptor (proven §2.5/§4.4) → `.d.ts` + editor type injection + error footer. *Verify:* unknown field shows a red squiggle; valid fields autocomplete; a type error blocks with a clear message.
- **T3 — Composition (pillar C).** Generators resolver plugin + ambient decl + editor generator-completion/import-insertion. *Verify:* `import { mkmsg } from "grpcview:generators"; export default () => ({ message: mkmsg() })` → echo resolves — the `{{ }}` replacement, end to end.
- **T4 — Ergonomics & migration (optional).** "Bare object" hidden-wrapper mode (needs the Monaco position-mapping spike, §0); one-click *convert JSON body → TS*; typed generator signatures; (later) TS metadata + a deprecation path for `{{ }}`.

## 6. Risks & decisions to confirm before building

1. **Composition is required for parity** (§2). If we'd rather *not* build it in v1, the alternative is introducing real engine builtins (`uuid`/`now`/…) and deferring saved-generator calls — a smaller T3 but a different feature. **Decision (locked §0 #1):** virtual-module composition.
2. **Type source: RESOLVED (§2.5 spike).** Run the real `protoc-gen-es` client-side — proven in-browser, correct protojson types, zero mapping code to own. The JSON-Schema→`.d.ts` client shortcut is the only fallback and is not needed.
3. **Coexistence vs replacement:** locked (§0 #3) — TS becomes the default and existing bodies migrate; JSON stays as an escape hatch. The global flip lands with T3 so token→call rewrites resolve.
4. **Authoring wrapper:** locked (§0 #4) — hidden wrapper (bare-object look) with an expand-to-module escape hatch; return-annotated arrow is the fallback if the two-model position-mapping proves janky. **Open sub-task:** the position-mapping Monaco spike (do before T4).
5. **Always-eval cost:** every TS-mode invoke runs the sandbox (~ms + 5s ceiling) even for a static object. Acceptable under the sandboxed-only posture; a "parses as static JSON ⇒ skip eval" fast-path is a later optimization.
6. **Metadata** stays `{{ }}` this pass; unifying it into TS is deferred to T4+.
7. **Bundle weight (new, from §2.5):** the in-browser generator pulls `typescript` (~10MB). **Decision for T2:** feed `_pb.ts` directly to Monaco (no separate `.d.ts` emit, no extra `typescript` on the main thread since Monaco already has it) vs. lazy-load a worker that runs `transpileDeclaration`. Lean: feed `_pb.ts` directly.
