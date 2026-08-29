// Drag-and-drop decisions in id space: pointer position -> zone -> destination,
// validity, autoscroll step. Pure and DOM-free; Tree.tsx does the measuring.

import type { FlatTree } from "./flatten";
import { descendantIds } from "./navigate";

// `into` is only ever produced for an expandable row.
export type DropZone = "before" | "into" | "after";

export interface DropResolution {
  parentId: string | null; // null = the collection root
  beforeId: string | null; // null = append to the end of that parent
  // The depth the dropped rows would render at, i.e. the indent of a between-rows
  // indicator line — the only thing such a line can say about the parent.
  depth: number;
}

function clamp(value: number, lo: number, hi: number): number {
  return Math.min(hi, Math.max(lo, value));
}

// An expandable row splits into quarters (before / into / into / after), a leaf
// into halves, with an exact boundary belonging to the lower band.
export function zoneForOffset(input: {
  offsetY: number;
  rowHeight: number;
  expandable: boolean;
}): DropZone {
  const { offsetY, rowHeight, expandable } = input;
  if (rowHeight <= 0) return "before";
  if (!expandable) return offsetY < rowHeight / 2 ? "before" : "after";
  const sector = clamp(Math.floor(offsetY / rowHeight / 0.25), 0, 3);
  if (sector === 0) return "before";
  if (sector === 3) return "after";
  return "into";
}

// The next visible row sharing `index`'s parentId, or null if it is the last
// visible child. Not `rows[index + 1]`: that is a child for an expanded folder.
export function nextVisibleSiblingId<T>(
  flat: FlatTree<T>,
  index: number,
): string | null {
  const row = flat.rows[index];
  if (row === undefined) return null;
  for (let i = index + 1; i < flat.rows.length; i++) {
    const candidate = flat.rows[i];
    if (candidate.depth > row.depth) continue;
    return candidate.parentId === row.parentId ? candidate.id : null;
  }
  return null;
}

function depthUnder<T>(flat: FlatTree<T>, parentId: string | null): number {
  if (parentId === null) return 0;
  const parentRow = flat.rows[flat.indexById.get(parentId) ?? -1];
  return parentRow === undefined ? 0 : parentRow.depth + 1;
}

// `after` an EXPANDED folder deliberately resolves to position 0 INSIDE it, since
// the next row on screen is that folder's own first child.
export function resolveDrop<T>(
  flat: FlatTree<T>,
  targetIndex: number,
  zone: DropZone,
): DropResolution | null {
  const row = flat.rows[targetIndex];
  if (row === undefined) return null;

  if (zone === "into") {
    if (!row.expandable) return null;
    return { parentId: row.id, beforeId: null, depth: row.depth + 1 };
  }

  if (zone === "after" && row.expandable && row.expanded) {
    const firstChild = flat.rows[targetIndex + 1];
    const beforeId = firstChild?.parentId === row.id ? firstChild.id : null;
    return { parentId: row.id, beforeId, depth: row.depth + 1 };
  }

  const parentId = row.parentId;
  const beforeId =
    zone === "before" ? row.id : nextVisibleSiblingId(flat, targetIndex);
  return { parentId, beforeId, depth: depthUnder(flat, parentId) };
}

// Every id the dragged set owns — each dragged row plus its visible subtree.
export function draggedSubtreeIds<T>(
  flat: FlatTree<T>,
  draggedIds: readonly string[],
): Set<string> {
  const owned = new Set<string>();
  for (const id of draggedIds) {
    owned.add(id);
    for (const descendant of descendantIds(flat, id)) owned.add(descendant);
  }
  return owned;
}

// Only defined for a single dragged row: a multi-row drag has no position that is
// a no-op for all of them at once.
export function isNoOpDrop<T>(
  flat: FlatTree<T>,
  draggedIds: readonly string[],
  res: DropResolution,
): boolean {
  if (draggedIds.length !== 1) return false;
  const index = flat.indexById.get(draggedIds[0]);
  if (index === undefined) return false;
  const row = flat.rows[index];
  if (row.parentId !== res.parentId) return false;
  if (res.beforeId === row.id) return true;
  return res.beforeId === nextVisibleSiblingId(flat, index);
}

// The one function Tree.tsx calls per `dragover`. A null answer makes it skip
// preventDefault(), which is what gives the native no-drop cursor.
export function dropTargetAt<T>(
  flat: FlatTree<T>,
  targetIndex: number,
  zone: DropZone,
  draggedIds: readonly string[],
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

export const AUTOSCROLL_EDGE = 24;
export const AUTOSCROLL_MAX_STEP = 28;

// How far to scroll (px; negative = up) for a pointer at `pointerY` in a
// scrollport spanning `top`..`bottom`. Stepped per `dragover` rather than from an
// rAF loop, which never fires in the automated browser harness.
export function autoScrollDelta(
  pointerY: number,
  top: number,
  bottom: number,
): number {
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
