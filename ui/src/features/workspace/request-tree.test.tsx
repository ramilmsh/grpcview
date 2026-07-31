import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { Tree } from "@/components/tree/Tree";
import type { TreeRowState } from "@/components/tree/types";
import type { Item, Service } from "@grpcview/v1/workspace_pb";
import { MethodKindTag, type MethodKind } from "@/components/ui/Tag";
import { renderRequestRow, requestTreeAdapter, type RequestRowCallbacks } from "./request-tree";
import type { ItemWithPath } from "@/lib/format";

// T0's acceptance test (tree-rewrite-plan.md §Phases, T0) is "everything that
// works today still works" — browser automation is unavailable right now, so
// this file pins what CAN be proven from rendered markup alone: the RICH tier
// (renderRequestRow, the request tree's actual row content — the "second
// renderer" tier, plan §"Second consumer") over a fabricated ItemWithPath tree,
// via react-dom/server's renderToStaticMarkup — the same technique
// Tree.portable.test.tsx already uses for the PORTABLE tier, and for the same
// reason (vitest.config.ts runs tests in node, with no jsdom, and this is the
// one React render path that needs no DOM). `expanded` is passed CONTROLLED
// for the same reason that file documents: useTreeState's default-expansion
// resolution (resolveExpansion, flatten.ts) runs inside a useMemo, so it DOES
// run under SSR, but its follow-up useEffect never does (no commit phase) —
// passing every folder's id in `expanded` up front means this file's outcome
// never depends on that distinction.
//
// Items are built structurally and cast, exactly like lib/format.test.ts: the
// generated proto runtime (_pb modules) is Bazel-generated and not importable
// from a test, and request-tree.tsx only ever reads the fields constructed
// below, never constructs or serializes a real Item/Service.

const folder = (name: string, path: string[], children: ItemWithPath[]): ItemWithPath => ({
  item: { name, content: { case: "folder", value: { items: [] } } } as unknown as Item,
  path,
  children,
});

const request = (name: string, path: string[], service: string, method: string): ItemWithPath => ({
  item: { name, content: { case: "request", value: { service, method } } } as unknown as Item,
  path,
});

// One service, four methods — one per MethodKind (components/ui/Tag.tsx) — so
// the four nested request fixtures below resolve to four DIFFERENT kinds
// through the exact production path (resolveMethod + methodKind, lib/format.ts)
// rather than the test asserting kinds it made up independently of that code.
const SERVICE = "fixture.Greeter";
const services = [
  {
    package: "fixture",
    name: "Greeter",
    methods: [
      { name: "SayHello", clientStreaming: false, serverStreaming: false }, // u
      { name: "ListHellos", clientStreaming: false, serverStreaming: true }, // ss
      { name: "SendHellos", clientStreaming: true, serverStreaming: false }, // cs
      { name: "Chat", clientStreaming: true, serverStreaming: true }, // bd
    ],
  },
] as unknown as Service[];

// Tree shape: a root request, a folder with children (5: four requests + one
// nested folder), a nested request one level deep, and a nested folder two
// levels deep with its own child — giving depth 0/1/2 rows in one fixture
// rather than three separate ones, and both direct-child CSS-contract targets
// (folder rows: rowmeta + rowbtns; request rows: rowbtns only) in the same tree.
const roots: ItemWithPath[] = [
  request("Ping", [], SERVICE, "SayHello"), // root request, depth 0
  folder("Calls", [], [
    // folder with children, depth 0 (count === 5, below)
    request("SayHello", ["Calls"], SERVICE, "SayHello"), // depth 1, kind u
    request("ListHellos", ["Calls"], SERVICE, "ListHellos"), // depth 1, kind ss
    request("SendHellos", ["Calls"], SERVICE, "SendHellos"), // depth 1, kind cs
    request("Chat", ["Calls"], SERVICE, "Chat"), // depth 1, kind bd
    folder("Admin", ["Calls"], [
      request("Purge", ["Calls", "Admin"], SERVICE, "SayHello"), // depth 2
    ]),
  ]),
];

// Both folders' ids, so useTreeState's default-expansion resolution has
// nothing to do (see the file comment above) — the outcome rests entirely on
// this controlled set, matching Tree.portable.test.tsx's own fixture rationale.
const FULLY_EXPANDED = new Set(["Calls", "Calls/Admin"]);

