import { describe, it } from "node:test";
import { expect } from "expect";
import { renderToStaticMarkup } from "react-dom/server";
import { Tree } from "./Tree";
import type { PortableTreeAdapter } from "./types";

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

    for (const label of ["Alpha", "Beta", "Charlie", "Alpha One", "Alpha Two"]) {
      expect(markup).toContain(label);
    }

    expect(markup).toContain("2 items");
    expect(markup).toContain("1 item");

    expect(markup).toContain('role="tree"');
    expect(markup).toContain('aria-label="Test tree"');
    expect(markup).toContain('aria-multiselectable="true"');
    expect(markup).toContain('aria-expanded="true"');
    expect(markup).toContain('aria-expanded="false"');
    expect(markup).toContain('aria-level="2"');

    expect(markup).not.toContain("Charlie One");
    const rowCount = markup.match(/role="treeitem"/g)?.length ?? 0;
    expect(rowCount).toBe(5);
  });

  it("paints .on only on the row matching activeId, independent of selection/focus", () => {
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
      "treerow",
      "treerow on",
      "treerow sel foc",
    ]);
  });
});
