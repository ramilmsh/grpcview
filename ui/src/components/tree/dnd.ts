// Drag-and-drop DECISIONS: pointer position -> `into`/`before`/`after`, that zone
// -> a destination `{parent, before}`, validity, and the autoscroll step (the
// plan's own module table: docs/design/tree-rewrite-plan.md, `dnd.ts` —
// "pointer position → into/before/after, validity, autoscroll"). Pure and
// DOM-FREE, like every sibling decision module here (keymap.ts's "one table, no
// DOM", navigate.ts's "pure, DOM-free", dispatch.ts's header): only ./flatten
// and ./navigate cross the import boundary. Tree.tsx does the DOM half —
// measuring a row's box, reading the pointer, scrolling the scrollport — and
// hands plain numbers and ids in here.
//
// Everything below speaks IDS, never nodes. That is what keeps this file free of
// TreeAdapter and of `T` beyond the FlatTree<T> it indexes: Tree.tsx resolves an
// id back to a node exactly once, at the boundary where `onMove`/`canDrop` need
// the real `T` (its own `nodeFor` helper, already used by dispatch.ts's
// open/delete actions for the same reason). A resolution expressed as
// `{parentId, beforeId}` is also directly comparable to a row's own
// `parentId` — which is what makes the no-op test below one field comparison
// rather than an adapter-mediated identity check.

import type { FlatTree } from "./flatten";
import { descendantIds } from "./navigate";

// Which side of a row the pointer is asking to drop on. `into` means "make it a
// child of this row" and is only ever produced for an EXPANDABLE row; `before`/
// `after` mean "put it between rows, on this side of this one". Structurally a
// subset of TreeRowState.dropTarget ("into" | "before" | "after" | null, types.ts),
// which is that same vocabulary plus the "no drop here" case Tree.tsx carries as
// `null`.
export type DropZone = "before" | "into" | "after";

// The DESTINATION a zone resolves to, in id space. `parentId: null` is the
// collection root (which `childPathOf(null)` already maps to MoveItemRequest's
// empty `new_path` — see CollectionPanel's onMove); `beforeId: null` is
// MoveItemRequest's unset `before`, i.e. append to the end of that parent.
export interface DropResolution {
  parentId: string | null;
  beforeId: string | null;
  // The DEPTH the dropped rows would render at — i.e. how far to indent a
  // between-rows indicator line. This is the whole informational content of such
  // a line: a full-width line at a row boundary cannot say WHICH parent the item
  // lands in, and the two candidates at any boundary (last child of the folder
  // above vs. next sibling of that folder) differ only by indent. Carried on the
  // resolution rather than recomputed by the renderer because it is derived from
  // the same parent lookup that produced parentId.
  depth: number;
}

function clamp(value: number, lo: number, hi: number): number {
  return Math.min(hi, Math.max(lo, value));
}

