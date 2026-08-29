import { describe, it } from "node:test";
import { expect } from "expect";
import { renderToStaticMarkup } from "react-dom/server";
import type { CollectionSummary } from "@grpcview/v1/service_pb";
import type { Item } from "@grpcview/v1/workspace_pb";
import { Tree } from "@/components/tree/Tree";
import { findByKey, type ItemWithPath } from "@/lib/format";
import { renderRequestRow, requestTreeAdapter, type RequestRowCallbacks } from "./request-tree";
import {
  EMPTY_LABEL,
  LOADING_LABEL,
  panelNodeId,
  panelTreeAdapter,
  renderPanelRow,
  STATUS_SEPARATOR,
  type PanelNode,
} from "./panel-tree";
import {
  collectionForNodeId,
  collectionsToQuery,
  countAllRequests,
  filterItemsByCollection,
  panelCanDrop,
} from "./panel-wiring";

// The panel's WIRING, not panel-tree's rules (panel-tree.test.ts pins those): what the host
// queries, how the filter box reaches a two-tier map, what the composed tree renders, and the
// display-name collision check the host adds on top of panelDropAllowed.
//
// No jsdom, so trees render with react-dom/server and `expanded` is passed controlled —
// useTreeState's default-expansion effect never runs under SSR.

const slugify = (name: string): string => name.toLowerCase();

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

const summary = (id: string, name: string): CollectionSummary =>
  ({ id, name, sourceCount: 1, error: "" }) as unknown as CollectionSummary;

const PAY = "services/payments/requests";
const LEDGER = "services/ledger/requests";
const payments = summary(PAY, "payments");
const ledger = summary(LEDGER, "ledger");

const payItems: ItemWithPath[] = [
  request(PAY, "Ping", []),
  folder(PAY, "Calls", [], [
    request(PAY, "Charge", ["Calls"]),
    request(PAY, "Ping", ["Calls"]),
    folder(PAY, "Admin", ["Calls"], [request(PAY, "Purge", ["Calls", "Admin"])]),
  ]),
  folder(PAY, "Dupes", [], [request(PAY, "Charge", ["Dupes"])]),
];
const ledgerItems: ItemWithPath[] = [
  request(LEDGER, "ListEntries", []),
  folder(LEDGER, "Reports", [], [request(LEDGER, "Ping", ["Reports"])]),
];

const bothLoaded: ReadonlyMap<string, ItemWithPath[]> = new Map([
  [PAY, payItems],
  [LEDGER, ledgerItems],
]);

const cb: RequestRowCallbacks = {
  services: [],
  onStartRename: () => {},
  onNewRequestUnder: () => {},
  onDelete: () => {},
  onEditMetadata: () => {},
};

// A row scanner, not an HTML parser: TreeRow renders `class` first on every row, which is
// the same prefix request-tree.test.tsx and Tree.portable.test.tsx already scrape.
interface Row {
  level: number;
  html: string;
}

const rowsOf = (markup: string): Row[] =>
  markup
    .split('<div class="treerow')
    .slice(1)
    .map((html) => ({ level: Number(/aria-level="(\d+)"/.exec(html)?.[1] ?? "0"), html }));

const panelMarkup = (
  input: {
    collections: readonly CollectionSummary[];
    itemsByCollection: ReadonlyMap<string, ItemWithPath[]>;
    activeCollection: string | null;
  },
  expanded: ReadonlySet<string>
): string =>
  renderToStaticMarkup(
    <Tree<PanelNode>
      adapter={panelTreeAdapter(input)}
      renderRow={(node, state) => renderPanelRow(node, state, cb)}
      expanded={expanded}
      aria-label="Collection"
    />
  );

