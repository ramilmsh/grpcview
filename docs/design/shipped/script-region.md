# The script region

**A wrapped body is not "text that happens to match a constant". It is a region marked
by comments, with everything outside the markers owned by the machine.**

This replaces the hidden-wrapper model in `body-wrapper.ts` / `metadata-wrapper.ts`.

**Shipped 2026-08-09** (W1–W6). The doc below is left in the present tense it was written
in, as a record of the decisions; what the code does now is in `AGENTS.md` under "Request
authoring model". Where the implementation contradicted a claim made here, the claim is
left in place and followed by a **Shipped correction** — D6, D7 and D8 each carry one.

## The problem being solved

A request body has two forms, and only one of them is JSON-like:

```ts
{ id: 1 }                                    // JSON-like: gets a wrapper, hidden
export default async () => ({ id: 1 })       // a module: shown whole, nothing hidden
```

Today the wrapper is a **constant string** and "is this wrapped?" is
`text.startsWith(WRAP_PREFIX) && text.endsWith(WRAP_SUFFIX)` (`body-wrapper.ts`).
Hidden-line geometry is a matching constant, `PREFIX_LINES = 4`.

That constant is what breaks. Accepting an auto-import inserts `import { requestId }
from "#/scripts/ids";` at the top of the file, the exact match fails, and the body
stops being wrapped — mid-edit, with the editor and the saved draft disagreeing until a
reload. Every workaround for this (a corruption guard that reverts, a `lastGood`
snapshot, live promotion into the plain form) is machinery that exists only because the
mode is inferred from an exact string.

Markers make the mode **stated** rather than inferred, which is what lets the region
above the markers change freely.

## The shape

```ts
import { requestId } from "#/scripts/ids";

export default async (): Promise<RequestMessage> => (
// grpcview:script start
{
  id: requestId(),
}
// grpcview:script end
)
```

The editor hides line 1 through `// grpcview:script start`, and `// grpcview:script end`
through EOF. The author sees three lines, numbered 1–3.

Everything above the start marker is **machine-owned**: the import block is derived from
what the region references, and the skeleton (`export default async (): Promise<T> => (`)
is a constant regenerated at the read seam. The author never types there and never sees
it.

The same markers, with the same text, delimit `metadata.ts`. The filename says which
skeleton applies; there is no `grpcview:metadata start` variant.

## Decisions

Each of these was argued and settled. A few look like defects until you read the reason —
those are marked **do not "fix"**.

### D1. Markers delimit the region; hidden areas derive from marker position

Not from a constant. `PREFIX_LINES` disappears; the gutter offset is the start marker's
line number, which moves as the import block grows. This is the whole point: an inserted
import no longer changes what "wrapped" means.

*Rejected:* keeping `isCanonical` as an exact prefix/suffix match. It is why the current
code needs a corruption guard, and why auto-import breaks the mode.

### D2. The shim carries no standard imports

The skeleton is one line plus the markers plus `)`. `invoke`, `params` and `inherit` are
**not** ambient. They are ordinary auto-import candidates from `grpcview:invoke`,
`grpcview:request` and `grpcview:metadata`, and they appear in the import block only when
the region references them.

Consequence, accepted deliberately: in a fresh `{}` body, typing `params.x` is an error
until you accept the completion. Today it just works, because the wrapper imports them
unconditionally.

*Why:* an import block that is a pure function of the region is regenerable and prunable.
One that mixes always-on entries with derived entries is neither.

### D3. The region's first token decides the mode

Skipping whitespace **and whole comments** — `leadsWithBrace` in `module-sniff.ts` already
does this and survives. `{` means JSON-like and wrapped; anything else (`[`, an
identifier, `import`, `export`) is a script the author owns.

An **empty region holds the current mode.** Deleting the last `}` of `{}` must not rip the
shim out and then put it back as you retype. Switch only on a definite non-`{` token.

### D4. Mode switches are text edits, pushed as one undo stop

- **wrapped → plain**: delete the two marker lines, stop hiding. The skeleton and imports
  stay, now visible and author-owned. The typed contract is preserved — the author gets
  the module they were implicitly inside.
- **plain → wrapped**: fires only when the *whole file's* first token is `{` (a bare
  object). Insert skeleton + markers around it, hide.

