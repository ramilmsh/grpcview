import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { Tree } from "./Tree";
import type { PortableTreeAdapter } from "./types";

// T0's acceptance test (tree-rewrite-plan.md §Phases, T0): "a throwaway declarative
// provider renders a readable tree with no renderRow at all" — proving the PORTABLE
// tier (the default row built from getTreeItem) is real and works standalone,
// before anything in this app depends on it (the descriptor explorer, plan
// §"Second consumer", is the actual second provider). The fixture below is
// deliberately unrelated to gRPC so this test can't accidentally pass only because
// it's exercising domain knowledge the component isn't supposed to have (enduring
// decision 1).
//
// renderToStaticMarkup, not ReactDOM.render: vitest.config.ts runs tests in node,
// with no jsdom, and renderToStaticMarkup is the one React render path that needs
// no DOM. `expanded` is passed CONTROLLED. useTreeState's default-expansion
// resolution (resolveExpansion, flatten.ts) runs inside a useMemo, not an effect,
// so it DOES run here — but useTreeState's follow-up useEffect (seenDefaults
// reconciliation + pushing the resolved set back to a controlled host) never does,
// since there is no commit phase. Both fixture folders below use "collapsed"
// rather than "expanded" for getCollapsibleState() specifically so resolveExpansion
// has nothing to fold in regardless: it keeps the test's outcome resting entirely
// on the controlled `expanded` set, with no dependence on default-expansion
// resolution one way or the other.

interface FixtureNode {
  id: string;
  label: string;
  description?: string;
  kids?: FixtureNode[];
}

const roots: FixtureNode[] = [
  {
    id: "folder-a",
    label: "Alpha",
    description: "2 items",
    kids: [
      { id: "a1", label: "Alpha One" },
      { id: "a2", label: "Alpha Two" },
    ],
  },
  {
    id: "folder-c",
    label: "Charlie",
    description: "1 item",
    kids: [{ id: "c1", label: "Charlie One" }],
  },
  { id: "leaf-b", label: "Beta" },
];

const adapter: PortableTreeAdapter<FixtureNode> = {
  getId: (node) => node.id,
  getChildren: (node) => (node === undefined ? roots : node.kids ?? []),
  getCollapsibleState: (node) => (node.kids && node.kids.length > 0 ? "collapsed" : "none"),
  getTreeItem: (node) => ({ label: node.label, description: node.description }),
  getTypeaheadLabel: (node) => node.label,
};

describe("Tree: portable tier", () => {
  it("renders a readable tree from getTreeItem with no renderRow supplied", () => {
    const markup = renderToStaticMarkup(
      <Tree adapter={adapter} expanded={new Set(["folder-a"])} aria-label="Test tree" />
    );

    // labels: every root's own label, plus the two children of the one expanded folder.
    for (const label of ["Alpha", "Beta", "Charlie", "Alpha One", "Alpha Two"]) {
      expect(markup).toContain(label);
    }

    // descriptions: the dimmed trailing text getTreeItem() supplies.
    expect(markup).toContain("2 items");
    expect(markup).toContain("1 item");

    // role/aria: the container and its rows, with expanded/collapsed reflected
    // per folder (folder-a is in `expanded`, folder-c is not).
    expect(markup).toContain('role="tree"');
    expect(markup).toContain('aria-label="Test tree"');
    expect(markup).toContain('aria-expanded="true"');
    expect(markup).toContain('aria-expanded="false"');
    expect(markup).toContain('aria-level="2"'); // folder-a's children, at depth 1

    // nested rows only for the EXPANDED parent: folder-a's two children are rows,
    // folder-c's child is not, proving descent is gated on `expanded` — not on
    // getCollapsibleState() alone, and not on a folder merely existing.
    expect(markup).not.toContain("Charlie One");
    const rowCount = markup.match(/role="treeitem"/g)?.length ?? 0;
    expect(rowCount).toBe(5); // folder-a, a1, a2, folder-c, leaf-b — not c1
  });

  // activeId (added for the CollectionPanel swap-in, tree-rewrite-plan.md's
  // TreeRowState.active): the CollectionPanel integration needs a channel for
  // "this row is the open tab" (`.treerow.on`) that is independent of the tree's
  // own selection/focus — this is the portable tier's version of that same proof,
  // since active/.on is painted by the shell regardless of which tier renders the
  // row content.
  it("paints .on only on the row matching activeId, independent of selection/focus", () => {
    // All three roots collapsed (expanded={new Set()}), so the flat rows are
    // exactly folder-a, folder-c, leaf-b, in that order — one class list per row,
    // read off in DOM order rather than sliced out by surrounding text.
    const markup = renderToStaticMarkup(
      <Tree
        adapter={adapter}
        expanded={new Set()}
        selection={["leaf-b"]}
        focused="leaf-b"
        activeId="folder-c"
      />
    );
    const rowClasses = [...markup.matchAll(/<div class="(treerow[^"]*)"/g)].map((m) => m[1]);
    expect(rowClasses).toEqual([
      "treerow", // folder-a: none of the three flags
      "treerow on", // folder-c: activeId match, not selected/focused
      "treerow sel foc", // leaf-b: selected + focused, but not the active row
    ]);
  });
});
