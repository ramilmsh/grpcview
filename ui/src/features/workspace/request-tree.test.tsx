import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { Tree } from "@/components/tree/Tree";
import { TreeRow } from "@/components/tree/TreeRow";
import { flatten } from "@/components/tree/flatten";
import type { TreeRowState } from "@/components/tree/types";
import type { Item, Service } from "@grpcview/v1/workspace_pb";
import { MethodKindTag, type MethodKind } from "@/components/ui/Tag";
import { renderRequestRow, requestTreeAdapter, type RequestRowCallbacks } from "./request-tree";
import type { ItemWithPath } from "@/lib/format";

// No jsdom here, so rows are rendered with react-dom/server; `expanded` is passed
// controlled because useTreeState's default-expansion useEffect never runs under SSR.

const folder = (name: string, path: string[], children: ItemWithPath[]): ItemWithPath => ({
  item: { name, content: { case: "folder", value: { items: [] } } } as unknown as Item,
  path,
  children,
});

const request = (name: string, path: string[], service: string, method: string): ItemWithPath => ({
  item: { name, content: { case: "request", value: { service, method } } } as unknown as Item,
  path,
});

const SERVICE = "fixture.Greeter";
const services = [
  {
    package: "fixture",
    name: "Greeter",
    methods: [
      { name: "SayHello", clientStreaming: false, serverStreaming: false },
      { name: "ListHellos", clientStreaming: false, serverStreaming: true },
      { name: "SendHellos", clientStreaming: true, serverStreaming: false },
      { name: "Chat", clientStreaming: true, serverStreaming: true },
    ],
  },
] as unknown as Service[];

const roots: ItemWithPath[] = [
  request("Ping", [], SERVICE, "SayHello"),
  folder("Calls", [], [
    request("SayHello", ["Calls"], SERVICE, "SayHello"),
    request("ListHellos", ["Calls"], SERVICE, "ListHellos"),
    request("SendHellos", ["Calls"], SERVICE, "SendHellos"),
    request("Chat", ["Calls"], SERVICE, "Chat"),
    folder("Admin", ["Calls"], [
      request("Purge", ["Calls", "Admin"], SERVICE, "SayHello"),
    ]),
  ]),
];

const FULLY_EXPANDED = new Set(["Calls", "Calls/Admin"]);

const adapter = requestTreeAdapter(roots);

const noopCallbacks: RequestRowCallbacks = {
  services,
  onStartRename: () => {},
  onNewRequestUnder: () => {},
  onDelete: () => {},
  onEditMetadata: () => {},
};

const rowRendererWith =
  (cb: RequestRowCallbacks) =>
  (item: ItemWithPath, state: TreeRowState) =>
    renderRequestRow(item, state, cb);

// A tag-nesting scanner for react-dom/server's output, not a general HTML parser:
// only <input> is ever void here, and SVG <path> arrives closed.
interface PNode {
  tag: string;
  classes: string[];
  attrs: Record<string, string>;
  children: PNode[];
  text: string;
}

function parseFragment(html: string): PNode[] {
  const tagRe = /<(\/)?([a-zA-Z][a-zA-Z0-9]*)([^<>]*)>/g;
  const root: PNode = { tag: "#root", classes: [], attrs: {}, children: [], text: "" };
  const stack: PNode[] = [root];
  let lastIndex = 0;
  let m: RegExpExecArray | null;
  while ((m = tagRe.exec(html))) {
    const between = html.slice(lastIndex, m.index);
    if (between) stack[stack.length - 1].text += between;
    lastIndex = tagRe.lastIndex;

    const [, closing, tag, rest] = m;
    if (closing) {
      if (stack.length > 1) stack.pop();
      continue;
    }
    // Attribute values are always quote-terminated, so a trailing "/" can only
    // be a genuine self-close, never a slash inside some value.
    const selfClose = /\/\s*$/.test(rest);
    const attrsStr = rest.replace(/\/\s*$/, "");
    const attrs: Record<string, string> = {};
    const attrRe = /([a-zA-Z-]+)="([^"]*)"/g;
    let am: RegExpExecArray | null;
    while ((am = attrRe.exec(attrsStr))) attrs[am[1]] = am[2];

    const node: PNode = {
      tag,
      classes: (attrs.class ?? "").split(/\s+/).filter(Boolean),
      attrs,
      children: [],
      text: "",
    };
    stack[stack.length - 1].children.push(node);
    if (!selfClose) stack.push(node);
  }
  return root.children;
}