Your keystroke and the shim edit go in one undo stop, so one Cmd-Z restores the prior
state. If monaco insists on two stops, one press still leaves a consistent state — text
without markers, mode recomputed as plain from that text — so the failure mode is an extra
keypress, never corruption.

*Rejected:* mode switch as a pure display change (keep markers, only toggle hiding). It
avoids the undo problem, but it means a shim the author edited while plain gets re-hidden
with their edits inside it, which is the hazard the whole design exists to avoid.

**Shipped correction (2026-08-10).** "The skeleton and imports stay" was wrong as a
default. Leaving `export default async (): Promise<RequestMessage> => (` and its `)`
behind is boilerplate the author did not write and now has to delete by hand, and the
typed contract it was meant to preserve is not needed for the value to invoke — the
backend wraps a bare expression itself (`resolveInvokeBody`, see
`../request-body-contract.md`). `unwrapEdits` now takes the whole shim with it, and keeps
it only where it is load-bearing (`shimIsDisposable` in `region-edits.ts`):

- the header holds an **import block** — `import` cannot stand in the expression position
  the plain text would then occupy, so the module form has to survive;
- the region **leads with `{`** — only resolve-or-bail can unwrap such a region, and a bare
  object left plain re-wraps on the next `onDidChangeModelContent`, silently undoing the
  bail.

`unwrapToPlain` in `script-region.ts`, the pure counterpart with the old semantics, had no
callers and was deleted rather than kept in two shapes.

### D5. Auto-import inserts on completion accept, in both modes

Wrapped: the import lands above the start marker, invisible. Plain: it lands in the
visible import block, and the author watches it appear.

This is what `module-auto-import.ts` already does via `additionalTextEdits`. Under D1 it
is no longer destructive.

**Shipped correction (2026-08-10).** D2 makes `invoke`, `params`, `assert` and `inherit`
"ordinary auto-import candidates", but the provider only ever walked the workspace module
list, so none of the four was ever suggested while typing — the TS worker's own quick fix,
which needs the unresolved name already written, was the only way to get the import. The
provider now iterates `candidatesFrom` (`resolve-imports.ts`), the same index resolve-or-
bail uses, so the virtuals are offered from the first keystroke and in an empty workspace.

### D6. Resolve-or-bail runs on paste, drop and programmatic replacement — never on typing

On paste of a region referencing names that are not in scope, resolve each against the
workspace export index plus the `grpcview:*` virtuals:

- exactly one match → add the import
- zero matches, or two or more → **bail**: perform the wrapped → plain switch of D4 and
  leave the unresolved names alone, as ordinary red squiggles, for the author to fix

**Typing is excluded and this is load-bearing.** Diagnostics cannot distinguish a
half-typed `requestI` from an unresolvable name, so a bail-on-every-edit rule tears the
wrapper out on the way to typing any new identifier. While typing, an unknown name is just
an error you clear by accepting a completion (D5).

The pass is async (it waits on the TS worker), so a failing paste flips the region to plain
a beat after the paste, not instantly. In wrapped mode the import block updating is
invisible, so only the bail is ever seen.

**Shipped correction:** the pass runs on **paste only**, not on drop. Monaco's
`codeEditorWidget.js` fires `onDidPaste` from `_paste` solely when the source is
`"keyboard"`, and it exposes no drop event at all, so there is no honest drop signal to
hook — a fabricated one was rejected over inventing a path the editor does not report.
"Programmatic replacement" likewise never arms it: the only programmatic writes are the
editors' own `setValue` on a request switch, which explicitly clears the arm flag so a
saved file that opens with red squiggles is not unwrapped on load.

### D7. Pruning is aggressive, but only of machine-owned imports

An import the author cannot see and did not write must not be allowed to go stale: a
leftover `requestId` from module A silences the completion that would offer `requestId`
from module B, because `namesAlreadyInScope` (`auto-import.ts`) skips names already
imported. So the hidden block is pruned on serialize.

Visible imports — plain mode, or the block after a wrapped → plain switch — are the
author's and are never pruned. Deleting a visible import on a debounce timer is exactly
the "behind my back" behaviour this design rejects; a stale one there is in plain sight
and can be deleted by hand.

