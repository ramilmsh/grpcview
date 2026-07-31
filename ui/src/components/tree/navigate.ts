// The array math for a "move" intent (keymap.ts's TreeIntent), plus the two
// structural lookups ("collapseOrParent" / "expandOrFirstChild" need) once that
// intent reaches a real tree — pure, DOM-free, and keyed entirely off a
// FlatTree<T> (flatten.ts's flat visible-rows model: tree-rewrite-plan.md's
// "Enduring decisions" #3, "every behavior operates on array indices, not
// recursion"). Not its own row in the plan's module table, but it earns its own
// file anyway rather than folding into a neighbor, because neither neighbor is
// the right home for it: keymap.ts has to stay a flat TABLE with no notion of
// rows or indices (see that file's header comment), and Tree.tsx is wiring, not
// arithmetic (the plan's own words: "Tree.tsx — the component: roving tabindex,
// aria, event wiring"). Splitting this out is what makes the arithmetic itself
// unit-testable (navigate.test.ts) independent of both.
//
// Deliberately does NOT import keymap.ts, or vice versa: MoveTarget below is its
// OWN type, not a re-export of TreeIntent's "move" variant's `to` field, even
// though the two are structurally identical unions today. Tree.tsx (the next
// phase) bridges them by passing a "move" intent's `to` straight into
// `targetIndex` — TypeScript accepts this with no cast, since the two literal
// unions are structurally the same type — but keeping the declarations
// independent means neither module has to import, or even know of, the other.

import type { FlatTree } from "./flatten";

export type MoveTarget = "up" | "down" | "first" | "last" | "pageUp" | "pageDown";

function clamp(value: number, lo: number, hi: number): number {
  return Math.min(hi, Math.max(lo, value));
}

// A page never advances by fewer than one row, whatever the caller passes:
// rowsPerPage is a caller-computed ESTIMATE (real viewport height / row height,
// computed once Tree.tsx exists) that can legitimately be 0 (nothing measured
// yet, e.g. before first layout) or 1 (a very short viewport) — treating either
// as "move by zero rows" would make PageUp/PageDown a dead key exactly when the
// viewport is smallest, which is the opposite of what paging is for.
function pageStride(rowsPerPage: number): number {
  return Math.max(1, rowsPerPage);
}

// A null OR unknown fromId — no row currently focused, or a focused id whose row
// just disappeared (hidden by a collapse or a filter) — is treated identically,
// as fromIndex === -1: this resolves it to a VIRTUAL starting position just
// BEFORE the first row for a downward move, or just AFTER the last row for an
// upward one. Applying the ordinary step arithmetic from that position is what
// turns a plain "down" into "the first row" and a plain "up" into "the last row"
// per the contract ("A null/unknown fromId starts from the first row for down
// and the last for up") — and because pageDown/pageUp share that same step
// arithmetic below (just a bigger step), the rule extends to them for free: a
// page move with nothing focused lands `stride` rows in from whichever end it
// starts at, rather than collapsing to the very first/last row the way a
// single-row move does.
function startIndex(fromIndex: number, direction: "down" | "up", rowCount: number): number {
  if (fromIndex !== -1) return fromIndex;
  return direction === "down" ? -1 : rowCount;
}

export function targetIndex<T>(
  flat: FlatTree<T>,
  fromId: string | null,
  to: MoveTarget,
  rowsPerPage: number
): number | null {
  const rowCount = flat.rows.length;
  if (rowCount === 0) return null; // empty tree -> null everywhere, incl. first/last
  const lastIndex = rowCount - 1;
  const fromIndex = fromId === null ? -1 : (flat.indexById.get(fromId) ?? -1);

  // The four directional cases below (down/up/pageDown/pageUp) all CLAMP at
  // rowCount's edges rather than wrapping: VS Code's list does not wrap on
  // plain arrow keys (wrap-around is typeahead's behavior, T3). Wrapping would
  // be a modulo here instead of a clamp; deliberately isn't one.
  switch (to) {
    case "first":
      return 0;
    case "last":
      return lastIndex;
    case "down":
      return clamp(startIndex(fromIndex, "down", rowCount) + 1, 0, lastIndex);
    case "up":
      return clamp(startIndex(fromIndex, "up", rowCount) - 1, 0, lastIndex);
    case "pageDown":
      return clamp(startIndex(fromIndex, "down", rowCount) + pageStride(rowsPerPage), 0, lastIndex);
    case "pageUp":
      return clamp(startIndex(fromIndex, "up", rowCount) - pageStride(rowsPerPage), 0, lastIndex);
  }
}

