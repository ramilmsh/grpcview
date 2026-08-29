import { describe, it } from "node:test";
import { expect } from "expect";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import type { CollectionSummary } from "@grpcview/v1/service_pb";
import type { Item } from "@grpcview/v1/workspace_pb";
import { Tree } from "@/components/tree/Tree";
import { flatten } from "@/components/tree/flatten";
import type { TreeAdapter, TreeRowState } from "@/components/tree/types";
import { itemKey, type ItemWithPath } from "@/lib/format";
import { requestTreeItem, type RequestRowCallbacks } from "./request-tree";
import {
  EMPTY_LABEL,
  LOADING_LABEL,
  panelDropAllowed,
  panelItems,
  panelNodeId,
  panelTiered,
  panelTreeAdapter,
  renderPanelRow,
  type PanelNode,
} from "./panel-tree";

// Local fixture builders rather than the ones in format.test.ts / request-tree.test.tsx:
// both hardcode a single collection id, and every rule here turns on having two.
const slugify = (name: string): string => name.toLowerCase().replace(/\s+/g, "-");

const folder = (
  collection: string,
  name: string,
  path: string[],
  children: ItemWithPath[]
): ItemWithPath => ({
  item: {
    name,
    slug: slugify(name),
    content: { case: "folder", value: { items: [] } },
  } as unknown as Item,
  collection,
  path,
  slugPath: path.map(slugify),
  children,
});

const request = (collection: string, name: string, path: string[]): ItemWithPath => ({
  item: { name, slug: slugify(name), content: { case: "request", value: {} } } as unknown as Item,
  collection,
  path,
  slugPath: path.map(slugify),
});

const summary = (
  id: string,
  name: string,
  sourceCount = 0,
  error = ""
): CollectionSummary => ({ id, name, sourceCount, error }) as unknown as CollectionSummary;

const PAY = "services/payments/requests";
const LEDGER = "services/ledger/requests";

const payItems: ItemWithPath[] = [
  request(PAY, "Ping", []),
  folder(PAY, "Calls", [], [
    request(PAY, "Charge", ["Calls"]),
    folder(PAY, "Admin", ["Calls"], [request(PAY, "Purge", ["Calls", "Admin"])]),
  ]),
];
const ledgerItems: ItemWithPath[] = [request(LEDGER, "ListEntries", [])];

const payments = summary(PAY, "payments", 2);
const ledger = summary(LEDGER, "ledger", 1);

const twoCollections = [payments, ledger];
const bothLoaded = new Map([
  [PAY, payItems],
  [LEDGER, ledgerItems],
]);

const tieredAdapter = (activeCollection: string | null = PAY) =>
  panelTreeAdapter({
    collections: twoCollections,
    itemsByCollection: bothLoaded,
    activeCollection,
  });

const soloAdapter = () =>
  panelTreeAdapter({
    collections: [payments],
    itemsByCollection: new Map([[PAY, payItems]]),
    activeCollection: PAY,
  });

const collectionNode = (c: CollectionSummary): PanelNode => ({ kind: "collection", collection: c });
const itemNode = (item: ItemWithPath): PanelNode => ({ kind: "item", item });
const statusNode = (collection: string, label = LOADING_LABEL): PanelNode => ({
  kind: "status",
  collection,
  label,
});

// The node the adapter itself built for `id`, so tests assert on the real objects.
const nodeOf = (adapter: TreeAdapter<PanelNode>, id: string): PanelNode => {
  const search = (nodes: PanelNode[]): PanelNode | undefined => {
    for (const node of nodes) {
      if (panelNodeId(node) === id) return node;
      const found = search(adapter.getChildren(node) as PanelNode[]);
      if (found) return found;
    }
    return undefined;
  };
  const found = search(adapter.getChildren() as PanelNode[]);
  if (!found) throw new Error(`no node ${JSON.stringify(id)}`);
  return found;
};

