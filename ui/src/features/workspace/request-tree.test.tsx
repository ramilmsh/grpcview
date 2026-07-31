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
  onStartRename: () => {},
  onNewRequestUnder: () => {},
  onDelete: () => {},
  onEditMetadata: () => {},
};

// renderRequestRow needs the callbacks bag threaded through Tree's renderRow
// prop (this is exactly what CollectionPanel.tsx's own `renderRow` closure
// does) — a small factory so a test can substitute a different bag.
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

    // Calls has 5 direct children (4 requests + the Admin folder); Admin has 1
    // (Purge) — a folder's own count is its direct children only, never a
    // recursive descendant count (request-tree.tsx's getTreeItem comment).
    expect(rowCalls.children.find((c) => hasClass(c, "rowmeta"))?.text).toBe("5");
    expect(rowAdmin.children.find((c) => hasClass(c, "rowmeta"))?.text).toBe("1");

    for (const row of [rowCalls, rowAdmin]) {
      const titles = findAll(row.children, (n) => n.tag === "button").map((b) => b.attrs.title);
      // "Rename folder" is T4b's addition, in the request row's ordering habit —
      // pencil immediately before the trash. Folders became renamable at T4a
      // (UpdateFolderRequest.name); before that this row had no pencil at all.
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

      // The name is a plain <span> as of T4b — no EditableName, and therefore no
      // <input> anywhere in a row that isn't renaming. The tree renders the rename
      // box itself now (components/tree/RenameInput.tsx), so a row content renderer
      // holding edit state of its own would be a second, competing rename UI.
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

// T4b: "which row is renaming" is the tree's own INTERNAL state, so there is no
// prop for a server render to set it with — a <Tree> rendered by
// renderToStaticMarkup always starts with nothing renaming. TreeRow is therefore
// driven directly here, which is also the honest level for these assertions: what
// is being pinned is that the tree's rename box wins over a RICH renderRow (this
// module's), which is TreeRow's decision, not Tree's.
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
    // The deliberate deviation from VS Code (plan §"Deliberate deviations" #5):
    // VS Code keeps the file icon beside its edit box, we yield the whole row. So
    // unlike the pre-T4b EditableName arrangement — which swapped only the name and
    // left the buttons up — the MethodKindTag and BOTH row buttons are gone while
    // the box is open.
    expect(findAll(row.children, (n) => n.tag === "button")).toHaveLength(0);
    expect(row.children.some((c) => hasClass(c, "mtag"))).toBe(false);
    expect(row.children.some((c) => hasClass(c, "rowbtns"))).toBe(false);
  });

  it("keeps the row SHELL — indent guides and the twistie column — around the input", () => {
    // The shell is the tree's own chrome, not the content's: dropping it would make
    // the edited row jump out of the indent staircase every other row aligns to.
    // Calls/Admin is a depth-1 EXPANDABLE row, so it proves both halves at once
    // (one guide, plus a twistie that still draws its caret).
    const row = renamingMarkup("Calls/Admin");
    expect(row.children.filter((c) => hasClass(c, "guide"))).toHaveLength(1);
    const twistie = row.children.find((c) => hasClass(c, "twistie"));
    expect(twistie).toBeDefined();
    expect(findAll([twistie as PNode], (n) => n.tag === "svg")).toHaveLength(1);
    expect(findAll(row.children, (n) => n.tag === "input")).toHaveLength(1);
  });

  it("seeds the input from adapter.getTreeItem().label, even for this RICH adapter", () => {
    // The narrow deliberate exception recorded in TreeRow.tsx and plan §"What T4b
    // settled" #2: a rich adapter's getTreeItem is not what renders its rows, but
    // `.label` is still the tree's only adapter-independent answer to "what text is
    // this row's name" — and request-tree.tsx returns the display name there.
    const row = renamingMarkup("Calls/SayHello");
    const input = findAll(row.children, (n) => n.tag === "input")[0];
    expect(input.attrs.value).toBe("SayHello");
    expect(adapter.getTreeItem(rowModel("Calls/SayHello").node).label).toBe("SayHello");
    // Not invalid on open: the sibling list excludes the renamed row itself, so a
    // freshly opened box can never already be colliding.
    expect(input.attrs["aria-invalid"]).toBeUndefined();
    expect(input.classes).not.toContain("rename-invalid");
  });
});

// T1 (keyboard + a11y) has no browser available in this environment, so this is
// the only regression cover it gets: what react-dom/server's OWN markup proves
// about the container/row attributes handleKeyDown depends on, not anything
// about a real keydown actually firing (there is no DOM event dispatch under
// SSR). See the file header above for why the same technique already carries
// T0's own cover.
describe("request tree rows: keyboard + a11y (T1)", () => {
  // The container div (Tree.tsx's outer `.tree`) — found the same way `rowsOf`
  // finds row divs, but by the literal class "tree" rather than "treerow": the
  // two never collide, since `hasClass` checks exact membership in the class
  // LIST, and "tree" is never one of a row's own classes.
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
    // Roving TABINDEX here means one focusable container, not one-tabIndex-
    // per-row (Tree.tsx's own "FOCUS MODEL" comment) — react-dom/server lowers
    // the JSX `tabIndex` prop to the HTML `tabindex` attribute.
    expect(container.attrs.tabindex).toBe("0");

    const [, , rowSayHello] = rowsOf(markup);
    // aria-activedescendant must equal the focused row's OWN dom id — which
    // must NOT be the raw itemKey ("Calls/SayHello"): that string is exactly
    // what Tree.tsx's domIdFor exists to avoid embedding verbatim (an ARIA
    // IDREF may not contain whitespace, and a request/folder name with a
    // space would put one right into a bare-itemKey id).
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
    // domIdFor is a pure function of (this Tree instance's useId, row.id) —
    // rendering the identical tree twice (two unrelated component instances,
    // as two separate renderToStaticMarkup calls are) must still line up
    // row-for-row, or reveal() / focus-driven scrollIntoView lookups (Tree.tsx's
    // rowEls map) would be keyed off ids that silently drift between renders.
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
    // Same 8-row fixture/order as the "active/selected/focused" describe block
    // above: Ping, Calls, Calls/SayHello, Calls/ListHellos, Calls/SendHellos
    // (selected), Calls/Chat (focused — but NOT selected), Calls/Admin, Purge.
    // Only the SELECTED row reads "true"; the focused-but-unselected row reads
    // "false" exactly like every other row — proving aria-selected tracks
    // selection, not focus, even on the one row where the two could be
    // confused for each other.
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
    // Same 8-row fixture/order as "indent guides" above: Ping, Calls — the two
    // ROOTS, so both carry setSize "2" — then Calls's 5 direct children
    // (SayHello, ListHellos, SendHellos, Chat, Admin — setSize "5", posInset
    // "1".."5"), then Admin's own 1 child Purge, which restarts at posInset
    // "1" with setSize "1" rather than continuing Admin's own posInset ("5")
    // or Calls's.
    expect(rows.map((r) => r.attrs["aria-posinset"])).toEqual(["1", "2", "1", "2", "3", "4", "5", "1"]);
    expect(rows.map((r) => r.attrs["aria-setsize"])).toEqual(["2", "2", "5", "5", "5", "5", "5", "1"]);
  });
});