function findAll(nodes: PNode[], pred: (n: PNode) => boolean): PNode[] {
  const out: PNode[] = [];
  const walk = (list: PNode[]): void => {
    for (const n of list) {
      if (pred(n)) out.push(n);
      walk(n.children);
    }
  };
  walk(nodes);
  return out;
}

const hasClass = (n: PNode, cls: string): boolean => n.classes.includes(cls);

function rowsOf(markup: string): PNode[] {
  return findAll(parseFragment(markup), (n) => n.tag === "div" && hasClass(n, "treerow"));
}

function labelFor(kind: MethodKind): string {
  return parseFragment(renderToStaticMarkup(<MethodKindTag kind={kind} />))[0].text;
}

describe("request tree rows: direct-child CSS contract", () => {
  // app-tokens.css hover-reveal uses direct-child selectors: `.treerow:hover > .rowbtns`.
  it("rowbtns is a direct child of .treerow, on every row", () => {
    const markup = renderToStaticMarkup(
      <Tree
        adapter={adapter}
        renderRow={rowRendererWith(noopCallbacks)}
        expanded={FULLY_EXPANDED}
        aria-label="Test tree"
      />
    );
    const rows = rowsOf(markup);
    expect(rows).toHaveLength(8);

    for (const row of rows) {
      const direct = row.children.filter((c) => hasClass(c, "rowbtns"));
      expect(direct).toHaveLength(1);
    }
  });

  it("rowmeta is a direct child of .treerow on folder rows, and absent on request rows", () => {
    const markup = renderToStaticMarkup(
      <Tree
        adapter={adapter}
        renderRow={rowRendererWith(noopCallbacks)}
        expanded={FULLY_EXPANDED}
        aria-label="Test tree"
      />
    );
    const rows = rowsOf(markup);
    const [rowPing, rowCalls, rowSayHello, , , , rowAdmin, rowPurge] = rows;

    for (const row of [rowCalls, rowAdmin]) {
      const direct = row.children.filter((c) => hasClass(c, "rowmeta"));
      expect(direct).toHaveLength(1);
    }
    for (const row of [rowPing, rowSayHello, rowPurge]) {
      expect(row.children.some((c) => hasClass(c, "rowmeta"))).toBe(false);
    }
  });
});

describe("request tree rows: folder row", () => {
  it("renders its child count, and its gear/plus/pencil/trash buttons by title", () => {
    const markup = renderToStaticMarkup(
      <Tree
        adapter={adapter}
        renderRow={rowRendererWith(noopCallbacks)}
        expanded={FULLY_EXPANDED}
        aria-label="Test tree"
      />
    );
    const [, rowCalls, , , , , rowAdmin] = rowsOf(markup);

    expect(rowCalls.children.find((c) => hasClass(c, "rowmeta"))?.text).toBe("5");
    expect(rowAdmin.children.find((c) => hasClass(c, "rowmeta"))?.text).toBe("1");

    for (const row of [rowCalls, rowAdmin]) {
      const titles = findAll(row.children, (n) => n.tag === "button").map((b) => b.attrs.title);
      expect(titles).toEqual(["Folder metadata", "Add request", "Rename folder", "Delete folder"]);
    }
  });
});

describe("request tree rows: request row", () => {
  it("renders the right MethodKindTag label per kind, its name, and pencil/trash buttons", () => {
    const markup = renderToStaticMarkup(
      <Tree
        adapter={adapter}
        renderRow={rowRendererWith(noopCallbacks)}
        expanded={FULLY_EXPANDED}
        aria-label="Test tree"
      />
    );
    const [, , rowSayHello, rowListHellos, rowSendHellos, rowChat] = rowsOf(markup);

    const cases: Array<[PNode, string, MethodKind]> = [
      [rowSayHello, "SayHello", "u"],
      [rowListHellos, "ListHellos", "ss"],
      [rowSendHellos, "SendHellos", "cs"],
      [rowChat, "Chat", "bd"],
    ];
    for (const [row, name, kind] of cases) {
      const tag = row.children.find((c) => hasClass(c, "mtag"));
      expect(tag?.classes).toContain(`mt-${kind}`);
      expect(tag?.text).toBe(labelFor(kind));
      expect(row.children.some((c) => c.text === name)).toBe(true);

      const titles = findAll(row.children, (n) => n.tag === "button").map((b) => b.attrs.title);
      expect(titles).toEqual(["Rename request", "Delete request"]);

      expect(findAll(row.children, (n) => n.tag === "input")).toHaveLength(0);
    }
  });
});

