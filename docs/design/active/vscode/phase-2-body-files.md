# Phase 2 — body and metadata as files

**Prereqs:** [phase 1](../../shipped/vscode/phase-1-workspace.md). **Unblocks:** reveal-body-as-file,
CI type-checking, `import`s from shared modules. See [`README.md`](./README.md) for the
track overview.

**Build this before [`script-imports.md`](../../planned/script-imports.md).** That track
wants the whole workspace import graph from one `Write: false` esbuild build, which
requires every entry to be a real file on disk; while bodies live inside `request.json`
they have to be fed through `Stdin`, one build per body. This phase is what makes bodies
ordinary entry points. Nothing here is invalidated by that track — it keeps the hidden
wrapper — so the order is strictly one-way.

## Goal

Move `draft_body` and `draft_metadata_script` out of `request.json` into sibling
`body.ts` and `metadata.ts` files.

This is the highest-value phase and it is close to `writeFile(dir/body.ts, draftBody)`.
One assumption it was originally scoped on is gone, and the reason matters: it read
`isCanonical`/`migrateBodyToTs` (`body-wrapper.ts`) as guaranteeing every persisted body
is a complete canonical module. Under
[the body contract](../../request-body-contract.md) that guarantee is gone by design — a
persisted body may be a module or a bare expression (which includes plain protojson,
since valid JSON is valid TS), and this phase is exactly what makes a hand-written one
easy to produce. The split must therefore preserve the bytes rather than normalize them;
it does *not* have to record which form they are, because both forms live in the same
`.ts` file and the backend already accepts either.

## Why

- **`tsc`-checkable and editor-agnostic.** Today the body is a JSON string; nothing
  outside the browser can check or edit it meaningfully.
- **Line diffs.** `docs/design/shipped/storage.md` lists "diff-first" as a principle, and the
  body is the one place it is violated — currently a single `"{\n  userId: …"` string.
- **AI tooling sees request bodies as code**, with types in scope.
- **`import` works.** A body could `import { authHeaders } from "../../shared/auth"`.
  Today it can only reach generators via injected globals, which is a real
  expressiveness ceiling.

## Changes

- **`proto/grpcview/store/v1/storage.proto:78,82`** — delete `draft_body` and
  `draft_metadata_script` from the on-disk `Request`. No `reserved` markers (project
  stage). The **wire** `grpcview.v1.Request.draft_body` stays a `string` — this whole
  phase lives below the wire API, so `ui/` is untouched.
- **`service/store/layout.go:15-21`** — add `bodyFileName = "body.ts"` and
  `metadataFileName = "metadata.ts"` next to `requestFileName`, and add both to
  `reservedSlugs` (`layout.go:68-72`) so no child directory can collide with them.
- **Always `.ts`, never `.json`.** Valid JSON is valid TypeScript, so `.ts` is never the
  wrong extension for a body — only a less informative one when the content happens to be
  plain protojson. That small loss buys three things: no rename-on-form-change (a body
  that stops being valid JSON is just a write), no read-time ambiguity about which of a
  pair wins, and — decisively — bodies that work as esbuild entry points, because esbuild
  picks a loader from the extension and a `body.json` entry would be parsed by the JSON
  loader, so a `require("@/…")` inside it is a syntax error. Overriding the workspace-wide
  `.json` loader to TypeScript to dodge that would break real JSON imports. See
  [`script-imports.md`](../../planned/script-imports.md).
- **The extension is still not a behavior switch.** Whatever the bytes are, they go
  through the same backend wrap as every other surface. A `.ts` holding plain protojson
  and a `.ts` holding a module must both work — that is
  [the body contract](../../request-body-contract.md), unchanged.
- **`service/store/fs.go:261-266`** — patch application writes the two files instead
  of setting proto fields.
- **`service/store/convert.go:8-18`** (`diskToWireRequest`) — store→wire reads them back
  into the wire `Request`.
