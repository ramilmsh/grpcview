# Phase 3 — extract the type producer/sink seam

**Prereqs:** none strictly (independent of [phase 2](../../shipped/vscode/phase-2-body-files.md)).
**Unblocks:** `DiskSink` (phase 5), tsserver-plugin and tsgo-IPC sinks (phase 6).
See [`README.md`](./README.md) for the producer/sink model and the write-only
invariant.

## Goal

A pure refactor with **no behavior change**: lift the snapshot type, the producer, and
`MonacoSink` out of the code that currently interleaves type *generation* with Monaco
`addExtraLib` calls.

## Why now

Adding a second sink later without this seam means threading Monaco-specific calls
through the extension host. Doing it as a standalone no-op refactor means the risky
part (a new sink) lands against a tested seam, and it is independently verifiable —
the registered extra-lib set should be byte-identical before and after.

## Current shape

Generation and registration are currently mixed across:

- `ui/src/features/workspace/proto-types.ts` — `generateWorkspaceTypes` (runs
  `protoc-gen-es` over the merged FDS, memoized on the descriptorSet reference),
  `resolveLocalSymbol`, `requestMessageAlias`, `messageTypeText`. Already pure —
  string-in/string-out — so it moves into the producer wholesale.
- `ui/src/features/workspace/Editor.tsx` — registers the generated files and re-adds
  the per-method `RequestMessage` alias on method change.
- `ui/src/features/workspace/MetadataEditor.tsx` — the same for the metadata editor.
- `ui/src/features/workspace/generator-libs.ts` — `registerGeneratorLibs` registers
  each generator's source as a virtual module plus one shared globals `.d.ts`.
- `ui/src/features/scripts/monaco-scripts.ts` — compiler options, the ambient
  `gv.d.ts`, and the virtual `node_modules/dayjs` package.

## Changes

- New `ui/src/lib/types/` (or `features/workspace/types/`):
  - `snapshot.ts` — `TypeSnapshot`, `RequestTypeRef`, `TypeSink` (see README).
  - `producer.ts` — builds a `TypeSnapshot` from `(descriptorSet, requests,
    generators)`. Absorbs `proto-types.ts` and the generator-signature derivation from
    `generator-libs.ts`. Must stay free of any `monaco-editor` import so the extension
    host can call it.
  - `monaco-sink.ts` — the only module that touches
    `monaco.languages.typescript.typescriptDefaults`.
- **Compute the digest.** The producer needs a stable descriptor-set digest for the
  freshness stamp. `generateWorkspaceTypes` currently memoizes on the `Uint8Array`
  *reference* (`proto-types.ts:26`, a `WeakMap`), which is fine in-page but is not a
  value the extension host or a `types.stamp` can compare. Hash the bytes.
- **Keep the ambient/static libs where they are.** `gv.d.ts`, the dayjs package and
  the compiler options in `monaco-scripts.ts` are not descriptor-derived and are not
  part of the snapshot. Do not pull them in.

## Verify

- Browser: IntelliSense, hovers, signature help and diagnostics unchanged on the body
  editor, the metadata editor, and generator globals (`mkid()` and friends still
  autocomplete with their real inferred signatures).
- Method switching still re-points `RequestMessage` at the new input message.
- Mechanical check: log the registered extra-lib paths + content lengths before and
  after the refactor and diff them — they should match exactly.
- `TypesModal` (which uses `messageTypeText`) still renders the message shape.

## Open questions

- Does the producer own generator signatures at all, or do those stay a separate
  Monaco-only concern? They are workspace-derived but not *descriptor*-derived, so
  they invalidate on a different trigger (script edits, not source refresh). Leaning:
  a second, separately-stamped snapshot rather than one bag.
- Where does the digest live on the wire so the extension host does not have to hash
  the FDS itself — a new field on the `Get` response?