describe("collectionsToQuery", () => {
  it("queries the active collection plus every EXPANDED collection row, sorted", () => {
    expect(
      collectionsToQuery(PAY, new Set([LEDGER]), [payments, ledger])
    ).toEqual([LEDGER, PAY]);
  });

  it("ignores the item keys that share treeExpanded with the collection ids", () => {
    const expanded = new Set([`${PAY}/calls`, `${PAY}/calls/admin`, "services/other/requests"]);
    expect(collectionsToQuery(PAY, expanded, [payments, ledger])).toEqual([PAY]);
  });

  it("dedupes an expanded collection that is also the active one", () => {
    expect(collectionsToQuery(PAY, new Set([PAY]), [payments, ledger])).toEqual([PAY]);
  });

  it("queries nothing when nothing is active or expanded", () => {
    expect(collectionsToQuery(null, new Set(), [payments, ledger])).toEqual([]);
  });
});

describe("countAllRequests", () => {
  it("sums the requests of every LOADED collection, folders excluded", () => {
    expect(countAllRequests(bothLoaded)).toBe(7); // 5 in payments, 2 in ledger
    expect(countAllRequests(new Map([[PAY, payItems]]))).toBe(5);
    expect(countAllRequests(new Map())).toBe(0);
  });
});

describe("filterItemsByCollection", () => {
  it("returns the SAME map for an empty query, so the adapter memo does not rebuild", () => {
    expect(filterItemsByCollection(bothLoaded, "")).toBe(bothLoaded);
  });

  it("prunes each collection independently", () => {
    const filtered = filterItemsByCollection(bothLoaded, "purge");
    expect(filtered.get(PAY)?.map((i) => i.item.name)).toEqual(["Calls"]);
    expect(findByKey(filtered.get(PAY) ?? [], `${PAY}/calls`)?.children?.map((c) => c.item.name))
      .toEqual(["Admin"]);
    // A collection with no match keeps its entry, holding nothing.
    expect(filtered.has(LEDGER)).toBe(true);
    expect(filtered.get(LEDGER)).toEqual([]);
  });

  it("never turns a loaded collection back into an absent one — empty means empty, not loading", () => {
    const filtered = filterItemsByCollection(bothLoaded, "nothing-matches-this");
    const markup = panelMarkup(
      { collections: [payments, ledger], itemsByCollection: filtered, activeCollection: PAY },
      new Set([PAY, LEDGER])
    );
    // Two collection rows, each with one empty row under it — not a Loading… row, which
    // would claim a Get is still in flight.
    const rows = markup.split('<div class="treerow').length - 1;
    expect(rows).toBe(4);
    expect(markup).not.toContain(LOADING_LABEL);
    expect(markup.split(EMPTY_LABEL)).toHaveLength(3);
  });
});

