import { describe, expect, it } from "vitest";
import { flatten } from "./flatten";
import type { TreeAdapter } from "./types";
import {
  autoScrollDelta,
  draggedSubtreeIds,
  dropTargetAt,
  isNoOpDrop,
  nextVisibleSiblingId,
  resolveDrop,
  zoneForOffset,
} from "./dnd";

// The fixture is built by running the REAL flatten() over a small adapter rather
// than hand-writing TreeRowModels (navigate.test.ts / selection.test.ts do the
// latter, because those modules read only `.id`/`.parentId`/array position). dnd.ts
// reads `.depth`, `.expandable` AND `.expanded` together, and the whole point of
// several assertions below is how those three interact — an "expanded folder whose
// next row is its own first child" is exactly the shape a hand-written fixture
// gets subtly wrong.
//
// Shape (indices are the flat row order the assertions below cite):
//
//   0  F1        folder, EXPANDED
//   1    r1      request
//   2    F2      folder, COLLAPSED (its child r2 is therefore not a row at all)
//   3  F3        folder, EXPANDED but EMPTY
//   4  r3        request
//   5  r4        request

interface Node {
  id: string;
  folder?: boolean;
  kids?: Node[];
}

const tree: Node[] = [
  {
    id: "F1",
    folder: true,
    kids: [{ id: "r1" }, { id: "F2", folder: true, kids: [{ id: "r2" }] }],
  },
  { id: "F3", folder: true, kids: [] },
  { id: "r3" },
  { id: "r4" },
];

const adapter: TreeAdapter<Node> = {
  getId: (node) => node.id,
  getChildren: (node) => (node === undefined ? tree : node.kids ?? []),
  // "collapsed" for every folder, never "expanded": flatten() gates descent on the
  // `expanded` SET alone, and reporting a per-node default would only add
  // defaultExpanded noise this fixture has no use for.
  getCollapsibleState: (node) => (node.folder ? "collapsed" : "none"),
  getTreeItem: (node) => ({ label: node.id }),
  getTypeaheadLabel: (node) => node.id,
};

const flat = flatten(adapter, new Set(["F1", "F3"]));

describe("zoneForOffset: a leaf splits in half", () => {
  it("top half is before, bottom half is after", () => {
    const leaf = { rowHeight: 22, expandable: false };
    expect(zoneForOffset({ ...leaf, offsetY: 0 })).toBe("before");
    expect(zoneForOffset({ ...leaf, offsetY: 10.9 })).toBe("before");
    expect(zoneForOffset({ ...leaf, offsetY: 11.1 })).toBe("after");
    expect(zoneForOffset({ ...leaf, offsetY: 22 })).toBe("after");
  });

  it("the exact midpoint belongs to the LOWER half (after)", () => {
    expect(zoneForOffset({ rowHeight: 22, expandable: false, offsetY: 11 })).toBe("after");
  });

  it("never produces into, wherever the pointer is", () => {
    for (let offsetY = -4; offsetY <= 26; offsetY += 0.5) {
      expect(zoneForOffset({ rowHeight: 22, expandable: false, offsetY })).not.toBe("into");
    }
  });
});

describe("zoneForOffset: a folder splits into quarters (listView.js's getTargetSector)", () => {
  const folder = { rowHeight: 22, expandable: true };

  it("outer quarters are before/after and the middle half is into", () => {
    expect(zoneForOffset({ ...folder, offsetY: 0 })).toBe("before");
    expect(zoneForOffset({ ...folder, offsetY: 5.4 })).toBe("before");
    expect(zoneForOffset({ ...folder, offsetY: 11 })).toBe("into");
    expect(zoneForOffset({ ...folder, offsetY: 16.4 })).toBe("into");
    expect(zoneForOffset({ ...folder, offsetY: 17 })).toBe("after");
    expect(zoneForOffset({ ...folder, offsetY: 22 })).toBe("after");
  });

  it("an exact quarter boundary belongs to the LOWER sector, per the floor", () => {
    // 5.5 = exactly 0.25 of 22 -> sector 1 -> into (not before)
    expect(zoneForOffset({ ...folder, offsetY: 5.5 })).toBe("into");
    // 16.5 = exactly 0.75 of 22 -> sector 3 -> after (not into)
    expect(zoneForOffset({ ...folder, offsetY: 16.5 })).toBe("after");
  });

  it("clamps a pointer outside the row's own box", () => {
    expect(zoneForOffset({ ...folder, offsetY: -8 })).toBe("before");
    expect(zoneForOffset({ ...folder, offsetY: 40 })).toBe("after");
  });

  it("degrades to before for a row with no measurable height", () => {
    expect(zoneForOffset({ rowHeight: 0, expandable: true, offsetY: 0 })).toBe("before");
  });
});