**Shipped correction:** "pruned on serialize" is not where it happens. There is no
serialize step — the model text, the draft and the invoke payload are the same string —
so the prune runs in the editor, on an 800 ms idle debounce off the last content change
(`Editor.tsx` / `MetadataEditor.tsx`), sharing one worker round trip with D6's pass.

### D8. The TS worker is the oracle. Do not hand-roll a parser

Unresolved names come back as `TS2304 Cannot find name 'x'`. Unused imports come back as
unused-identifier diagnostics — confirm whether monaco's worker exposes
`getSuggestionDiagnostics`; if not, fall back to a references-at-position check on each
binding and treat "only the declaration" as unused.

*Rejected:* scanning the region for identifiers with a regex over `maskLiterals` output.
It cannot tell `{ id: 1 }`'s **key** from a reference, so it either imports property names
or drops needed imports. The language service already does this scoping correctly.

**Shipped correction:** monaco's worker does expose `getSuggestionDiagnostics`, so the
references-at-position fallback was never needed. But the assumption that an
unused-import diagnostic points at the bound identifier is wrong. Verified against the
`typescript` package's own language service: **6133's span is the whole import
declaration** whenever the declaration has exactly one binding and that binding is unused,
and only a bare identifier when it is one of several bindings and the others are still
used; 6192 is always the whole declaration. The code alone does not distinguish the two
shapes — the span's own text does, since a whole declaration starts with `import` and a
bound name never does — so `pruneEdits` dispatches on the shape, not on which code fired.

### D9. Imports are sorted by specifier, then by name

The block is regenerated, so without a total order it churns in git on every edit.

### D10. No daemon pass. The skeleton is normalized at the UI read seam

The read seam already hosts and strips text on open (`migrateBodyToTs`,
`hostMetadataScript`). Regenerating the skeleton there costs no Go changes and does not
write to the workspace at startup. A shim-version bump repairs files as they are opened;
an old-shim file invoked from the CLI meanwhile still works, being self-consistent.

Normalize the **skeleton only** — the `export default` line, the markers, the `)`.
Preserve the import block, which is derived content, not shim.

*Rejected:* a normalization pass at daemon start that rewrites every `body.ts` and
`metadata.ts` in the workspace. It writes to the user's files on startup, and it cannot
regenerate the import block without reimplementing the resolver in Go.

### D11. Pre-marker bodies read as plain scripts. There is no migration

Any `body.ts` / `metadata.ts` without both markers is a plain script, shown whole. That
includes every file carrying the current marker-less wrapper.

This is a deliberate one-time break, chosen over a read-seam migration that recognizes the
old wrapper. `example/` gets rewritten by hand with markers (see "Work" below).

### D12. `grpcview:*` modules stay universal. **Do not "fix" this**

`inherit` imported into a **body** does not fail: `bundler.go` defines it as
`globalThis.__grpcview_inherited || {}`, and `__grpcview_inherited` is only written on
metadata runs (`service/workspace/invoke.go:442`, `:508`). A body gets `{}` back.

A per-surface resolver gate was designed and then **rejected as unnecessary**: there is
nowhere to configure inherited metadata for a body, so there is nothing an author could
expect `inherit()` to return there. It is benign. Do not add a gate, and do not filter the
completion list per surface — IntelliSense offering all four modules matches what the
runtime actually does.

### D13. One marker text for both surfaces

`// grpcview:script start` / `// grpcview:script end` in `body.ts` and `metadata.ts`
alike. The filename already says which skeleton applies, and one helper then serves both
editors.

## Hazards carried forward

These are established facts about the current code that the new design must not break.

- **`=> ( … )` parens are load-bearing.** Without them `{ … }` parses as a block, not an
  object literal.
- **`import` is a statement** and cannot appear in expression position. This is why the
  region is expression-only and why the import block must live above the start marker —
  and why a bare-object body reaching the backend by any other route has to use
  `require("…")`.
- **The store normalizes to exactly one trailing newline** (`writeSourceFile` in
  `service/store/codec.go`). Persisted text arrives as `"<text>\n"`. Any end-marker scan
  must tolerate it, and the round trip must stay byte-identical or every open dirties the
  file.
- **`maskLiterals` (`module-sniff.ts`) mirrors the Go bundler's** `maskLiterals`
  (`service/scripting/bundler.go`) byte-for-byte, and blanks comment *interiors* while
  leaving `//` and `/*` delimiters in place. It is not a substitute for a token scan —
  `leadsWithBrace` exists because masked text still begins with `/` for a leading comment.
