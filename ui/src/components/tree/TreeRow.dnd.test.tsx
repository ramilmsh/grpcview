import { describe, it } from "node:test";
import { expect } from "expect";
import { renderToStaticMarkup } from "react-dom/server";
import { flatten } from "./flatten";
import { TreeRow } from "./TreeRow";
import type { TreeAdapter, TreeRowState } from "./types";

interface Node {
  id: string;
  folder?: boolean;
  kids?: Node[];
}

const tree: Node[] = [{ id: "F", folder: true, kids: [{ id: "r" }] }];

const adapter: TreeAdapter<Node> = {
  getId: (node) => node.id,
  getChildren: (node) => (node === undefined ? tree : node.kids ?? []),
  getCollapsibleState: (node) => (node.folder ? "collapsed" : "none"),
  getTreeItem: (node) => ({ label: node.id }),
  getTypeaheadLabel: (node) => node.id,
};

const flat = flatten(adapter, new Set(["F"]));
const rowOf = (id: string) => flat.rows[flat.indexById.get(id) ?? -1];

function render(
  id: string,
  drop: { dropTarget: TreeRowState["dropTarget"]; dropDepth?: number; dragging?: boolean }
): string {
  return renderToStaticMarkup(
    <TreeRow
      row={rowOf(id)}
      domId="test-row"
      dataIndex={3}
      rowRef={() => {}}
      adapter={adapter}
      selected={false}
      focused={false}
      active={false}
      renaming={false}
      dropTarget={drop.dropTarget}
      dropDepth={drop.dropDepth ?? 0}
      dragging={drop.dragging ?? false}
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
  );
}

describe("TreeRow: drag and drop chrome", () => {
  it("is draggable and carries the data-index the delegated handlers recover it by", () => {
    const markup = render("r", { dropTarget: null });
    expect(markup).toContain("draggable=\"true\"");
    expect(markup).toContain("data-index=\"3\"");
  });

  it("is NOT draggable while renaming — the row is hosting a text input", () => {
    const markup = renderToStaticMarkup(
      <TreeRow
        row={rowOf("r")}
        domId="test-row"
        dataIndex={0}
        rowRef={() => {}}
        adapter={adapter}
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
    );
    expect(markup).toContain("draggable=\"false\"");
  });

  it("paints nothing extra when there is no drop in progress", () => {
    const markup = render("r", { dropTarget: null });
    expect(markup).not.toContain("dropline");
    expect(markup).not.toContain("dropinto");
    expect(markup).not.toContain("dragging");
  });

  it("an `into` drop washes the whole row and draws no line", () => {
    const markup = render("F", { dropTarget: "into", dropDepth: 1 });
    expect(markup).toContain("treerow dropinto");
    expect(markup).not.toContain("dropline");
  });

  it("a `before`/`after` drop draws a line at that edge, indented to the drop depth", () => {
    expect(render("r", { dropTarget: "before", dropDepth: 1 })).toContain(
      '<span class="dropline before" style="--drop-depth:1"></span>'
    );
    expect(render("r", { dropTarget: "after", dropDepth: 2 })).toContain(
      '<span class="dropline after" style="--drop-depth:2"></span>'
    );
  });

  it("a dragged row reads as in-flight", () => {
    expect(render("r", { dropTarget: null, dragging: true })).toContain("treerow dragging");
  });
});
