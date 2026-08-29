import type { TreeRowModel } from "./types";

// Selection algebra over the flattened visible rows. Pure and DOM-free.

// replaceSelection is a plain click.
export const replaceSelection = (id: string): string[] => [id];

// toggleSelection is cmd/ctrl+click. Order-stable for existing entries; an add appends.
export const toggleSelection = (
  current: readonly string[],
  id: string,
): string[] =>
  current.includes(id)
    ? current.filter((existing) => existing !== id)
    : [...current, id];

// rangeSelection is shift+click / shift+arrow: anchor..focus inclusive, in visible-row
// order, in either direction. Endpoints not in `rows` are treated as absent.
export const rangeSelection = <T>(
  rows: readonly TreeRowModel<T>[],
  anchorId: string | null,
  focusId: string,
): string[] => {
  const focusIdx = rows.findIndex((r) => r.id === focusId);
  if (focusIdx === -1) return [];

  const anchorIdx =
    anchorId === null ? -1 : rows.findIndex((r) => r.id === anchorId);
  if (anchorIdx === -1) return [rows[focusIdx].id];

  const [lo, hi] =
    anchorIdx <= focusIdx ? [anchorIdx, focusIdx] : [focusIdx, anchorIdx];
  return rows.slice(lo, hi + 1).map((r) => r.id);
};

// selectAll is cmd/ctrl+A: every currently visible row.
export const selectAll = <T>(rows: readonly TreeRowModel<T>[]): string[] =>
  rows.map((r) => r.id);