// Where in a row's height the two/three drop zones sit — the one piece of this
// phase worth being literally faithful about, since it decides whether
// reordering AROUND a folder is reachable by pointer at all.
//
// What the vendored widget does (ui/node_modules/monaco-editor/esm/vs/base/
// browser/ui/list/listView.js:888-892, `getTargetSector`):
//
//     const relativePosition = browserEvent.offsetY / this.items[targetIndex].size;
//     const sector = Math.floor(relativePosition / 0.25);
//     return clamp(sector, 0, 3);
//
// so the row is cut into four QUARTERS, 0..3, floored (an exact quarter boundary
// belongs to the LOWER sector) and clamped for a pointer outside the row's box.
// The widget stops there: `sector` is handed to the caller's `dnd.onDragOver`
// delegate (abstractTree.js:63-64) and the mapping from sector to
// before/into/after lives in the workbench's own file-explorer delegate, which
// is NOT vendored here. So the quartering is verified from source; the mapping
// below is our reading of it, and it is the obvious one: the outer quarters are
// the "between rows" bands and the middle half is "into".
//
// A LEAF has no "into" — there is nothing to put a request inside — so it splits
// in HALF instead of into quarters. Written as its own branch rather than by
// folding sector 1/2 into before/after, because "the top half means before" and
// "the top quarter means before" are different behaviors and the leaf case is the
// former: a leaf's whole box is a between-rows target, which is what makes
// dropping between two requests easy to hit.
//
// Exact boundaries, all decided by the floor above (and asserted in dnd.test.ts):
// on a 22px expandable row, offsetY 5.5 (a quarter) is `into`, 11 (the midpoint)
// is `into`, 16.5 (three quarters) is `after`; on a 22px leaf, offsetY 11 is
// `after` — the midpoint belongs to the lower half, matching the floor's
// treatment of a quarter boundary rather than special-casing it.
export function zoneForOffset(input: {
  offsetY: number;
  rowHeight: number;
  expandable: boolean;
}): DropZone {
  const { offsetY, rowHeight, expandable } = input;
  // A zero/negative height cannot be divided into anything meaningful (a row that
  // has not laid out yet, or a caller passing a bogus rowHeight): treat the whole
  // row as `before`, the least destructive of the three — a between-rows drop at
  // a known boundary rather than a reparent into a row whose extent is unknown.
  if (rowHeight <= 0) return "before";
  if (!expandable) return offsetY < rowHeight / 2 ? "before" : "after";
  const sector = clamp(Math.floor(offsetY / rowHeight / 0.25), 0, 3);
  if (sector === 0) return "before";
  if (sector === 3) return "after";
  return "into";
}

// The next VISIBLE SIBLING of the row at `index` — the row after it that shares
// its parentId — or null if it is the last visible child of its parent.
//
// NOT simply `rows[index + 1]`: the next row in the flat array is only a sibling
// when the target is a leaf or a collapsed folder. For an EXPANDED folder the
// next row is its own first child, and for the last child of a nested folder the
// next row is something at a shallower depth entirely. Both would make an
// "insert before the next sibling" resolution place the item in the wrong parent.
//
// Implemented as one forward scan that skips the target's own DESCENDANTS — the
// contiguous run of deeper rows immediately after it, exactly the run
// navigate.ts's descendantIds walks — and then tests parentId on the first row
// back at or above the target's own depth. The parentId test, not the depth
// arrival, is what decides: a row at the target's depth whose parentId differs is
// not a sibling (it cannot happen while flatten() increments depth by one per
// hop, but it is the assumption T7's compact-folders flag is free to break, and
// descendantIds carries the same caveat for the same reason).
export function nextVisibleSiblingId<T>(flat: FlatTree<T>, index: number): string | null {
  const row = flat.rows[index];
  if (row === undefined) return null;
  for (let i = index + 1; i < flat.rows.length; i++) {
    const candidate = flat.rows[i];
    if (candidate.depth > row.depth) continue; // inside the target's own subtree
    return candidate.parentId === row.parentId ? candidate.id : null;
  }
  return null;
}

// The depth rows drop at under `parentId` — one deeper than the parent's own row,
// or 0 at the root. Also the indent of a between-rows indicator (see
// DropResolution.depth).
function depthUnder<T>(flat: FlatTree<T>, parentId: string | null): number {
  if (parentId === null) return 0;
  const parentRow = flat.rows[flat.indexById.get(parentId) ?? -1];
  return parentRow === undefined ? 0 : parentRow.depth + 1;
}

