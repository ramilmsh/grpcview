// The host half of CollectionPanel's two-tier wiring: which collections must be on hand,
// how the filter box reaches a map of them, and the display-name collision check that
// panel-tree's pure `panelDropAllowed` deliberately leaves to the host. Its own module so
// it is testable — CollectionPanel pulls in `monaco-editor`, unresolvable under the sandboxed test run.
import type { CollectionSummary } from "@grpcview/v1/service_pb";
import {
  childPathOf,
  findByKey,
  itemKey,
  pruneNestedSelections,
  type ItemWithPath,
} from "@/lib/format";
import {
  panelDropAllowed,
  panelItems,
  panelNodeCollection,
  STATUS_SEPARATOR,
  type PanelNode,
} from "./panel-tree";

// The collections whose items the tree needs: the active one (always — its rows are the
// untiered tree, and its row auto-expands when tiered) plus every collection row the user
// has expanded. `treeExpanded` also holds ITEM keys, so it is intersected with the real
// collection ids rather than trusted. Sorted and deduped so the id list feeding
// useCollectionItems is stable across renders that changed nothing.
export const collectionsToQuery = (
  activeCollection: string | null,
  expanded: ReadonlySet<string>,
  collections: readonly CollectionSummary[]
): string[] => {
  const ids = new Set<string>();
  for (const c of collections) {
    if (c.id === activeCollection || expanded.has(c.id)) ids.add(c.id);
  }
  return [...ids].sort();
};

// The collection a row id belongs to, matched against the KNOWN collection ids rather
// than parsed out of the id: a collection id may itself contain slashes
// ("services/payments/requests"), so splitting a key on "/" would name the wrong thing.
// Every id in the tree is either a collection id, an item key (`<id>/<slug path>`) or a
// status id (`<id>` + NUL), so a prefix test against the real ids is exact. Decision 4's
// non-nesting invariant means no two can match, and the longest match is taken anyway.
export const collectionForNodeId = (
  id: string,
  collections: readonly CollectionSummary[]
): string | null => {
  let best: string | null = null;
  for (const c of collections) {
    const owns = id === c.id || id.startsWith(c.id + '/') || id.startsWith(c.id + STATUS_SEPARATOR);
    if (owns && (best === null || c.id.length > best.length)) best = c.id;
  }
  return best;
};

export const countRequests = (items: readonly ItemWithPath[]): number =>
  items.reduce(
    (n, it) =>
      it.item.content.case === "folder" ? n + countRequests(it.children ?? []) : n + 1,
    0
  );

// Every loaded collection's requests. A collection whose Get has not landed is absent from
// the map and so contributes nothing until it does.
export const countAllRequests = (
  itemsByCollection: ReadonlyMap<string, ItemWithPath[]>
): number => {
  let total = 0;
  for (const items of itemsByCollection.values()) total += countRequests(items);
  return total;
};

export const filterTree = (items: ItemWithPath[], q: string): ItemWithPath[] => {
  if (!q) return items;
  const lower = q.toLowerCase();
  const walk = (list: ItemWithPath[]): ItemWithPath[] => {
    const out: ItemWithPath[] = [];
    for (const it of list) {
      if (it.item.content.case === "folder") {
        const kids = walk(it.children ?? []);
        if (kids.length || it.item.name.toLowerCase().includes(lower)) {
          out.push({ ...it, children: kids });
        }
      } else if (it.item.name.toLowerCase().includes(lower)) {
        out.push(it);
      }
    }
    return out;
  };
  return walk(items);
};

// Prunes each collection's items to the filter box. Returns the SAME map for an empty
// query, because filterTree returns its input array unchanged there and a rebuilt map
// would differ from it only by identity — which is exactly what usePanelTreeAdapter
// memoizes on, so rebuilding it would cost the tree its node identity on every render.
export const filterItemsByCollection = (
  itemsByCollection: ReadonlyMap<string, ItemWithPath[]>,
  query: string
): ReadonlyMap<string, ItemWithPath[]> => {
  if (!query) return itemsByCollection;
  const out = new Map<string, ItemWithPath[]>();
  // A collection that filtered down to nothing stays PRESENT with an empty array: absent
  // means "its Get has not landed" (a Loading… row), which a filter must never claim.
  for (const [id, items] of itemsByCollection) out.set(id, filterTree(items, query));
  return out;
};

// Which collection a drop lands in. A destination row names it; the untiered root, whose
// parent is null, names none, so it is the collection the dragged items came from — which
// panelDropAllowed has already verified is the only one they are in.
export const panelDropCollection = (
  dragged: readonly ItemWithPath[],
  parent: PanelNode | null
): string | null =>
  parent !== null ? panelNodeCollection(parent) : dragged[0]?.collection ?? null;

// The item a drop's parent addresses, or null for a collection row and the untiered root —
// both of which are a collection's ROOT, which is `null` in item terms.
export const panelDropParentItem = (parent: PanelNode | null): ItemWithPath | null =>
  parent?.kind === "item" ? parent.item : null;

// The UNFILTERED siblings a drop would join, resolved out of the destination COLLECTION's
// items: the `children` the tree sees are pruned to whatever the filter box left visible,
// so a collision test against those would miss a hidden sibling the server will reject.
const destinationChildren = (
  parent: PanelNode | null,
  destination: string,
  itemsByCollection: ReadonlyMap<string, ItemWithPath[]>
): ItemWithPath[] => {
  const roots = itemsByCollection.get(destination) ?? [];
  const parentItem = panelDropParentItem(parent);
  if (parentItem === null) return roots;
  return findByKey(roots, itemKey(parentItem))?.children ?? [];
};

const samePath = (a: readonly string[], b: readonly string[]): boolean =>
  a.length === b.length && a.every((segment, i) => segment === b[i]);

// The panel's whole canDrop: panel-tree's structural rules, then the one thing they cannot
// see — a destination that already holds the same display name, whose children may be
// collapsed or filtered out. Collection.Move refuses a reparent onto an existing display
// name, so this mirrors that rule early. `itemsByCollection` must be the UNFILTERED map.
export const panelCanDrop = (
  dragged: readonly PanelNode[],
  to: { parent: PanelNode | null; before?: PanelNode },
  opts: { tiered: boolean; itemsByCollection: ReadonlyMap<string, ItemWithPath[]> }
): boolean => {
  if (!panelDropAllowed(dragged, to, { tiered: opts.tiered })) return false;
  const items = panelItems(dragged);
  const destination = panelDropCollection(items, to.parent);
  if (destination === null) return false;

  const newPath = childPathOf(panelDropParentItem(to.parent));
  const taken = new Set(
    destinationChildren(to.parent, destination, opts.itemsByCollection).map(
      (child) => child.item.name
    )
  );
  for (const node of pruneNestedSelections(items)) {
    if (samePath(node.path, newPath)) continue; // pure reorder in its own parent
    if (taken.has(node.item.name)) return false;
    taken.add(node.item.name);
  }
  return true;
};
