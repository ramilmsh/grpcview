# Phase 2 — body and metadata as files

**Prereqs:** [phase 1](./phase-1-collection-dir.md). **Unblocks:** reveal-body-as-file,
CI type-checking, `import`s from shared modules. See [`README.md`](./README.md) for the
track overview.

## Goal

Move `draft_body` and `draft_metadata_script` out of `request.json` into sibling
`body.ts` and `metadata.ts` files.

This is the highest-value phase and it is nearly free, because
`isCanonical`/`migrateBodyToTs` (`body-wrapper.ts`) already guarantee every persisted
body is a complete canonical module. The split is close to
`writeFile(dir/body.ts, draftBody)`.

## Why

- **`tsc`-checkable and editor-agnostic.** Today the body is a JSON string; nothing
  outside the browser can check or edit it meaningfully.
- **Line diffs.** `docs/design/storage.md` lists "diff-first" as a principle, and the
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
  `metadataFileName = "metadata.ts"` next to `requestFileName`. Add both to
  `reservedSlugs` so no child item can collide with them.
- **`service/store/fs.go:265-269`** — patch application writes the two files instead
  of setting proto fields.
- **`service/store/convert.go:25`** — store→wire reads them back into the wire
  `Request`.
- **Always write both files on request creation**, seeded with the canonical
  `EMPTY_BODY` shape (`body-wrapper.ts`), so "file absent" is never a state anyone has
  to interpret.
- **Keep `WRAP_PREFIX`/`WRAP_SUFFIX`/`PREFIX_LINES` unchanged.** The per-method
  `declare global { type RequestMessage = … }` alias keeps working because only one
  method is active per editor. The generated `import type … as RequestMessage` line is
  a `DiskSink` concern ([phase 5](./phase-5-extension.md)), *not* a split concern —
  introducing it here would be premature.
- **Tighten the runtime contract** — see [`body-contract.md`](./body-contract.md).
  Layer 4 (callable default export → await → `protojson` unmarshal, with an error that
  names `body.ts` and the field) is the only real enforcement and should land with this
  phase, since bodies are now hand-editable by anything.

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
