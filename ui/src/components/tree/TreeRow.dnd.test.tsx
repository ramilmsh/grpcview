import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { flatten } from "./flatten";
import { TreeRow } from "./TreeRow";
import type { TreeAdapter, TreeRowState } from "./types";

// The MARKUP half of T6b, which is all this suite can reach: vitest runs with no
// jsdom (vitest.config.ts, `environment: "node"`), so nothing here dispatches a real
// DragEvent, measures a row's box, or reads getComputedStyle — the geometry and
// validity decisions are covered directly in dnd.test.ts, and the gesture itself
// needs the browser pass. What IS worth pinning is the contract between TreeRow and
// app-tokens.css: the class names and the custom property the indicator rules key
// off. Those are coupled across two files with nothing but convention holding them
// together, and a renamed class fails silently — a drag with no visible indicator.
//
// TreeRow is driven directly rather than through <Tree>, for the same reason the
// rename markup tests are (request-tree.test.tsx): "which row is the drop target" is
// internal drag state a server render has no way to set from outside.

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
    // --drop-depth, not a px value: app-tokens.css multiplies it by --tree-indent, so
    // the indicator's pitch stays the one the indent guides use.
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