// Folders report themselves default-expanded and resolveExpansion folds that in during
// render, so the `expanded` argument below only decides which non-active COLLECTION rows are
// open — every folder under an open one is.
describe("the panel's composed tree", () => {
  it("puts a collection row above each collection's items when there are two", () => {
    const rows = rowsOf(
      panelMarkup(
        { collections: [payments, ledger], itemsByCollection: bothLoaded, activeCollection: PAY },
        new Set([PAY, LEDGER])
      )
    );
    // Two roots, and both of them are collection rows: name plus the path that
    // disambiguates two collections sharing one.
    const roots = rows.filter((r) => r.level === 1);
    expect(roots).toHaveLength(2);
    expect(rows[0].html).toContain("payments");
    expect(rows[0].html).toContain(PAY);

    const ledgerRow = rows.findIndex((r) => r.level === 1 && r.html.includes(">ledger<"));
    expect(ledgerRow).toBeGreaterThan(0);
    expect(rows[ledgerRow].html).toContain(LEDGER);
    // Every item row hangs off one collection row or the other; none is a root.
    expect(rows.slice(1, ledgerRow).every((r) => r.level >= 2)).toBe(true);
    expect(rows.slice(ledgerRow + 1).every((r) => r.level >= 2)).toBe(true);
    expect(rows.slice(1, ledgerRow).some((r) => r.html.includes(">Ping<"))).toBe(true);
    expect(rows.slice(ledgerRow + 1).some((r) => r.html.includes(">ListEntries<"))).toBe(true);
  });

  it("renders ONE collection byte-for-byte as the single-collection request tree did", () => {
    const markup = panelMarkup(
      { collections: [payments], itemsByCollection: bothLoaded, activeCollection: PAY },
      new Set()
    );
    const asBefore = renderToStaticMarkup(
      <Tree<ItemWithPath>
        adapter={requestTreeAdapter(payItems)}
        renderRow={(item, state) => renderRequestRow(item, state, cb)}
        expanded={new Set()}
        aria-label="Collection"
      />
    );
    expect(markup).toBe(asBefore);
    // No collection row: the three root ITEMS are the depth-0 rows.
    const rows = rowsOf(markup);
    expect(rows.filter((r) => r.level === 1)).toHaveLength(3);
    expect(rows[0].html).toContain(">Ping<");
    // No row carries the collection's NAME as a label (its id is in every row's dom id).
    expect(markup).not.toContain(">payments<");
  });

  it("shows a Loading… row under an EXPANDED collection whose Get has not landed", () => {
    const input = {
      collections: [payments, ledger],
      itemsByCollection: new Map([[PAY, payItems]]),
      activeCollection: PAY,
    };
    const rows = rowsOf(panelMarkup(input, new Set([LEDGER])));
    const last = rows[rows.length - 1];
    expect(last.level).toBe(2);
    expect(last.html).toContain(LOADING_LABEL);
    expect(last.html).not.toContain("rowbtns"); // portable tier: no rich row content

    // Collapsed, it says nothing — expanding the row is what makes the host query it.
    expect(panelMarkup(input, new Set())).not.toContain(LOADING_LABEL);
  });

  it("shows an empty row under a collection that loaded and holds nothing", () => {
    const markup = panelMarkup(
      {
        collections: [payments, ledger],
        itemsByCollection: new Map([
          [PAY, payItems],
          [LEDGER, []],
        ]),
        activeCollection: PAY,
      },
      new Set([LEDGER])
    );
    expect(markup).toContain(EMPTY_LABEL);
    expect(markup).not.toContain(LOADING_LABEL);
  });
});

