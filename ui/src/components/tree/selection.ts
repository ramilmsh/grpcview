import type { TreeRowModel } from "./types";

// Selection algebra against the FLATTENED visible-row array (tree-rewrite-plan.md's
// "Flat visible-rows model" — every behavior operates on the flat array, not
// recursion). Pure and DOM-free: no click/keyboard handling lives here, only the
// resulting-array math that T1 (keyboard) and T2 (multi-select wiring) call into.
// T0 itself only calls replaceSelection (plain click, see useTreeState.ts /
// Tree.tsx) — the other three are written now, per the plan's module layout
// listing this file at T0, but nothing invokes them yet.

// replaceSelection is a plain click: it discards whatever was selected before.
export const replaceSelection = (id: string): string[] => [id];

// toggleSelection is cmd/ctrl+click, adding or removing one id. Unlike
// rangeSelection/selectAll below, it takes no `rows` argument, so it has no way to
// know the current visible order and re-sort an addition into it — the best it can
// do is leave the untouched entries exactly where they were and append a newly
// added id at the end. That makes it "order-stable" in a weaker sense than the
// other three functions here: stable for what was already selected, not
// necessarily sorted to match visible-row order after an add.
export const toggleSelection = (current: readonly string[], id: string): string[] =>
  current.includes(id) ? current.filter((existing) => existing !== id) : [...current, id];

// rangeSelection is shift+click / shift+arrow: every row between the anchor (where
// the range started) and the focus (where it currently ends), inclusive of both, in
// ascending VISIBLE-row order regardless of which one comes first — that is what
// "either direction" means: shift+↑ from a low anchor selects the same rows
// shift+↓ would from a high one.
//
// Both endpoints are resolved by searching `rows`, not trusted as given, because a
// filtered-out row cannot be part of an on-screen selection:
//   - if `focusId` itself isn't in `rows`, there is nothing to select: [].
//   - if `anchorId` is null, or no longer in `rows` (its row was filtered out from
//     under it since the anchor was set), the range degenerates to just the focus
//     row — same result as "no anchor yet".
export const rangeSelection = <T>(
  rows: readonly TreeRowModel<T>[],
  anchorId: string | null,
  focusId: string
): string[] => {
  const focusIdx = rows.findIndex((r) => r.id === focusId);
  if (focusIdx === -1) return [];

  const anchorIdx = anchorId === null ? -1 : rows.findIndex((r) => r.id === anchorId);
  if (anchorIdx === -1) return [rows[focusIdx].id];

  const [lo, hi] = anchorIdx <= focusIdx ? [anchorIdx, focusIdx] : [focusIdx, anchorIdx];
  return rows.slice(lo, hi + 1).map((r) => r.id);
};

// selectAll is cmd/ctrl+A: every currently visible row, in visible-row order.
// "Visible" falls out for free — `rows` is already the flattened, filtered set, so
// collapsed subtrees and filtered-out items were never rows to begin with.
export const selectAll = <T>(rows: readonly TreeRowModel<T>[]): string[] =>
  rows.map((r) => r.id);