describe("panelNodeId", () => {
  it("gives each tier its own id shape", () => {
    expect(panelNodeId(collectionNode(payments))).toBe(PAY);
    expect(panelNodeId(itemNode(payItems[0]))).toBe(itemKey(payItems[0]));
    expect(panelNodeId(itemNode(payItems[0]))).toBe(`${PAY}/ping`);
    expect(panelNodeId(statusNode(PAY))).toBe(`${PAY}\u0000status`);
  });

  it("cannot collide across tiers: an item key adds a segment, a status id adds a NUL", () => {
    const ids = flatten(tieredAdapter(), new Set([PAY, LEDGER, `${PAY}/calls`])).rows.map(
      (r) => r.id
    );
    expect(new Set(ids).size).toBe(ids.length);

    // Every item key is the collection id plus at least one more "/" segment, so it is
    // never equal to a collection id (and no collection id prefixes another).
    for (const item of [...payItems, ...ledgerItems]) {
      const key = panelNodeId(itemNode(item));
      expect(key.startsWith(`${PAY}/`) || key.startsWith(`${LEDGER}/`)).toBe(true);
      expect(key).not.toBe(PAY);
      expect(key).not.toBe(LEDGER);
      expect(key).not.toContain("\u0000");
    }
    expect(panelNodeId(statusNode(PAY))).toContain("\u0000");
  });
});

describe("panelTiered", () => {
  it("is off for exactly one collection and on for several or none", () => {
    expect(panelTiered([payments])).toBe(false);
    expect(panelTiered(twoCollections)).toBe(true);
    expect(panelTiered([])).toBe(true);
  });
});

describe("panelTreeAdapter: roots", () => {
  it("with one collection the roots are its items — there is no collection row", () => {
    const roots = soloAdapter().getChildren() as PanelNode[];
    expect(roots.map((n) => n.kind)).toEqual(["item", "item"]);
    expect(roots.map(panelNodeId)).toEqual([`${PAY}/ping`, `${PAY}/calls`]);
  });

  it("with one collection whose Get has not landed the tree is simply empty", () => {
    const adapter = panelTreeAdapter({
      collections: [payments],
      itemsByCollection: new Map(),
      activeCollection: PAY,
    });
    expect(adapter.getChildren()).toEqual([]);
  });

  it("with several collections the roots are the collection rows, in listing order", () => {
    const roots = tieredAdapter().getChildren() as PanelNode[];
    expect(roots.map((n) => n.kind)).toEqual(["collection", "collection"]);
    expect(roots.map(panelNodeId)).toEqual([PAY, LEDGER]);
  });

  it("with no collections there are no roots at all", () => {
    const adapter = panelTreeAdapter({
      collections: [],
      itemsByCollection: new Map(),
      activeCollection: null,
    });
    expect(adapter.getChildren()).toEqual([]);
  });
});