// A zone on a row -> the destination it means. Pure function of the flat array,
// per the plan's enduring decision 3 (every behavior operates on array indices).
//
// THE AMBIGUOUS CASE, decided here: `after` an EXPANDED FOLDER row. Two readings
// are available — "the folder's next sibling" (treat the folder as one opaque
// row) and "position 0 inside the folder" — and this file picks the SECOND. The
// reason is that the indicator is drawn at a y-position, and at that particular
// boundary the very next row on screen is the folder's own first child: a line
// there, indented one level in, is unambiguously "in front of that child". The
// alternative would draw a line at the same pixel row meaning something the pixels
// contradict, since the folder's real next sibling may be many rows further down
// with its whole subtree in between. Resolving it as position 0 inside also keeps
// the folder's bottom quarter useful rather than making it a duplicate of `into`
// (which appends at the END inside the folder) — the two together give "first
// child" and "last child" from two adjacent bands. An empty (or filtered-empty)
// expanded folder has no first child to go before, so it degrades to append
// inside — still "inside", which is what the pointer was over.
export function resolveDrop<T>(
  flat: FlatTree<T>,
  targetIndex: number,
  zone: DropZone
): DropResolution | null {
  const row = flat.rows[targetIndex];
  if (row === undefined) return null;

  if (zone === "into") {
    // Only ever produced for an expandable row (zoneForOffset above), but the
    // guard is cheap and this function is public: a caller passing "into" for a
    // leaf is asking for something that does not exist, not for a fallback.
    if (!row.expandable) return null;
    return { parentId: row.id, beforeId: null, depth: row.depth + 1 };
  }

  if (zone === "after" && row.expandable && row.expanded) {
    const firstChild = flat.rows[targetIndex + 1];
    const beforeId = firstChild?.parentId === row.id ? firstChild.id : null;
    return { parentId: row.id, beforeId, depth: row.depth + 1 };
  }

  const parentId = row.parentId;
  const beforeId = zone === "before" ? row.id : nextVisibleSiblingId(flat, targetIndex);
  return { parentId, beforeId, depth: depthUnder(flat, parentId) };
}

// Every id the dragged set OWNS — each dragged row plus its whole visible
// subtree. This is the into-own-descendant check the plan's UX spec asks for
// ("reject drops into a dragged node's own descendant"), and it reuses
// navigate.ts's descendantIds rather than re-deriving the subtree walk: that
// function already reads the contiguous deeper-row run off the array and is
// already unit-tested. Only VISIBLE descendants can appear here, which is
// sufficient by construction — a row inside a COLLAPSED dragged folder is not a
// row at all, so it can never be a drop target.
export function draggedSubtreeIds<T>(
  flat: FlatTree<T>,
  draggedIds: readonly string[]
): Set<string> {
  const owned = new Set<string>();
  for (const id of draggedIds) {
    owned.add(id);
    for (const descendant of descendantIds(flat, id)) owned.add(descendant);
  }
  return owned;
}

// Whether a resolution would leave the dragged item exactly where it already is —
// worth rejecting rather than firing, because MoveItem would happily accept it
// (same parent, same position: a reorder that reorders nothing) and the UI would
// show a drop indicator promising a change that does not happen.
//
// Only defined for a SINGLE dragged row. For a multi-row drag there is no
// position that is a no-op for every member at once: the moves reinsert the set
// contiguously at the destination, which reorders the set relative to whatever
// used to sit between its members, so "unchanged" is not a state the gesture can
// land in. Returning false for those lets the drop through rather than inventing
// a rule.
//
// Three ways one row's drop is a no-op, all in the row's CURRENT parent:
//   - insert before ITSELF
//   - insert before its own next visible sibling (which is where it already sits)
//   - append, when it is already the last visible child (both are beforeId null)
export function isNoOpDrop<T>(
  flat: FlatTree<T>,
  draggedIds: readonly string[],
  res: DropResolution
): boolean {
  if (draggedIds.length !== 1) return false;
  const index = flat.indexById.get(draggedIds[0]);
  if (index === undefined) return false;
  const row = flat.rows[index];
  if (row.parentId !== res.parentId) return false; // a reparent is never a no-op
  if (res.beforeId === row.id) return true;
  return res.beforeId === nextVisibleSiblingId(flat, index);
}

