// The pure half of QuickOpen (⌘K): every request in a collection, fuzzy-filtered, plus the
// tree keys that must be expanded to show the one opened.
import { itemKey, type ItemWithPath } from "@/lib/format";
import { fuzzyHits } from "./method-dropdown";

export interface QuickOpenRow {
  item: ItemWithPath;
  key: string;
  // Display-name folder path, "" at the root.
  folder: string;
  // Matched character indices within the request name only.
  hits: number[];
}

// Requests in tree order (depth first).
export function flattenRequests(
  roots: readonly ItemWithPath[],
): ItemWithPath[] {
  const out: ItemWithPath[] = [];
  const walk = (items: readonly ItemWithPath[]) => {
    for (const it of items) {
      if (it.item.content.case === "request") out.push(it);
      else if (it.children) walk(it.children);
    }
  };
  walk(roots);
  return out;
}

// Name matches first, then matches that need the folder path ("orders/list"); tree order
// within each. An empty query lists everything.
export function filterRequests(
  requests: readonly ItemWithPath[],
  query: string,
): QuickOpenRow[] {
  const q = query.trim();
  const byName: QuickOpenRow[] = [];
  const byPath: QuickOpenRow[] = [];
  for (const item of requests) {
    const name = item.item.name;
    const folder = item.path.join(" / ");
    const row = { item, key: itemKey(item), folder };
    const hits = fuzzyHits(name, q);
    if (hits !== null) {
      byName.push({ ...row, hits });
      continue;
    }
    const prefix = item.path.length ? `${item.path.join("/")}/` : "";
    const full = fuzzyHits(prefix + name, q);
    if (full === null) continue;
    byPath.push({
      ...row,
      hits: full
        .filter((i) => i >= prefix.length)
        .map((i) => i - prefix.length),
    });
  }
  return [...byName, ...byPath];
}

// The tree rows to expand so `item` is visible: its collection (tiered tree only) and
// every ancestor folder.
export function ancestorKeys(item: ItemWithPath, tiered: boolean): string[] {
  const out = tiered ? [item.collection] : [];
  for (let i = 1; i <= item.slugPath.length; i++)
    out.push([item.collection, ...item.slugPath.slice(0, i)].join("/"));
  return out;
}