describe("request tree rows: active/selected/focused", () => {
  it("are independent flags — each paints its own class with no effect on the others", () => {
    const markup = renderToStaticMarkup(
      <Tree
        adapter={adapter}
        renderRow={rowRendererWith(noopCallbacks)}
        expanded={FULLY_EXPANDED}
        activeId="Calls/ListHellos"
        selection={["Calls/SendHellos"]}
        focused="Calls/Chat"
        aria-label="Test tree"
      />
    );
    const rows = rowsOf(markup);
    expect(rows.map((r) => r.classes)).toEqual([
      ["treerow"],
      ["treerow"],
      ["treerow"],
      ["treerow", "on"],
      ["treerow", "sel"],
      ["treerow", "foc"],
      ["treerow"],
      ["treerow"],
    ]);
  });

  it("can all three land on the same row at once", () => {
    const markup = renderToStaticMarkup(
      <Tree
        adapter={adapter}
        renderRow={rowRendererWith(noopCallbacks)}
        expanded={FULLY_EXPANDED}
        activeId="Calls/Chat"
        selection={["Calls/Chat"]}
        focused="Calls/Chat"
        aria-label="Test tree"
      />
    );
    const [, , , , , rowChat] = rowsOf(markup);
    expect(rowChat.classes).toEqual(["treerow", "sel", "foc", "on"]);
  });
});

describe("request tree rows: indent guides", () => {
  it("renders exactly `depth` guide elements per row, and none at the root", () => {
    const markup = renderToStaticMarkup(
      <Tree
        adapter={adapter}
        renderRow={rowRendererWith(noopCallbacks)}
        expanded={FULLY_EXPANDED}
        aria-label="Test tree"
      />
    );
    const rows = rowsOf(markup);
    const guideCounts = rows.map((r) => r.children.filter((c) => hasClass(c, "guide")).length);
    expect(guideCounts).toEqual([0, 0, 1, 1, 1, 1, 1, 2]);
  });
});

// Driven through TreeRow: renaming is <Tree>'s internal state, unreachable by props.
describe("request tree rows: renaming replaces the row content entirely", () => {
  const flat = flatten(adapter, FULLY_EXPANDED);
  const rowModel = (id: string) => flat.rows[flat.indexById.get(id) ?? -1];

  const renamingMarkup = (id: string): PNode =>
    rowsOf(
      renderToStaticMarkup(
        <TreeRow
          row={rowModel(id)}
          domId="test-row"
          dataIndex={0}
          rowRef={() => {}}
          adapter={adapter}
          renderRow={rowRendererWith(noopCallbacks)}
          selected={false}
          focused={false}
          active={false}
          renaming
          dropTarget={null}
          dropDepth={0}
          dragging={false}
          renameSiblings={[]}
          onRenameCommit={() => {}}
          onRenameCancel={() => {}}
          indent={8}
          rowHeight={22}
          onRowClick={() => {}}
          onTwistieClick={() => {}}
          onContextMenu={() => {}}
          onDragStart={() => {}}
        />
      )
    )[0];

  it("swaps a request row's whole content for the input — tag, name and buttons all yield", () => {
    const row = renamingMarkup("Calls/SayHello");

    expect(findAll(row.children, (n) => n.tag === "input")).toHaveLength(1);
    expect(row.children.some((c) => c.text === "SayHello")).toBe(false);
    expect(findAll(row.children, (n) => n.tag === "button")).toHaveLength(0);
    expect(row.children.some((c) => hasClass(c, "mtag"))).toBe(false);
    expect(row.children.some((c) => hasClass(c, "rowbtns"))).toBe(false);
  });

  it("keeps the row SHELL — indent guides and the twistie column — around the input", () => {
    const row = renamingMarkup("Calls/Admin");
    expect(row.children.filter((c) => hasClass(c, "guide"))).toHaveLength(1);
    const twistie = row.children.find((c) => hasClass(c, "twistie"));
    expect(twistie).toBeDefined();
    expect(findAll([twistie as PNode], (n) => n.tag === "svg")).toHaveLength(1);
    expect(findAll(row.children, (n) => n.tag === "input")).toHaveLength(1);
  });

  it("seeds the input from adapter.getTreeItem().label, even for this RICH adapter", () => {
    const row = renamingMarkup("Calls/SayHello");
    const input = findAll(row.children, (n) => n.tag === "input")[0];
    expect(input.attrs.value).toBe("SayHello");
    expect(adapter.getTreeItem(rowModel("Calls/SayHello").node).label).toBe("SayHello");
    expect(input.attrs["aria-invalid"]).toBeUndefined();
    expect(input.classes).not.toContain("rename-invalid");
  });
});