describe("panelCanDrop", () => {
  const collectionNode = (c: CollectionSummary): PanelNode => ({ kind: "collection", collection: c });
  const itemNode = (item: ItemWithPath): PanelNode => ({ kind: "item", item });

  const opts = { tiered: true, itemsByCollection: bothLoaded };
  const payRow = collectionNode(payments);
  const ledgerRow = collectionNode(ledger);
  const calls = itemNode(payItems[1]);
  const charge = itemNode(payItems[1].children![0]);
  const callsPing = itemNode(payItems[1].children![1]);
  const admin = itemNode(payItems[1].children![2]);
  const dupeCharge = itemNode(payItems[2].children![0]);
  const reportsPing = itemNode(ledgerItems[1].children![0]);

  it("rejects a cross-collection drop even where the name is free", () => {
    expect(panelCanDrop([charge], { parent: ledgerRow }, opts)).toBe(false);
    expect(panelCanDrop([charge], { parent: itemNode(ledgerItems[1]) }, opts)).toBe(false);
    expect(panelCanDrop([reportsPing], { parent: calls }, opts)).toBe(false);
  });

  it("rejects a drop onto a name the destination already holds, though the tree cannot see it", () => {
    // Calls is COLLAPSED in every render above, so its "Charge" is invisible to the tree —
    // the whole reason this check exists rather than being left to dnd.ts.
    expect(panelCanDrop([dupeCharge], { parent: calls }, opts)).toBe(false);
    // Same name, a destination that does not hold it: allowed.
    expect(panelCanDrop([dupeCharge], { parent: admin }, opts)).toBe(true);
  });

  it("counts a sibling the FILTER hid, which is why the panel passes the UNFILTERED map", () => {
    const filtered = filterItemsByCollection(bothLoaded, "purge");
    expect(
      findByKey(filtered.get(PAY) ?? [], `${PAY}/calls`)?.children?.map((c) => c.item.name)
    ).toEqual(["Admin"]);
    expect(panelCanDrop([dupeCharge], { parent: calls }, opts)).toBe(false);
    // What the panel must NOT do: resolve the collision against what the filter left.
    expect(
      panelCanDrop([dupeCharge], { parent: calls }, { tiered: true, itemsByCollection: filtered })
    ).toBe(true);
  });

  it("resolves the collision in the DESTINATION collection, not the active one", () => {
    // Payments' root holds "Ping"; ledger's does not — so this move into ledger's root is
    // legal, and would be rejected by anything reading the active collection's items.
    expect(panelCanDrop([reportsPing], { parent: ledgerRow }, opts)).toBe(true);
    expect(panelCanDrop([callsPing], { parent: payRow }, opts)).toBe(false);
  });

  it("allows a pure reorder inside a parent that already holds the name", () => {
    expect(panelCanDrop([charge], { parent: calls, before: callsPing }, opts)).toBe(true);
  });

  it("uses the sole collection's root for the untiered null parent", () => {
    const solo = { tiered: false, itemsByCollection: new Map([[PAY, payItems]]) };
    expect(panelCanDrop([charge], { parent: null }, solo)).toBe(true);
    // "Ping" is already at that root.
    expect(panelCanDrop([callsPing], { parent: null }, solo)).toBe(false);
  });

  it("still rejects everything panelDropAllowed does — the structural half is unchanged", () => {
    expect(panelCanDrop([], { parent: calls }, opts)).toBe(false);
    expect(panelCanDrop([payRow], { parent: calls }, opts)).toBe(false);
    expect(panelCanDrop([charge], { parent: null }, opts)).toBe(false); // tiered root
    expect(
      panelCanDrop([charge], { parent: { kind: "status", collection: PAY, label: LOADING_LABEL } }, opts)
    ).toBe(false);
  });
});

// Focusing any row is what scopes Sources, Scripts and the pickers to a collection, so the
// row id has to resolve back to one. A collection id contains slashes of its own, which is
// why this matches against the known ids instead of splitting the key.
describe("collectionForNodeId", () => {
  const all = [payments, ledger];

  it("resolves a collection row to itself", () => {
    expect(collectionForNodeId(PAY, all)).toBe(PAY);
  });

  it("resolves an item key to the collection it is prefixed with", () => {
    const key = panelNodeId({ kind: "item", item: payItems[0] });
    expect(key.startsWith(`${PAY}/`)).toBe(true);
    expect(collectionForNodeId(key, all)).toBe(PAY);
    expect(collectionForNodeId(panelNodeId({ kind: "item", item: ledgerItems[0] }), all)).toBe(
      LEDGER
    );
  });

  it("resolves a status row id", () => {
    const id = panelNodeId({ kind: "status", collection: LEDGER, label: LOADING_LABEL });
    expect(id).toBe(`${LEDGER}${STATUS_SEPARATOR}status`);
    expect(collectionForNodeId(id, all)).toBe(LEDGER);
  });

  it("does not let a collection own a sibling whose id merely starts with its own", () => {
    // "requests" is a string prefix of "requests2" without being a path prefix of it, so a
    // bare startsWith would hand requests2's rows to requests.
    const a = summary("requests", "a");
    const b = summary("requests2", "b");
    expect(collectionForNodeId("requests2", [a, b])).toBe("requests2");
    expect(collectionForNodeId("requests2/x", [a, b])).toBe("requests2");
    expect(collectionForNodeId("requests/x", [a, b])).toBe("requests");
  });

  it("returns null for an id no collection owns", () => {
    expect(collectionForNodeId("services/other/requests", all)).toBeNull();
    expect(collectionForNodeId("", all)).toBeNull();
  });
});