- **`hasDefaultExport` (`service/scripting/entry.go`)** decides entry-vs-scratchpad on the
  backend: a module is compiled as an entry, anything else is a scratchpad whose last
  expression is the value. A bare `[1, 2, 3]` left after a mode switch is therefore still
  a valid body.
- Monaco's `baseCompilerOptions` has **no `noUnusedLocals`**, and `setHiddenAreas` exists
  on the editor but is stripped from the public `monaco.d.ts`.

## Work

Ordered so each step is verifiable on its own.

**W1 — the marker seam.** Replace `isCanonical` with a marker scan in `body-wrapper.ts`
and `metadata-wrapper.ts`: find the start/end marker lines, derive `bodyBounds` /
`metaBounds` and `hiddenLineRanges` from them, drop `PREFIX_LINES` / `META_PREFIX_LINES`
and the geometry constants. Skeleton normalization at the read seam (D10). Unit tests for
marker scanning, trailing-newline tolerance, and a file with one marker or none.

**W2 — editor simplification.** `Editor.tsx` and `MetadataEditor.tsx`: delete the
wrapper guard, `lastGood`, the revert path and the live-promotion logic. Keep the seam
key-swallowing, the shadow select-all, the format-selection dance and the marker-line
filtering of error counts, all now driven by marker-derived bounds. Gutter offset from the
start marker line.

**W3 — mode switching.** D3 + D4. Region-first-token watch, the two text edits, one undo
stop.

**W4 — derived imports.** D2 + D5 + D7 + D8 + D9. The TS-worker-backed resolve and prune,
the generated block, deterministic ordering. Verify the D8 fallback question
(`getSuggestionDiagnostics` availability) before building on it.

**W5 — resolve-or-bail.** D6. Paste/drop only.

**W6 — `example/` and docs.** Rewrite the `body.ts` files and the two `folder.json`
inline metadata scripts with markers and no standard imports (six `body.ts` files, not
five — `describemethod-json/body.ts` is bare protojson with no wrapper and stays exactly
as it is, being the byte-identical-round-trip demo). Rewrite the `AGENTS.md` "Request
authoring model" section, which currently documents the constant-wrapper model. Move this
doc to `shipped/`.

## Starting state

Trunk plus **uncommitted** work in the tree, which this plan partly supersedes:

- `module-sniff.ts` gained `leadsWithBrace` — **keep**, D3 uses it as-is.
- `body-wrapper.ts` / `metadata-wrapper.ts` gained a two-import wrapper and
  `PREFIX_LINES = 4`, and inverted detection to first-token — the **inversion survives as
  D3**, the imports and the constant geometry do **not** (D1, D2).
- `Editor.tsx` / `MetadataEditor.tsx` gained live promotion of a wrapped body into a plain
  module when an edit breaks the canonical match — **deleted by W2**; D1 removes the
  condition that made promotion necessary.
- `example/` was rewritten to the two-import wrapper — **rewritten again** by W6.
- `AGENTS.md` documents all of the above — **rewritten** by W6.

Decide with the user whether to commit that work first as a checkpoint or to build on the
dirty tree.

## Verifying

`npx vitest run src/features/workspace` and `npx tsc --noEmit` in `ui/`. Three test files
fail to collect on `@grpcview/v1/*` outside bazel — pre-existing, not caused by this work.

Browser verification cannot use `localhost:10000`: that is the user's own running
grpcview, rooted at their workspace, and typing in a body editor auto-saves into their
collections. Instead:

1. `cp -R example <scratch>/example`
2. `bazel run //service/cmd/dev -- -port 10002 -workspace <scratch>`
3. Temporarily point `ui/src/lib/client.ts`'s dev `baseUrl` at `:10002`, `bazel run
   //ui:dev`, and **revert `client.ts` afterwards**.

The cases worth driving by hand: wrapped body shows only the region and numbers from 1;
accepting an auto-import keeps it wrapped and the gutter stable; changing the region's
first token off `{` un-hides the shim in one step and Cmd-Z restores it; pasting a region
with an unresolvable name flips to plain; pasting one with a resolvable name imports it and
stays wrapped.