// The row ArrowLeft focuses when the current row is either already collapsed or
// a leaf (an already-EXPANDED folder just collapses in place instead — a
// same-row state change Tree.tsx makes directly, never reaching this function).
// null when `id` has no parent (it's a root) or isn't a currently visible row at
// all.
export function parentIndex<T>(flat: FlatTree<T>, id: string): number | null {
  const rowIndex = flat.indexById.get(id);
  if (rowIndex === undefined) return null; // id isn't a currently visible row
  const parentId = flat.rows[rowIndex].parentId;
  if (parentId === null) return null; // a root has no parent row to focus

  // A visible child's parent is always visible too: flatten() only ever
  // descends into an EXPANDED node (flatten.ts), so a node is pushed as a row at
  // all only once its own parent already was. The `?? null` below is defensive
  // rather than load-bearing — it protects a FlatTree assembled some other way
  // (or a future bug in flatten()) from throwing on an arrow-key press, which is
  // a worse failure mode for keyboard navigation than silently doing nothing.
  return flat.indexById.get(parentId) ?? null;
}

// Every VISIBLE row strictly beneath `id`, in row order — the ids a collapse of
// `id` is about to hide. Added for the twistie-collapse rebase (dispatch.ts's
// applyTwistieClick): collapsing a folder from its twistie deliberately touches
// neither focus nor selection, so without this the cursor and any selected rows
// could end up naming rows that are no longer painted anywhere — an
// aria-activedescendant pointing at nothing, and a Delete acting on rows the
// user cannot see.
//
// "Strictly beneath" is read off the ARRAY, not by recursing the adapter:
// flatten() emits a node's whole visible subtree as the contiguous run
// immediately after it (flatten.ts's visit() pushes a row, then descends), and
// every row in that run is drawn deeper than the folder itself — so the run
// ends at the first row whose depth is back at or above the folder's own. One
// forward scan, no set membership, no second pass.
//
// This is the one place in this file that DOES key off `depth` rather than
// parentId, in deliberate contrast to firstChildIndex below (see its comment
// for why it refuses to). The honest reason for the asymmetry: firstChildIndex
// asks a question about ONE parent-child hop, which parentId answers exactly,
// while this asks about a transitive closure, which parentId answers only by
// accumulating a set as it walks. Depth is the cheaper and — for a contiguous
// run — equivalent formulation TODAY. It stops being equivalent the moment
// T7's compact-folders flag draws several logical hops at one depth; whoever
// turns that flag on has to revisit this function (a parentId-closure walk over
// the same contiguous run is the drop-in replacement), which is why the
// dependency is spelled out here rather than left implicit.
//
// An `id` that names no current row returns [] — the same "not a visible row,
// nothing to say about it" answer parentIndex/firstChildIndex already give.
export function descendantIds<T>(flat: FlatTree<T>, id: string): string[] {
  const rowIndex = flat.indexById.get(id);
  if (rowIndex === undefined) return [];
  const baseDepth = flat.rows[rowIndex].depth;

  const out: string[] = [];
  for (let i = rowIndex + 1; i < flat.rows.length; i++) {
    if (flat.rows[i].depth <= baseDepth) break; // back out to a sibling/ancestor: run over
    out.push(flat.rows[i].id);
  }
  return out;
}

// The row ArrowRight focuses when the current row is already an EXPANDED folder
// (a collapsed folder just expands in place instead, another same-row change
// Tree.tsx makes directly). Exactly "the row after it, iff that row is its
// child" — nothing here walks depth or recurses.
export function firstChildIndex<T>(flat: FlatTree<T>, id: string): number | null {
  const rowIndex = flat.indexById.get(id);
  if (rowIndex === undefined) return null; // id isn't a currently visible row
  const nextRow = flat.rows[rowIndex + 1];

  // Consulting parentId directly — NOT "is the next row's depth === this row's
  // depth + 1" — is what makes this correct for a COLLAPSED or childless row,
  // not just a simpler way to write it: flatten() never visits a collapsed
  // node's children, so nothing in `rows` ever carries that node's id as a
  // parentId, and whatever row happens to follow it — a sibling, or a backtrack
  // target further up — is never its child, regardless of what depth that row
  // is drawn at. A depth-only check happens to AGREE with this today, because
  // T0's flatten() always increments depth by exactly one per parent-child hop
  // — but "happens to agree today" is exactly the assumption T7's
  // compact-folders flag is free to break (a chain of single-child folders
  // drawn as one row, at one depth, for several logical hops). Reading parentId
  // asks the real relationship directly instead of relying on a layout artifact
  // that merely implies it for now.
  return nextRow !== undefined && nextRow.parentId === id ? rowIndex + 1 : null;
}
