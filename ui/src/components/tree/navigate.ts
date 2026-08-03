import type { FlatTree } from "./flatten";

export type MoveTarget = "up" | "down" | "first" | "last" | "pageUp" | "pageDown";

function clamp(value: number, lo: number, hi: number): number {
  return Math.min(hi, Math.max(lo, value));
}

function pageStride(rowsPerPage: number): number {
  return Math.max(1, rowsPerPage);
}

function startIndex(fromIndex: number, direction: "down" | "up", rowCount: number): number {
  if (fromIndex !== -1) return fromIndex;
  return direction === "down" ? -1 : rowCount;
}

// Where a move lands, or null for an empty tree. A null or unknown fromId starts
// from just before the first row going down, just after the last going up.
export function targetIndex<T>(
  flat: FlatTree<T>,
  fromId: string | null,
  to: MoveTarget,
  rowsPerPage: number
): number | null {
  const rowCount = flat.rows.length;
  if (rowCount === 0) return null;
  const lastIndex = rowCount - 1;
  const fromIndex = fromId === null ? -1 : (flat.indexById.get(fromId) ?? -1);

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

// null when `id` is a root or isn't a currently visible row.
export function parentIndex<T>(flat: FlatTree<T>, id: string): number | null {
  const rowIndex = flat.indexById.get(id);
  if (rowIndex === undefined) return null;
  const parentId = flat.rows[rowIndex].parentId;
  if (parentId === null) return null;
  return flat.indexById.get(parentId) ?? null;
}

// Every visible row strictly beneath `id`, in row order — the ids a collapse of
// `id` would hide. Read off the contiguous deeper-row run, which stops being
// equivalent once compactFolders draws several hops at one depth.
export function descendantIds<T>(flat: FlatTree<T>, id: string): string[] {
  const rowIndex = flat.indexById.get(id);
  if (rowIndex === undefined) return [];
  const baseDepth = flat.rows[rowIndex].depth;

  const out: string[] = [];
  for (let i = rowIndex + 1; i < flat.rows.length; i++) {
    if (flat.rows[i].depth <= baseDepth) break;
    out.push(flat.rows[i].id);
  }
  return out;
}

// The row after `id`, iff that row is its child.
export function firstChildIndex<T>(flat: FlatTree<T>, id: string): number | null {
  const rowIndex = flat.indexById.get(id);
  if (rowIndex === undefined) return null;
  const nextRow = flat.rows[rowIndex + 1];
  return nextRow !== undefined && nextRow.parentId === id ? rowIndex + 1 : null;
}