describe("panelTreeAdapter: getChildren", () => {
  it("a loaded collection yields its root items", () => {
    const adapter = tieredAdapter();
    const kids = adapter.getChildren(collectionNode(payments)) as PanelNode[];
    expect(kids.map(panelNodeId)).toEqual([`${PAY}/ping`, `${PAY}/calls`]);
  });

  it("an unloaded collection yields exactly one Loading… row", () => {
    const adapter = panelTreeAdapter({
      collections: twoCollections,
      itemsByCollection: new Map([[LEDGER, ledgerItems]]),
      activeCollection: PAY,
    });
    const kids = adapter.getChildren(collectionNode(payments)) as PanelNode[];
    expect(kids).toHaveLength(1);
    expect(kids[0]).toMatchObject({ kind: "status", collection: PAY, label: LOADING_LABEL });
  });

  it("a collection with a summary error yields that error, loaded or not", () => {
    const broken = summary(PAY, "payments", 0, "grpcview.json: unexpected EOF");
    const adapter = panelTreeAdapter({
      collections: [broken, ledger],
      itemsByCollection: bothLoaded,
      activeCollection: PAY,
    });
    const kids = adapter.getChildren(collectionNode(broken)) as PanelNode[];
    expect(kids).toHaveLength(1);
    expect(kids[0]).toMatchObject({ kind: "status", label: "grpcview.json: unexpected EOF" });
  });

  it("a loaded but genuinely empty collection says so", () => {
    const adapter = panelTreeAdapter({
      collections: twoCollections,
      itemsByCollection: new Map([
        [PAY, []],
        [LEDGER, ledgerItems],
      ]),
      activeCollection: PAY,
    });
    const kids = adapter.getChildren(collectionNode(payments)) as PanelNode[];
    expect(kids).toHaveLength(1);
    expect(kids[0]).toMatchObject({ kind: "status", label: EMPTY_LABEL });
  });

  it("an item yields its children, and a request none", () => {
    const adapter = tieredAdapter();
    const calls = nodeOf(adapter, `${PAY}/calls`);
    expect((adapter.getChildren(calls) as PanelNode[]).map(panelNodeId)).toEqual([
      `${PAY}/calls/charge`,
      `${PAY}/calls/admin`,
    ]);
    expect(adapter.getChildren(nodeOf(adapter, `${PAY}/ping`))).toEqual([]);
  });

  it("a status row has no children", () => {
    const adapter = panelTreeAdapter({
      collections: twoCollections,
      itemsByCollection: new Map(),
      activeCollection: PAY,
    });
    const kids = adapter.getChildren(collectionNode(payments)) as PanelNode[];
    expect(adapter.getChildren(kids[0])).toEqual([]);
  });
});

describe("panelTreeAdapter: getCollapsibleState", () => {
  it("expands the ACTIVE collection and collapses every other", () => {
    const adapter = tieredAdapter(LEDGER);
    expect(adapter.getCollapsibleState(collectionNode(ledger))).toBe("expanded");
    expect(adapter.getCollapsibleState(collectionNode(payments))).toBe("collapsed");
  });

  it("collapses everything when no collection is active", () => {
    const adapter = tieredAdapter(null);
    expect(adapter.getCollapsibleState(collectionNode(payments))).toBe("collapsed");
    expect(adapter.getCollapsibleState(collectionNode(ledger))).toBe("collapsed");
  });

  it("keeps a folder expanded, a request and a status row uncollapsible", () => {
    const adapter = tieredAdapter();
    expect(adapter.getCollapsibleState(itemNode(payItems[1]))).toBe("expanded");
    expect(adapter.getCollapsibleState(itemNode(payItems[0]))).toBe("none");
    expect(adapter.getCollapsibleState(statusNode(PAY))).toBe("none");
  });

  it("only the active collection is reported as default-expanded by flatten", () => {
    expect(flatten(tieredAdapter(PAY), new Set()).defaultExpanded).toEqual([PAY]);
    expect(flatten(tieredAdapter(LEDGER), new Set()).defaultExpanded).toEqual([LEDGER]);
    // Already in the caller's set = never re-reported, which is what stops a manual
    // collapse from being forced back open on the next render. Its folder children
    // are now visible and report themselves, as folders always have.
    expect(flatten(tieredAdapter(PAY), new Set([PAY])).defaultExpanded).toEqual([
      `${PAY}/calls`,
    ]);
  });
});

