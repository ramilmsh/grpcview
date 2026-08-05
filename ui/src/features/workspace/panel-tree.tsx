import { useMemo, type ReactNode } from "react";
import type { CollectionSummary } from "@grpcview/v1/service_pb";
import type { TreeAdapter, TreeItemLike, TreeRowState } from "@/components/tree/types";
import { itemKey, type ItemWithPath } from "@/lib/format";
import { renderRequestRow, requestTreeItem, type RequestRowCallbacks } from "./request-tree";

// The request panel's tree spans two tiers: the collections in the workspace, and the
// items inside one. A collection row appears only when there is more than one — with a
// single collection the panel looks exactly as it did before, its name in the header.
export type PanelNode =
  | { kind: "collection"; collection: CollectionSummary }
  | { kind: "item"; item: ItemWithPath }
  // A collection whose Get has not landed (or failed, or came back empty): the host
  // resolves that promise and re-renders, so the tree only ever sees a synchronous row.
  | { kind: "status"; collection: string; label: string };

// Joins a status row's id to its collection. Exported so the panel can TEST for it when
// resolving which collection a row id belongs to, rather than spelling the byte twice.
export const STATUS_SEPARATOR = "\u0000";

export const LOADING_LABEL = "Loading…";
export const EMPTY_LABEL = "No requests yet";

// Ids must be unique across the whole flatten pass (flatten.ts:39-47 throws on a
// duplicate), and the three tiers cannot collide:
//   - An item key is `<collection id>/<slug path>` (format.ts), so it carries at least
//     one more `/`-separated segment than any collection id — and by Decision 4's
//     non-nesting invariant no collection id is a path prefix of another, so no item
//     key can equal a collection id either.
//   - A status id joins on NUL, which neither a slug nor a filesystem path can contain,
//     so it sits outside both other spaces entirely (the same trick
//     pruneNestedSelections uses). A printable separator would not do: a collection
//     whose path happened to be "x status" would collide with collection "x"'s status
//     row.
// A collision would therefore be a loud crash rather than silent corruption, but the
// argument above is why one cannot happen in the first place.
export const panelNodeId = (node: PanelNode): string => {
  switch (node.kind) {
    case "collection":
      return node.collection.id;
    case "item":
      return itemKey(node.item);
    case "status":
      return node.collection + STATUS_SEPARATOR + "status";
  }
};

// The collection a node lives in: its own id for a collection row, and the owning
// collection for the other two tiers.
export const panelNodeCollection = (node: PanelNode): string => {
  switch (node.kind) {
    case "collection":
      return node.collection.id;
    case "item":
      return node.item.collection;
    case "status":
      return node.collection;
  }
};

export const panelNodeLabel = (node: PanelNode): string => {
  switch (node.kind) {
    case "collection":
      return node.collection.name;
    case "item":
      return node.item.item.name;
    case "status":
      return node.label;
  }
};

// Whether the collection tier is on screen. Exactly one collection means no tier at
// all — the panel header names it, as it did before the workspace existed.
export const panelTiered = (collections: readonly CollectionSummary[]): boolean =>
  collections.length !== 1;

// Drops the tiers that only the tree understands, for the callbacks that speak items
// (onDelete / onMove / onContextMenu).
export const panelItems = (nodes: readonly PanelNode[]): ItemWithPath[] =>
  nodes.flatMap((node) => (node.kind === "item" ? [node.item] : []));

export interface PanelTreeInput {
  // Ordered as ListCollections returned them; that order is the row order.
  collections: readonly CollectionSummary[];
  // A collection's already-loaded ROOT items. Absent = its Get has not landed, which
  // is a "Loading…" status row rather than an empty collection.
  itemsByCollection: ReadonlyMap<string, ItemWithPath[]>;
  activeCollection: string | null;
}

// The tooltip disambiguates two collections that share a display name, which the id
// (their path) always can.
const collectionTooltip = (c: CollectionSummary): string =>
  `${c.id} — ${c.sourceCount} ${c.sourceCount === 1 ? "source" : "sources"}`;