describe("request tree rows: keyboard + a11y (T1)", () => {
  const treeContainer = (markup: string): PNode =>
    findAll(parseFragment(markup), (n) => n.tag === "div" && hasClass(n, "tree"))[0];

  it("the container is the one tabbable element, and names the focused row via aria-activedescendant", () => {
    const markup = renderToStaticMarkup(
      <Tree
        adapter={adapter}
        renderRow={rowRendererWith(noopCallbacks)}
        expanded={FULLY_EXPANDED}
        focused="Calls/SayHello"
        aria-label="Test tree"
      />
    );
    const container = treeContainer(markup);
    expect(container.attrs.tabindex).toBe("0");

    const [, , rowSayHello] = rowsOf(markup);
    // An ARIA IDREF may not contain whitespace, so the dom id is never the raw itemKey.
    expect(rowSayHello.attrs.id).toBeTruthy();
    expect(rowSayHello.attrs.id).not.toBe("Calls/SayHello");
    expect(container.attrs["aria-activedescendant"]).toBe(rowSayHello.attrs.id);
  });

  it("omits aria-activedescendant entirely when nothing is focused", () => {
    const markup = renderToStaticMarkup(
      <Tree
        adapter={adapter}
        renderRow={rowRendererWith(noopCallbacks)}
        expanded={FULLY_EXPANDED}
        aria-label="Test tree"
      />
    );
    expect(treeContainer(markup).attrs["aria-activedescendant"]).toBeUndefined();
  });

  it("each row's dom id is stable across two renders of the same tree", () => {
    const markupA = renderToStaticMarkup(
      <Tree adapter={adapter} renderRow={rowRendererWith(noopCallbacks)} expanded={FULLY_EXPANDED} aria-label="t" />
    );
    const markupB = renderToStaticMarkup(
      <Tree adapter={adapter} renderRow={rowRendererWith(noopCallbacks)} expanded={FULLY_EXPANDED} aria-label="t" />
    );
    expect(rowsOf(markupA).map((r) => r.attrs.id)).toEqual(rowsOf(markupB).map((r) => r.attrs.id));
  });

  it("aria-selected reflects SELECTION, independent of which row is focused", () => {
    const markup = renderToStaticMarkup(
      <Tree
        adapter={adapter}
        renderRow={rowRendererWith(noopCallbacks)}
        expanded={FULLY_EXPANDED}
        selection={["Calls/SendHellos"]}
        focused="Calls/Chat"
        aria-label="Test tree"
      />
    );
    const ariaSelected = rowsOf(markup).map((r) => r.attrs["aria-selected"]);
    expect(ariaSelected).toEqual(["false", "false", "false", "false", "true", "false", "false", "false"]);
  });
});

describe("request tree rows: aria-posinset / aria-setsize (T1)", () => {
  it("carries the right 1-based position and sibling count for a realistic folder+request tree", () => {
    const markup = renderToStaticMarkup(
      <Tree
        adapter={adapter}
        renderRow={rowRendererWith(noopCallbacks)}
        expanded={FULLY_EXPANDED}
        aria-label="Test tree"
      />
    );
    const rows = rowsOf(markup);
    expect(rows.map((r) => r.attrs["aria-posinset"])).toEqual(["1", "2", "1", "2", "3", "4", "5", "1"]);
    expect(rows.map((r) => r.attrs["aria-setsize"])).toEqual(["2", "2", "5", "5", "5", "5", "5", "1"]);
  });
});