const adapter = requestTreeAdapter(roots);

const noopCallbacks: RequestRowCallbacks = {
  services,
  renamingKey: null,
  onRenamingChange: () => {},
  onRename: () => {},
  onNewRequestUnder: () => {},
  onDelete: () => {},
  onEditMetadata: () => {},
};

// renderRequestRow needs the callbacks bag threaded through Tree's renderRow
// prop (this is exactly what CollectionPanel.tsx's own `renderRow` closure
// does) — a small factory so each test can override just renamingKey.
const rowRendererWith =
  (cb: RequestRowCallbacks) =>
  (item: ItemWithPath, state: TreeRowState) =>
    renderRequestRow(item, state, cb);

// ── a minimal markup parser, NOT a full HTML parser ─────────────────────────
// vitest.config.ts runs in node with no jsdom (see its own comment), so there is
// no real DOM to query with querySelector. What follows is a tiny tag-nesting
// scanner purpose-built for react-dom/server's OWN output shape (verified by
// hand against this exact component tree): every non-void element is opened and
// closed with matching tags (including SVG's <path>...</path> — Phosphor icons
// render it unself-closed), and the one void element this file's markup ever
// produces, <input>, is self-closed with a trailing `/>` — which is what
// `selfClose` below detects, so it is never pushed as a parent for whatever
// follows it. This is deliberately more than a plain regex/substring check:
// item 1 below (the direct-child CSS contract) needs to distinguish "rowbtns
// appears somewhere inside this row" from "rowbtns is this row's OWN direct
// child", and a substring search over the row's markup cannot tell those apart
// — it would keep passing even if a later refactor wrapped the buttons in an
// extra element, silently breaking app-tokens.css's `.treerow:hover > .rowbtns`
// (a direct-child selector) with no test catching it. Building a real (if
// minimal) parent/children tree makes that distinction a one-line `.children`
// check instead of a fragile regex.
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
    // Text between the previous tag and this one belongs to whatever element
    // is currently open — e.g. the "5" inside <span class="rowmeta">5</span>,
    // or a row's own visible name.
    const between = html.slice(lastIndex, m.index);
    if (between) stack[stack.length - 1].text += between;
    lastIndex = tagRe.lastIndex;

    const [, closing, tag, rest] = m;
    if (closing) {
      if (stack.length > 1) stack.pop();
      continue;
    }
    // The tag's own self-close marker, if any, is unambiguously the LAST
    // character before its closing `>` — attribute VALUES are always
    // quote-terminated (e.g. xmlns="http://www.w3.org/2000/svg" ends in a
    // quote, not a slash, despite the slashes inside it), so a trailing `/`
    // here can only be a genuine self-close, never a slash that happened to
    // occur inside some attribute's value.
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

// Every rendered row, in document (== visible top-to-bottom) order — a `div`
// specifically, so this never accidentally matches something else that merely
// carries a "treerow"-prefixed class.
function rowsOf(markup: string): PNode[] {
  return findAll(parseFragment(markup), (n) => n.tag === "div" && hasClass(n, "treerow"));
}

// Derived from the real component rather than hand-copied: the four
// MethodKindTag labels include non-ASCII arrows (S←, C→, B⇄), and re-typing
// those by hand risks a transcription slip that would make this test wrong
// independent of whatever request-tree.tsx actually renders. Rendering the
// same component in isolation and reading back its own text ties the
// assertion to Tag.tsx's real output instead of a second, hand-maintained copy
// of it.
function labelFor(kind: MethodKind): string {
  return parseFragment(renderToStaticMarkup(<MethodKindTag kind={kind} />))[0].text;
}