export function panelTreeAdapter(input: PanelTreeInput): TreeAdapter<PanelNode> {
  const { collections, itemsByCollection, activeCollection } = input;
  const tiered = panelTiered(collections);

  // Built eagerly, exactly as request-tree's buildParentIndex walks its roots: every
  // node object is then created once, so getChildren/getParent are map lookups and a
  // node's identity is stable for the life of this adapter.
  const childrenById = new Map<string, PanelNode[]>();
  const parentById = new Map<string, PanelNode>();

  const register = (node: PanelNode, children: PanelNode[], parent: PanelNode | undefined): void => {
    const id = panelNodeId(node);
    childrenById.set(id, children);
    if (parent) parentById.set(id, parent);
  };

  const itemNode = (item: ItemWithPath, parent: PanelNode | undefined): PanelNode => {
    const node: PanelNode = { kind: "item", item };
    const children = (item.children ?? []).map((child) => itemNode(child, node));
    register(node, children, parent);
    return node;
  };

  const statusNode = (collection: string, label: string, parent: PanelNode): PanelNode => {
    const node: PanelNode = { kind: "status", collection, label };
    register(node, [], parent);
    return node;
  };

  // A broken collection reports its error whatever its Get did, because a listing that
  // failed to summarize it will never produce a tree.
  const collectionChildren = (c: CollectionSummary, parent: PanelNode): PanelNode[] => {
    if (c.error) return [statusNode(c.id, c.error, parent)];
    const items = itemsByCollection.get(c.id);
    if (items === undefined) return [statusNode(c.id, LOADING_LABEL, parent)];
    if (items.length === 0) return [statusNode(c.id, EMPTY_LABEL, parent)];
    return items.map((item) => itemNode(item, parent));
  };

  const roots: PanelNode[] = tiered
    ? collections.map((c) => {
        const node: PanelNode = { kind: "collection", collection: c };
        register(node, collectionChildren(c, node), undefined);
        return node;
      })
    : (itemsByCollection.get(collections[0].id) ?? []).map((item) => itemNode(item, undefined));

  return {
    getId: panelNodeId,
    getChildren: (node) =>
      node === undefined ? roots : childrenById.get(panelNodeId(node)) ?? [],

    // The ACTIVE collection opens itself; every other one starts closed. Tree folds
    // this in through resolveExpansion (flatten.ts:87-105), whose `seen` set means the
    // auto-open happens ONCE — a later manual collapse is never sprung back open.
    getCollapsibleState: (node) => {
      switch (node.kind) {
        case "collection":
          return node.collection.id === activeCollection ? "expanded" : "collapsed";
        case "item":
          return node.item.item.content.case === "folder" ? "expanded" : "none";
        case "status":
          return "none";
      }
    },

    getParent: (node) => parentById.get(panelNodeId(node)),

    getTreeItem: (node): TreeItemLike => {
      switch (node.kind) {
        case "collection":
          return {
            // Name, disambiguated by the path (Decision 2): five collections may all
            // be called "requests", and only the id tells them apart.
            label: node.collection.name,
            description: node.collection.id,
            icon: "root-folder",
            tooltip: collectionTooltip(node.collection),
            kind: "collection",
          };
        case "item":
          return requestTreeItem(node.item);
        case "status":
          return { label: node.label, kind: "status" };
      }
    },

    getTypeaheadLabel: panelNodeLabel,
  };
}

export function usePanelTreeAdapter(input: PanelTreeInput): TreeAdapter<PanelNode> {
  const { collections, itemsByCollection, activeCollection } = input;
  return useMemo(
    () => panelTreeAdapter({ collections, itemsByCollection, activeCollection }),
    [collections, itemsByCollection, activeCollection]
  );
}

// Returns null for the two portable tiers, declining them back to the tree's own
// getTreeItem renderer (TreeRow.tsx) — which is what keeps the collection tier usable
// by a VS Code TreeProvider while the item tier stays rich.
export function renderPanelRow(
  node: PanelNode,
  state: TreeRowState,
  cb: RequestRowCallbacks
): ReactNode {
  if (node.kind !== "item") return null;
  return renderRequestRow(node.item, state, cb);
}

// The pure half of the panel's canDrop. The host ANDs this with its display-name
// collision check, which needs the store and cannot live here.
export function panelDropAllowed(
  dragged: readonly PanelNode[],
  to: { parent: PanelNode | null; before?: PanelNode },
  opts: { tiered: boolean }
): boolean {
  // Only items are draggable payloads: a collection row is not moveable (MoveItem
  // addresses one collection) and a status row is not a thing at all.
  const items = panelItems(dragged);
  if (items.length === 0 || items.length !== dragged.length) return false;

  // Nothing goes inside a "Loading…" / error / empty placeholder.
  if (to.parent?.kind === "status") return false;

  // Which collection the drop lands in. With the tier present, a null parent is the
  // tree root — the space BETWEEN collection rows — which names no collection, so
  // there is nothing to move into. Untiered, a null parent is the sole collection's
  // root, and the sole collection is by definition the one every dragged item is in.
  const destination =
    to.parent === null
      ? opts.tiered
        ? null
        : items[0].collection
      : panelNodeCollection(to.parent);
  if (destination === null) return false;

  // Cross-collection moves are rejected outright (Decision 10): MoveItem addresses one
  // collection, and copying across two source lists and two script sets is not this phase.
  if (!items.every((item) => item.collection === destination)) return false;
  if (to.before !== undefined && panelNodeCollection(to.before) !== destination) return false;

  return true;
}