describe("nextVisibleSiblingId", () => {
  it("skips an expanded folder's whole subtree rather than taking the next row", () => {
    // F1's next ROW is its child r1; its next SIBLING is F3, two rows further on.
    expect(nextVisibleSiblingId(flat, 0)).toBe("F3");
  });

  it("finds an ordinary sibling one row along", () => {
    expect(nextVisibleSiblingId(flat, 1)).toBe("F2"); // r1 -> F2
    expect(nextVisibleSiblingId(flat, 4)).toBe("r4"); // r3 -> r4
  });

  it("is null for the last child of a nested folder, not the row that follows it", () => {
    // The row after F2 is F3, which is at a SHALLOWER depth and a different parent.
    expect(nextVisibleSiblingId(flat, 2)).toBeNull();
  });

  it("is null for the last row of the tree", () => {
    expect(nextVisibleSiblingId(flat, 5)).toBeNull();
  });

  it("is null for an index that names no row", () => {
    expect(nextVisibleSiblingId(flat, 99)).toBeNull();
  });
});

describe("resolveDrop: into", () => {
  it("appends inside the folder", () => {
    expect(resolveDrop(flat, 0, "into")).toEqual({ parentId: "F1", beforeId: null, depth: 1 });
  });

  it("works for a COLLAPSED folder too — the destination need not be open", () => {
    expect(resolveDrop(flat, 2, "into")).toEqual({ parentId: "F2", beforeId: null, depth: 2 });
  });

  it("refuses a leaf: there is no inside of a request", () => {
    expect(resolveDrop(flat, 4, "into")).toBeNull();
  });
});

describe("resolveDrop: before/after between rows", () => {
  it("before a row inserts ahead of it, in that row's own parent", () => {
    expect(resolveDrop(flat, 1, "before")).toEqual({ parentId: "F1", beforeId: "r1", depth: 1 });
    expect(resolveDrop(flat, 3, "before")).toEqual({ parentId: null, beforeId: "F3", depth: 0 });
  });

  it("after a row inserts ahead of its next visible SIBLING", () => {
    expect(resolveDrop(flat, 1, "after")).toEqual({ parentId: "F1", beforeId: "F2", depth: 1 });
    expect(resolveDrop(flat, 4, "after")).toEqual({ parentId: null, beforeId: "r4", depth: 0 });
  });

  it("after the last child of a folder appends inside that folder", () => {
    expect(resolveDrop(flat, 2, "after")).toEqual({ parentId: "F1", beforeId: null, depth: 1 });
  });

  it("after the last root row appends at the collection root", () => {
    expect(resolveDrop(flat, 5, "after")).toEqual({ parentId: null, beforeId: null, depth: 0 });
  });

  it("is null for an index that names no row", () => {
    expect(resolveDrop(flat, 99, "before")).toBeNull();
  });
});

describe("resolveDrop: after an EXPANDED folder resolves INSIDE it, at position 0", () => {
  it("targets the folder's first child, not the folder's next sibling", () => {
    // The decision recorded in dnd.ts and in the plan's §"What T6b settled": the
    // indicator sits between F1 and r1 on screen, so it means "ahead of r1".
    expect(resolveDrop(flat, 0, "after")).toEqual({ parentId: "F1", beforeId: "r1", depth: 1 });
  });

  it("degrades to append-inside for an expanded folder with no visible children", () => {
    // F3 is expanded and empty, so the row after it (r3) is NOT its child.
    expect(resolveDrop(flat, 3, "after")).toEqual({ parentId: "F3", beforeId: null, depth: 1 });
  });

  it("a COLLAPSED folder keeps the ordinary sibling reading", () => {
    // F2 is collapsed, so `after` it means "in F1, after F2" — i.e. append in F1.
    expect(resolveDrop(flat, 2, "after")).toEqual({ parentId: "F1", beforeId: null, depth: 1 });
  });
});

describe("draggedSubtreeIds", () => {
  it("includes each dragged row and its whole VISIBLE subtree", () => {
    expect([...draggedSubtreeIds(flat, ["F1"])].sort()).toEqual(["F1", "F2", "r1"]);
  });

  it("does not reach a collapsed folder's children — they are not rows", () => {
    // F2 is collapsed, so r2 is nowhere in the flat array and cannot be a target.
    expect(draggedSubtreeIds(flat, ["F2"]).has("r2")).toBe(false);
    expect([...draggedSubtreeIds(flat, ["F2"])]).toEqual(["F2"]);
  });

  it("unions a multi-row drag", () => {
    expect([...draggedSubtreeIds(flat, ["r3", "F3"])].sort()).toEqual(["F3", "r3"]);
  });
});