describe("panelTreeAdapter: getParent", () => {
  it("walks item → item → collection row when the tier is present", () => {
    const adapter = tieredAdapter();
    const purge = nodeOf(adapter, `${PAY}/calls/admin/purge`);
    const admin = adapter.getParent?.(purge);
    expect(admin && panelNodeId(admin)).toBe(`${PAY}/calls/admin`);
    const calls = admin && adapter.getParent?.(admin);
    expect(calls && panelNodeId(calls)).toBe(`${PAY}/calls`);
    const collection = calls && adapter.getParent?.(calls);
    expect(collection).toMatchObject({ kind: "collection" });
    expect(collection && panelNodeId(collection)).toBe(PAY);
  });

  it("stops at the root item when there is no tier", () => {
    const adapter = soloAdapter();
    const roots = adapter.getChildren() as PanelNode[];
    expect(adapter.getParent?.(roots[0])).toBeUndefined();
  });

  it("hands a status row back to its collection row", () => {
    const adapter = panelTreeAdapter({
      collections: twoCollections,
      itemsByCollection: new Map(),
      activeCollection: PAY,
    });
    const status = (adapter.getChildren(collectionNode(payments)) as PanelNode[])[0];
    expect(adapter.getParent?.(status)).toMatchObject({ kind: "collection" });
    expect(panelNodeId(adapter.getParent?.(status) as PanelNode)).toBe(PAY);
  });
});

describe("panelTreeAdapter: getTreeItem / getTypeaheadLabel", () => {
  it("describes a collection row portably: name, path, root-folder icon, kind", () => {
    const item = tieredAdapter().getTreeItem(collectionNode(payments));
    expect(item).toEqual({
      label: "payments",
      description: PAY,
      icon: "root-folder",
      tooltip: `${PAY} — 2 sources`,
      kind: "collection",
    });
  });

  it("singularizes a one-source tooltip", () => {
    expect(tieredAdapter().getTreeItem(collectionNode(ledger)).tooltip).toBe(
      `${LEDGER} — 1 source`
    );
  });

  it("describes a status row as label-only, with no icon", () => {
    const item = tieredAdapter().getTreeItem(statusNode(PAY, LOADING_LABEL));
    expect(item).toEqual({ label: LOADING_LABEL, kind: "status" });
  });

  it("delegates an item row to request-tree's own getTreeItem", () => {
    const adapter = tieredAdapter();
    for (const item of [payItems[0], payItems[1]]) {
      expect(adapter.getTreeItem(itemNode(item))).toEqual(requestTreeItem(item));
    }
  });

  it("types ahead on the display label of every tier", () => {
    const adapter = tieredAdapter();
    expect(adapter.getTypeaheadLabel(collectionNode(payments))).toBe("payments");
    expect(adapter.getTypeaheadLabel(itemNode(payItems[0]))).toBe("Ping");
    expect(adapter.getTypeaheadLabel(statusNode(PAY, LOADING_LABEL))).toBe(LOADING_LABEL);
  });
});

describe("panelItems", () => {
  it("keeps the items and drops the tiers the item callbacks cannot speak", () => {
    const items = panelItems([
      collectionNode(payments),
      itemNode(payItems[0]),
      statusNode(PAY),
      itemNode(ledgerItems[0]),
    ]);
    expect(items).toEqual([payItems[0], ledgerItems[0]]);
  });
});

