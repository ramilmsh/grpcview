# Known bugs

Defects found by the codebase audit (`codebase-audit-plan.md`) but deliberately
**not** fixed by it — the audit's scope was reuse, naming and duplication, not
behavior. Each entry is written up because the fix is more than a few lines or
needs a design call; trivial fixes were just made.

Line numbers are against `1440d82` unless noted, so **re-verify an entry before acting on
it** — this file is not re-checked when the code moves. An entry is deleted when its bug is
fixed; `gv.invoke is unavailable on two of the three invoke paths` went that way on
2026-08-06 (`WithInvoker` now sits at the top of the shared `resolvePreSend`, and
`service/workspace/gvinvoke_test.go` covers the unary, streaming and saved paths).

---

## Metadata expressions don't get the bare-expression wrap that bodies get

**Impact:** an expression that works in the body editor fails in the metadata
editor, with no stated reason. The two editors look symmetrical and are not.

Bodies are wrapped so a bare expression (`{ id: 1 }`) evaluates as a value rather
than parsing as a statement; the metadata path does not apply the equivalent
wrap. See `AGENTS.md` §Request authoring model for the intended model — the doc
describes one rule and the code implements it in one of the two places.

**Fix:** apply the same wrapping to the metadata source. Confirm against
`ui/src/features/workspace/metadata-wrapper.ts` and its Go counterpart which
side is authoritative before changing either.

---

## `mdToStringMap` silently flattens multi-valued metadata

**Impact:** a request script that reads metadata sees only the first value of any
multi-valued key, and writing it back collapses the rest. Real gRPC metadata is
multi-valued (`set-cookie`, repeated custom keys), so this loses data on a wire
feature that exists.

`mdToStringMap` keeps only `vals[0]`. Request middleware therefore gets a
single-valued *view* of a multi-valued structure and writes it back
single-valued, so the flattening is not just a read limitation — it destroys the
other values on the round trip.

**Why it's a design call, not a patch.** The script-facing metadata type has to
change shape, and the choices interact with the metadata editor's evaluated
`{[k]: string[]}` contract (see `AGENTS.md` §Request authoring model, which
already models metadata as string *lists*). Options:

1. Expose `Record<string, string[]>` to scripts and break the current
   single-valued reads. Consistent with the editor contract; the honest model.
2. Keep `Record<string, string>` for reads, add a parallel multi-valued accessor.
   Backwards-compatible with existing scripts, two ways to do one thing.
3. Expose a small metadata object with `get`/`getAll`/`set`/`append`, mirroring
   the Fetch `Headers` API. Most familiar, most code.

**Recommend option 1** — pre-release, no users, and the editor already speaks
`string[]`, so this makes two halves of one feature agree instead of adding a
third shape. Found by G3; recorded rather than designed because it changes a
documented script-facing contract.

---

## The unary log emits `status` twice on an error

**Impact:** cosmetic but misleading in logs — one record carries two `status`
keys, and which one a log viewer shows is undefined.

`service/logging.go`'s unary path adds `status` once on the normal path and again
in the error branch. **Fix:** build the attribute list once and append `status`
exactly once before emitting.

---

## `Tree.tsx`'s drag comparator sorts a missing row to the front

**Impact:** during a multi-row drag, a row whose id is absent from the index is
ordered *first* instead of nowhere, so a drop can reorder items unexpectedly.

Four sites spell the missing-row lookup `flat.rows[flat.indexById.get(id) ?? -1]`.
The sort comparator inside `draggedNodes` (`ui/src/components/tree/Tree.tsx:293`)
uses `?? 0`. **Fix:** `?? -1`, matching its four siblings. The audit's `D-X2-33`
removes this line as a side effect of a different change, but the bug is
independent of whether that lands.

---

## `schemaSourceFor`'s fallback is unreachable

**Impact:** none at runtime — it is dead code that reads as a live compatibility
path, which is exactly what `AGENTS.md` §Project Stage says to delete on sight.

**Fix:** delete the fallback. Verify with a `grep` that no caller depends on the
branch's return value being distinguishable, then remove it.