describe("isNoOpDrop", () => {
  it("is true for inserting a row before itself", () => {
    expect(isNoOpDrop(flat, ["r3"], { parentId: null, beforeId: "r3", depth: 0 })).toBe(true);
  });

  it("is true for inserting a row before its own next sibling", () => {
    expect(isNoOpDrop(flat, ["r3"], { parentId: null, beforeId: "r4", depth: 0 })).toBe(true);
  });

  it("is true for appending a row that is already the last child", () => {
    expect(isNoOpDrop(flat, ["F2"], { parentId: "F1", beforeId: null, depth: 1 })).toBe(true);
    expect(isNoOpDrop(flat, ["r4"], { parentId: null, beforeId: null, depth: 0 })).toBe(true);
  });

  it("is false for appending a row that is NOT already last", () => {
    expect(isNoOpDrop(flat, ["r1"], { parentId: "F1", beforeId: null, depth: 1 })).toBe(false);
  });

  it("is false for any reparent, even to the same position index", () => {
    expect(isNoOpDrop(flat, ["r3"], { parentId: "F1", beforeId: null, depth: 1 })).toBe(false);
  });

  it("is false for a multi-row drag, which has no unchanged position", () => {
    expect(isNoOpDrop(flat, ["r3", "r4"], { parentId: null, beforeId: "r3", depth: 0 })).toBe(false);
  });

  it("is false for a dragged id that is no longer a row", () => {
    expect(isNoOpDrop(flat, ["gone"], { parentId: null, beforeId: null, depth: 0 })).toBe(false);
  });
});

describe("dropTargetAt: structural rejections", () => {
  it("rejects a target inside the dragged set's own subtree", () => {
    expect(dropTargetAt(flat, 0, "into", ["F1"])).toBeNull(); // into itself
    expect(dropTargetAt(flat, 1, "before", ["F1"])).toBeNull(); // before its own child
    expect(dropTargetAt(flat, 2, "after", ["F1"])).toBeNull(); // after its own child folder
  });

  it("rejects a target row that is itself being dragged", () => {
    expect(dropTargetAt(flat, 4, "after", ["r3"])).toBeNull();
    expect(dropTargetAt(flat, 5, "before", ["r4"])).toBeNull();
  });

  it("rejects a no-op drop reached via some OTHER row", () => {
    // r3 already sits immediately ahead of r4, so "before r4" changes nothing.
    expect(dropTargetAt(flat, 5, "before", ["r3"])).toBeNull();
    // F2 is already F1's last child, so appending it inside F1 changes nothing.
    expect(dropTargetAt(flat, 0, "into", ["F2"])).toBeNull();
  });

  it("still accepts moving a row to the OTHER side of an adjacent sibling", () => {
    // The mirror of the first no-op above: r4 currently follows r3, so putting it
    // ahead of r3 is a real reorder, not the same position spelled differently.
    expect(dropTargetAt(flat, 4, "before", ["r4"])).toEqual({
      parentId: null,
      beforeId: "r3",
      depth: 0,
    });
  });

  it("rejects an empty dragged set and an out-of-range target", () => {
    expect(dropTargetAt(flat, 0, "into", [])).toBeNull();
    expect(dropTargetAt(flat, 99, "into", ["r3"])).toBeNull();
  });

  it("rejects into a leaf", () => {
    expect(dropTargetAt(flat, 4, "into", ["r1"])).toBeNull();
  });
});

describe("dropTargetAt: accepted drops", () => {
  it("reparents a root request into a folder", () => {
    expect(dropTargetAt(flat, 0, "into", ["r3"])).toEqual({
      parentId: "F1",
      beforeId: null,
      depth: 1,
    });
  });

  it("reorders within a folder", () => {
    // r1 appended at the end of F1 — it currently sits ahead of F2, so this moves.
    expect(dropTargetAt(flat, 2, "after", ["r1"])).toEqual({
      parentId: "F1",
      beforeId: null,
      depth: 1,
    });
  });

  it("moves a folder out to the root", () => {
    expect(dropTargetAt(flat, 5, "after", ["F2"])).toEqual({
      parentId: null,
      beforeId: null,
      depth: 0,
    });
  });

  it("accepts a multi-row drag whose members are all outside the target's subtree", () => {
    expect(dropTargetAt(flat, 3, "before", ["r3", "r4"])).toEqual({
      parentId: null,
      beforeId: "F3",
      depth: 0,
    });
  });
});

describe("autoScrollDelta", () => {
  // A scrollport spanning y 100..400 (300px tall), so the two 24px edge bands are
  // 100..124 and 376..400.
  const top = 100;
  const bottom = 400;

  it("is zero anywhere away from the edges", () => {
    expect(autoScrollDelta(250, top, bottom)).toBe(0);
    expect(autoScrollDelta(124, top, bottom)).toBe(0);
    expect(autoScrollDelta(376, top, bottom)).toBe(0);
  });

  it("scrolls UP proportionally to how far into the top band the pointer is", () => {
    expect(autoScrollDelta(100, top, bottom)).toBe(-24); // right at the edge
    expect(autoScrollDelta(118, top, bottom)).toBe(-6);
  });

  it("scrolls DOWN proportionally within the bottom band", () => {
    expect(autoScrollDelta(390, top, bottom)).toBe(14);
    expect(autoScrollDelta(400, top, bottom)).toBe(24);
  });

  it("clamps a pointer dragged well outside the scrollport", () => {
    expect(autoScrollDelta(0, top, bottom)).toBe(-28);
    expect(autoScrollDelta(900, top, bottom)).toBe(28);
  });
});
