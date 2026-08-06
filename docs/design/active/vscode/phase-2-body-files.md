# Phase 2 — body and metadata as files

**Prereqs:** [phase 1](../../shipped/vscode/phase-1-workspace.md). **Unblocks:** reveal-body-as-file,
CI type-checking, `import`s from shared modules. See [`README.md`](./README.md) for the
track overview.

## Goal

Move `draft_body` and `draft_metadata_script` out of `request.json` into sibling
`body.{ts,json}` and `metadata.{ts,json}` files.

This is the highest-value phase and it is close to `writeFile(dir/body.ts, draftBody)`.
It is no longer *quite* free, though, and the reason matters: this phase was originally
scoped on the assumption that `isCanonical`/`migrateBodyToTs` (`body-wrapper.ts`)
guarantee every persisted body is a complete canonical module. Under
[the body contract](../../request-body-contract.md) that guarantee is gone by design — a
persisted body may be a module or a bare expression (which includes plain protojson,
since valid JSON is valid TS), and this phase is exactly what makes a hand-written one
easy to produce. So the split must carry the form along rather than assume it.

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

- **`proto/grpcview/store/v1/storage.proto:72,82`** — delete `draft_body` and
  `draft_metadata_script` from the on-disk `Request`. No `reserved` markers (project
  stage). The **wire** `grpcview.v1.Request.draft_body` stays a `string` — this whole
  phase lives below the wire API, so `ui/` is untouched.
- **`service/store/layout.go:17`** — add `bodyFileName = "body.ts"` and
  `metadataFileName = "metadata.ts"` next to `requestFileName`, **plus their `.json`
  counterparts**. Add all four to `reservedSlugs` so no child item can collide with them.
- **The extension is an editor hint; it does not decide behavior.** Write `.json` when the
  body is valid JSON and `.ts` otherwise, so the file opens with the right language
  service and diffs as what it is. On read, whichever of the pair exists wins (`.ts` if
  both somehow exist, and log it) — the bytes then go through the same backend wrap as
  every other surface. Never branch invoke behavior on the extension: a `.ts` holding
  plain protojson and a `.json` holding a module must both work.
- **Switch extension only on a form change.** A body that stops being valid JSON becomes
  `body.ts`; that is a rename plus a write, and the old file must be removed or the pair
  goes stale. This is the one genuinely new piece of work the contract adds to this phase
  — and it is worth confirming it is wanted at all, since the alternative is *always*
  `body.ts` (valid JSON is valid TS, so it is never the wrong extension, only a less
  informative one).
- **`service/store/fs.go:265-269`** — patch application writes the two files instead
  of setting proto fields.
- **`service/store/convert.go:25`** — store→wire reads them back into the wire
  `Request`.
- **Always write both files on request creation**, seeded with the canonical
  `EMPTY_BODY` shape (`body-wrapper.ts`), so "file absent" is never a state anyone has
  to interpret. (An absent body is nonetheless *legal* now — the contract reads it as
  `{}` — so this is a diff-hygiene choice, not a correctness one.)
- **Keep `WRAP_PREFIX`/`WRAP_SUFFIX`/`PREFIX_LINES` unchanged.** The per-method
  `declare global { type RequestMessage = … }` alias keeps working because only one
  method is active per editor. The generated `import type … as RequestMessage` line is
  a `DiskSink` concern ([phase 5](./phase-5-extension.md)), *not* a split concern —
  introducing it here would be premature.
- **Tighten the runtime contract** — see [`body-contract.md`](./body-contract.md) for the
  editor layers and [`request-body-contract.md`](../../request-body-contract.md) for what
  is accepted. Layer 4 (wrap unless it already default-exports → evaluate → `protojson`
  unmarshal, with an error that names the file and the field) is the only real enforcement
  and should land with this phase, since bodies are now hand-editable by anything.
- **Do not normalize on read.** A `body.json` the user never edited must round-trip
  byte-identical; rewriting it as a wrapped TS module on load is a spurious git diff on a
  file the user never touched, and it discards the form they deliberately authored.

## Watch out

- **`readChildren` reads configs for *ordering* only** (slug, name, kind — see the
  `childEntry` cache comment at `layout.go:42-52`). Do not make it read bodies; only
  `readItem`/`Load` need them.
- **Accepted wart:** a `body.ts` opened in a plain text editor shows one unresolved
  type name (`RequestMessage`). Cover it with a generated header comment; phase 6
  removes it properly.

## Verify

- `bazel test //service/store/... //service/workspace/...`
- Browser (prod binary reflecting itself on `:10000`): the body and metadata editors,
  IntelliSense, and invoke all behave identically to before. The hidden wrapper still
  hides exactly one line at each end.
- **Dogfood:** create a `requests/` collection in this repo, author a couple of real
  requests against grpcview's own reflection, and **commit the request files**. Then
  edit a body and confirm `git diff` shows a line diff rather than one mutated JSON
  string.
- Hand-edit a `body.ts` outside the app, then invoke: the change is picked up (the
  store re-reads on every RPC).

## Open questions

- Extension for the metadata file: `metadata.ts` reads well but is a second TS file
  per request directory. Alternative is folding metadata back into `request.json` and
  splitting only the body — cheaper diffs, but then the two authoring surfaces behave
  differently for no good reason. Lean: split both.
- Does a body get a generated header comment (`// grpcview: edit via the app or as
  TypeScript…`), and if so does the app preserve it across writes?