// The one function Tree.tsx calls per `dragover`: a target row + the zone the
// pointer is in + the dragged set -> the destination, or `null` for "this is not
// a legal drop". A null answer is what makes Tree.tsx skip preventDefault(),
// which is what gives the user the browser's own no-drop cursor for free.
//
// Rejects exactly what is STRUCTURALLY impossible or pointless, which is all the
// tree can know. Anything requiring domain knowledge — a destination that
// already holds an item with the same display name, say, which the tree cannot
// see because those children may be collapsed or filtered out — is the host's
// `canDrop` (types.ts), consulted by Tree.tsx after this returns non-null.
//
// The "target row is inside the dragged set" test SUBSUMES the corresponding test
// on the resolved parentId: `into` resolves parentId to the target row's own id,
// and `before`/`after` resolve it to the target's parentId — and descendantIds is
// transitive, so a row whose parent is owned by the drag is itself owned. One
// membership test is enough; a second would only look reassuring.
export function dropTargetAt<T>(
  flat: FlatTree<T>,
  targetIndex: number,
  zone: DropZone,
  draggedIds: readonly string[]
): DropResolution | null {
  if (draggedIds.length === 0) return null;
  const row = flat.rows[targetIndex];
  if (row === undefined) return null;
  if (draggedSubtreeIds(flat, draggedIds).has(row.id)) return null;
  const res = resolveDrop(flat, targetIndex, zone);
  if (res === null) return null;
  if (isNoOpDrop(flat, draggedIds, res)) return null;
  return res;
}

// AUTOSCROLL. How close to a scrollport edge the pointer has to get before the
// list starts moving under it, and the most one dragover may scroll by.
export const AUTOSCROLL_EDGE = 24;
export const AUTOSCROLL_MAX_STEP = 28;

// How far to scroll the scrollport (px; negative = up) for a pointer at
// `pointerY` inside a scrollport spanning `top`..`bottom` in the same coordinate
// space (Tree.tsx passes clientY and a getBoundingClientRect()). 0 means "leave
// it alone", which is the answer everywhere except within AUTOSCROLL_EDGE of
// either edge.
//
// Same SHAPE as the vendored widget's own
// `animateDragAndDropScrollTop` (listView.js:865-876):
//
//     const diff = this.dragOverMouseY - viewTop;
//     const upperLimit = this.renderHeight - 35;
//     if (diff < 35)             this.scrollTop += Math.max(-14, Math.floor(0.3 * (diff - 35)));
//     else if (diff > upperLimit) this.scrollTop += Math.min(14, Math.floor(0.3 * (diff - upperLimit)));
//
// — a proportional step, faster the deeper the pointer is into the edge band,
// clamped so a pointer far outside the box cannot jump the list. Two deliberate
// differences, both consequences of WHEN this runs:
//
//   - The widget calls that from an rAF loop (`setupDragAndDropScrollTopAnimation`,
//     :849-861) so it fires ~60x/s from a stored pointer position, which is why
//     its per-call step is small (0.3 slope, 14px cap). Ours runs once per
//     `dragover` — the HTML drag-and-drop processing model fires that on movement
//     and, per spec, at least every 350ms even for a stationary pointer — so the
//     call rate is both lower and variable, and the per-call step is
//     correspondingly larger (slope 1, i.e. "scroll by exactly how far the pointer
//     has pushed into the band"). Chosen over an rAF loop deliberately: rAF never
//     fires in the automated browser harness (plan §"Verification recipe", "Two
//     traps in the browser harness itself"), and an event-driven step needs no
//     teardown at all — there is no timer to leak if a drop, a dragend, or an
//     unmount is missed.
//   - The band is 24px rather than 35, because our scrollport is a 278px-wide
//     sidebar showing ~22px rows: a 35px band would arm autoscroll a row and a
//     half in from each edge, where the user is still legitimately aiming at rows.
export function autoScrollDelta(pointerY: number, top: number, bottom: number): number {
  const diff = pointerY - top;
  const upperLimit = bottom - top - AUTOSCROLL_EDGE;
  if (diff < AUTOSCROLL_EDGE) {
    return Math.max(-AUTOSCROLL_MAX_STEP, Math.floor(diff - AUTOSCROLL_EDGE));
  }
  if (diff > upperLimit) {
    return Math.min(AUTOSCROLL_MAX_STEP, Math.floor(diff - upperLimit));
  }
  return 0;
}