- **Always write both files on request creation**, so "file absent" is never a state
  anyone has to interpret. (An absent body is nonetheless *legal* now — the contract
  reads it as `{}` — so this is a diff-hygiene choice, not a correctness one.) Seed them
  with the backend's `emptyBody` (`service/workspace/invoke.go:41`, `"{}"`), **not** the
  UI's `EMPTY_BODY` (`body-wrapper.ts:16`) — that one is a module-private const on the
  editor side, and creation happens in the store.
- **Keep `WRAP_PREFIX`/`WRAP_SUFFIX`/`PREFIX_LINES` unchanged.** The per-method
  `declare global { type RequestMessage = … }` alias keeps working because only one
  method is active per editor. The generated `import type … as RequestMessage` line is
  a `DiskSink` concern ([phase 5](./phase-5-extension.md)), *not* a split concern —
  introducing it here would be premature.
- **Hard break, migrated by hand. No converter.** Every `request.json` on disk today
  carries the two fields, but only two collections exist — the in-repo `example/` one and
  the author's local workspace — so both are converted manually and the loader only ever
  understands the new layout. Read-both is rejected for the same reason
  [`script-imports.md`](../../planned/script-imports.md) rejects it: two resolution models
  is what this deletes.
- **The hard break must be loud, and by default it is silent.** `unmarshalOpts` is
  `protojson.UnmarshalOptions{DiscardUnknown: true}` (`service/store/codec.go:16`), so an
  un-migrated `request.json` loads *successfully* with an empty body, and the next write
  drops `draftBody` from the file — the body is gone with no error anywhere. Detect the
  two stale keys when reading a `request.json` and fail with a message naming the file
  and the fix. This is the only real work the hard break costs.
- **Tighten the runtime contract** — see [`body-contract.md`](./body-contract.md) for the
  editor layers and [`request-body-contract.md`](../../request-body-contract.md) for what
  is accepted. Layer 4 (wrap unless it already default-exports → evaluate → `protojson`
  unmarshal, with an error that names the file and the field) is the only real enforcement
  and should land with this phase, since bodies are now hand-editable by anything.
- **Do not normalize on read.** A `body.ts` holding plain protojson that the user never
  edited must round-trip byte-identical; rewriting it as a wrapped module on load is a
  spurious git diff on a file the user never touched, and it discards the form they
  deliberately authored.

## Watch out

- **`readChildren` (`fs.go:644`) already unmarshals every `request.json` it walks**, and
  caches the whole message on `childEntry` (`layout.go:51-58`), but it needs only slug,
  name and kind — it is an ordering pass. Do not give it a second read per child for the
  body and metadata files; only `readItem`/`Load` need those.
- **Accepted wart:** a `body.ts` opened outside the app — plain VS Code, `tsc` — shows
  one unresolved type name, `RequestMessage`, because the app supplies it per-editor as a
  `declare global` alias for the selected method's input.
  [Phase 5's `DiskSink`](./phase-5-extension.md) fixes it with a real import; until then
  it stands as-is.

## Verify

- `bazel test //service/store/... //service/workspace/...`
- Browser (prod binary reflecting itself on `:10000`): the body and metadata editors,
  IntelliSense, and invoke all behave identically to before. The hidden wrapper still
  hides exactly one line at each end.
- **Dogfood:** `example/` already targets grpcview's own API and is committed, so the
  migration lands there as a real diff. Confirm the converted bodies are byte-faithful,
  then edit one and check `git diff` shows a line diff rather than one mutated JSON
  string. Re-run its requests through MCP `invoke_saved` / `invoke_saved_streaming` and
  the smoke scenario, which is how `example/` is verified today.
- Hand-edit a `body.ts` outside the app, then invoke: the change is picked up (the
  store re-reads on every RPC).

## Settled

- **Extension: always `.ts`**, never `.json` — see Changes. The rename-on-form-change
  work the alternative implied is not built.
- **Metadata splits too.** It is TypeScript, so it is a file, on the same reasoning as the
  body. Two `.ts` files per request directory is the accepted cost; the alternative left
  the two halves of one authoring surface persisting differently.
- **Migration is a hard break, done by hand** — see Changes.
- **Generated files carry no header comment.** A write is the body bytes and nothing
  else, so it never depends on what the file already contains.