describe("panelDropAllowed", () => {
  const pay = collectionNode(payments);
  const led = collectionNode(ledger);
  const ping = itemNode(payItems[0]);
  const calls = itemNode(payItems[1]);
  const charge = itemNode(payItems[1].children![0]);
  const entries = itemNode(ledgerItems[0]);

  it("allows an item into a folder in its own collection", () => {
    expect(panelDropAllowed([ping], { parent: calls }, { tiered: true })).toBe(true);
  });

  it("allows an item into its own collection row, and before a sibling there", () => {
    expect(panelDropAllowed([charge], { parent: pay }, { tiered: true })).toBe(true);
    expect(panelDropAllowed([charge], { parent: pay, before: ping }, { tiered: true })).toBe(true);
  });

  it("allows the untiered root, where a null parent IS the sole collection", () => {
    expect(panelDropAllowed([charge], { parent: null }, { tiered: false })).toBe(true);
  });

  it("rejects a dragged collection row", () => {
    expect(panelDropAllowed([pay], { parent: calls }, { tiered: true })).toBe(false);
    expect(panelDropAllowed([ping, pay], { parent: calls }, { tiered: true })).toBe(false);
  });

  it("rejects a dragged status row", () => {
    expect(panelDropAllowed([statusNode(PAY)], { parent: calls }, { tiered: true })).toBe(false);
  });

  it("rejects an empty drag", () => {
    expect(panelDropAllowed([], { parent: calls }, { tiered: true })).toBe(false);
  });

  it("rejects a status row as the destination", () => {
    expect(panelDropAllowed([ping], { parent: statusNode(PAY) }, { tiered: true })).toBe(false);
  });

  it("rejects a cross-collection drop into an item", () => {
    expect(panelDropAllowed([ping], { parent: entries }, { tiered: true })).toBe(false);
    expect(panelDropAllowed([entries], { parent: calls }, { tiered: true })).toBe(false);
  });

  it("rejects a cross-collection drop into a collection row", () => {
    expect(panelDropAllowed([ping], { parent: led }, { tiered: true })).toBe(false);
  });

  it("rejects a mixed-collection dragged set wherever it lands", () => {
    expect(panelDropAllowed([ping, entries], { parent: calls }, { tiered: true })).toBe(false);
    expect(panelDropAllowed([ping, entries], { parent: null }, { tiered: false })).toBe(false);
  });

  it("rejects the tiered root, which sits between collection rows and names none", () => {
    expect(panelDropAllowed([ping], { parent: null }, { tiered: true })).toBe(false);
  });

  it("rejects a `before` that lives in another collection", () => {
    expect(panelDropAllowed([ping], { parent: pay, before: entries }, { tiered: true })).toBe(false);
    expect(
      panelDropAllowed([ping], { parent: calls, before: statusNode(LEDGER) }, { tiered: true })
    ).toBe(false);
  });
});

// renderPanelRow declines the two portable tiers, which TreeRow must then render from
// getTreeItem — the affordance the collection tier depends on.
describe("renderPanelRow", () => {
  const cb: RequestRowCallbacks = {
    services: [],
    onStartRename: () => {},
    onNewRequestUnder: () => {},
    onDelete: () => {},
    onEditMetadata: () => {},
  };
  const state: TreeRowState = {
    focused: false,
    selected: false,
    active: false,
    expanded: false,
    depth: 0,
    renaming: false,
    dropTarget: null,
  };

  it("returns null for a collection row and for a status row", () => {
    expect(renderPanelRow(collectionNode(payments), state, cb)).toBeNull();
    expect(renderPanelRow(statusNode(PAY), state, cb)).toBeNull();
  });

  it("returns row content for an item", () => {
    expect(renderPanelRow(itemNode(payItems[0]), state, cb)).not.toBeNull();
  });

  it("renders a mixed tree: portable collection rows, rich item rows", () => {
    const adapter = tieredAdapter();
    const markup = renderToStaticMarkup(
      createElement(Tree<PanelNode>, {
        adapter,
        renderRow: (node, rowState) => renderPanelRow(node, rowState, cb),
        expanded: new Set([PAY]),
        "aria-label": "Requests",
      })
    );

    // The declined collection row falls back to getTreeItem: label, description, and
    // the tooltip — which only the fallback path sets.
    expect(markup).toContain("payments");
    expect(markup).toContain(PAY);
    expect(markup).toContain(`title="${PAY} — 2 sources"`);
    // The rich item rows still get their hover buttons.
    expect(markup).toContain("rowbtns");
    expect(markup).toContain("Ping");
    // Ledger stays collapsed, so its item never renders.
    expect(markup).not.toContain("ListEntries");
  });

  it("renders a status row's label through the same fallback", () => {
    const adapter = panelTreeAdapter({
      collections: twoCollections,
      itemsByCollection: new Map(),
      activeCollection: PAY,
    });
    const markup = renderToStaticMarkup(
      createElement(Tree<PanelNode>, {
        adapter,
        renderRow: (node, rowState) => renderPanelRow(node, rowState, cb),
        expanded: new Set([PAY]),
        "aria-label": "Requests",
      })
    );
    expect(markup).toContain(LOADING_LABEL);
  });
});