describe("request tree rows: direct-child CSS contract", () => {
  // app-tokens.css reveals a row's buttons and hides its count via
  // `.treerow:hover > .rowbtns` / `.treerow:hover > .rowmeta` — both DIRECT
  // child selectors. A wrapper introduced around either span in a future
  // refactor (e.g. a flex container added "just for layout") would keep both
  // classes present in the markup somewhere, so a naive `toContain("rowbtns")`
  // check would keep passing right up until hover-reveal silently broke in the
  // browser. This test instead asserts row.children — ONE nesting level below
  // the row div — actually contains them.
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
    expect(rows).toHaveLength(8); // one per fixture node — sanity that nothing collapsed unexpectedly

    // How this fails: wrap the RowDeleteButton/rename button pair in an extra
    // <span className="rowActions"> inside renderRequestRow, and "rowbtns"
    // becomes a GRANDCHILD of .treerow — row.children (one level down) no
    // longer contains it, `direct` below comes back empty, and toHaveLength(1)
    // fails with "expected [] to have length 1" rather than silently passing.
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
    // Document order == flatten()'s depth-first visit of `roots` with both
    // folders in FULLY_EXPANDED: Ping, Calls, Calls/SayHello, Calls/ListHellos,
    // Calls/SendHellos, Calls/Chat, Calls/Admin, Calls/Admin/Purge.
    const [rowPing, rowCalls, rowSayHello, , , , rowAdmin, rowPurge] = rows;

    for (const row of [rowCalls, rowAdmin]) {
      const direct = row.children.filter((c) => hasClass(c, "rowmeta"));
      expect(direct).toHaveLength(1); // same direct-child proof as rowbtns above
    }
    // Request rows never render a count at all — proving the assertion above
    // is about FOLDERS specifically, not "rowmeta happens to be direct
    // whenever it's present".
    for (const row of [rowPing, rowSayHello, rowPurge]) {
      expect(row.children.some((c) => hasClass(c, "rowmeta"))).toBe(false);
    }
  });
});

describe("request tree rows: folder row", () => {
  it("renders its child count, and its gear/plus/trash buttons by title", () => {
    const markup = renderToStaticMarkup(
      <Tree
        adapter={adapter}
        renderRow={rowRendererWith(noopCallbacks)}
        expanded={FULLY_EXPANDED}
        aria-label="Test tree"
      />
    );
    const [, rowCalls, , , , , rowAdmin] = rowsOf(markup);

    // Calls has 5 direct children (4 requests + the Admin folder); Admin has 1
    // (Purge) — a folder's own count is its direct children only, never a
    // recursive descendant count (request-tree.tsx's getTreeItem comment).
    expect(rowCalls.children.find((c) => hasClass(c, "rowmeta"))?.text).toBe("5");
    expect(rowAdmin.children.find((c) => hasClass(c, "rowmeta"))?.text).toBe("1");

    for (const row of [rowCalls, rowAdmin]) {
      const titles = findAll(row.children, (n) => n.tag === "button").map((b) => b.attrs.title);
      expect(titles).toEqual(["Folder metadata", "Add request", "Delete folder"]);
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
      ["treerow"], // Ping — none of the three
      ["treerow"], // Calls
      ["treerow"], // Calls/SayHello
      ["treerow", "on"], // Calls/ListHellos — active only
      ["treerow", "sel"], // Calls/SendHellos — selected only
      ["treerow", "foc"], // Calls/Chat — focused only
      ["treerow"], // Calls/Admin
      ["treerow"], // Calls/Admin/Purge
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
    // clsx(...) argument order in TreeRow.tsx is ("treerow", sel, foc, on).
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
    // Ping(0) Calls(0) SayHello(1) ListHellos(1) SendHellos(1) Chat(1) Admin(1) Purge(2)
    expect(guideCounts).toEqual([0, 0, 1, 1, 1, 1, 1, 2]);
  });
});

describe("request tree rows: renaming", () => {
  it("shows an <input> instead of the plain name, and keeps rendering its buttons", () => {
    const key = "Calls/SayHello";
    const markup = renderToStaticMarkup(
      <Tree
        adapter={adapter}
        renderRow={rowRendererWith({ ...noopCallbacks, renamingKey: key })}
        expanded={FULLY_EXPANDED}
        renamingId={key}
        aria-label="Test tree"
      />
    );
    const [, , rowSayHello] = rowsOf(markup);

    expect(findAll(rowSayHello.children, (n) => n.tag === "input")).toHaveLength(1);
    expect(rowSayHello.children.some((c) => c.text === "SayHello")).toBe(false);

    // Buttons are unconditional in renderRequestRow today (EditableName swaps
    // only the name itself) — pinning that they SURVIVE a rename, not just
    // that the input appears.
    const titles = findAll(rowSayHello.children, (n) => n.tag === "button").map((b) => b.attrs.title);
    expect(titles).toEqual(["Rename request", "Delete request"]);
  });
});
