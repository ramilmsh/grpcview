import { describe, expect, it } from "vitest";
import type { TreeRowModel } from "./types";
import type { FlatTree } from "./flatten";
import { targetIndex, parentIndex, firstChildIndex, type MoveTarget } from "./navigate";

// navigate.ts only ever reads a row's `.id`, `.parentId`, and its position in the
// array — never `.depth`/`.expandable`/`.expanded`/`.posInSet`/`.setSize` — so
// this fixture leaves those at a constant rather than computing them, the same
// "only populate what's actually read" reasoning as selection.test.ts's own
// row() helper. `depth` takes an explicit override in the one test below that
// needs to make a point about it (firstChildIndex's parentId-not-depth
// adversarial case).
const row = (id: string, parentId: string | null = null, depth = 0): TreeRowModel<string> => ({
  node: id,
  id,
  depth,
  parentId,
  expandable: false,
  expanded: false,
  posInSet: 1,
  setSize: 1,
});

function flatOf(rows: TreeRowModel<string>[]): FlatTree<string> {
  return { rows, indexById: new Map(rows.map((r, i) => [r.id, i])), defaultExpanded: [] };
}

// A flat, unnested run of 10 rows (indices 0-9) — enough to exercise clamping at
// both ends and paging without every test having to restate the shape. Nesting is
// irrelevant to targetIndex (it only ever moves by ARRAY position), so these are
// all "roots" in name only.
const TEN = flatOf(["A", "B", "C", "D", "E", "F", "G", "H", "I", "J"].map((id) => row(id)));

describe("targetIndex: first/last", () => {
  it("first is always index 0, regardless of fromId", () => {
    expect(targetIndex(TEN, "F", "first", 5)).toBe(0);
    expect(targetIndex(TEN, null, "first", 5)).toBe(0);
  });

  it("last is always the final index, regardless of fromId", () => {
    expect(targetIndex(TEN, "C", "last", 5)).toBe(9);
    expect(targetIndex(TEN, null, "last", 5)).toBe(9);
  });
});

describe("targetIndex: down/up — single-row moves, clamped rather than wrapped", () => {
  it("down moves to the next row", () => {
    expect(targetIndex(TEN, "C", "down", 5)).toBe(3); // C(2) -> D(3)
  });

  it("down from the last row stays put — no wrap to the first", () => {
    expect(targetIndex(TEN, "J", "down", 5)).toBe(9);
  });

  it("up moves to the previous row", () => {
    expect(targetIndex(TEN, "G", "up", 5)).toBe(5); // G(6) -> F(5)
  });

  it("up from the first row stays put — no wrap to the last", () => {
    expect(targetIndex(TEN, "A", "up", 5)).toBe(0);
  });
});

describe("targetIndex: down/up — null or unknown fromId", () => {
  it("down with fromId=null starts from the first row", () => {
    expect(targetIndex(TEN, null, "down", 5)).toBe(0);
  });

  it("up with fromId=null starts from the last row", () => {
    expect(targetIndex(TEN, null, "up", 5)).toBe(9);
  });

  it("an unknown fromId (no row by that id) is treated exactly like null", () => {
    expect(targetIndex(TEN, "ghost", "down", 5)).toBe(0);
    expect(targetIndex(TEN, "ghost", "up", 5)).toBe(9);
  });
});

describe("targetIndex: pageUp/pageDown", () => {
  it("pageDown moves forward by rowsPerPage rows", () => {
    expect(targetIndex(TEN, "C", "pageDown", 3)).toBe(5); // C(2) + 3 -> F(5)
  });

  it("pageDown clamps at the last row rather than overshooting", () => {
    expect(targetIndex(TEN, "H", "pageDown", 3)).toBe(9); // H(7) + 3 = 10 -> clamp 9
  });

  it("pageUp moves backward by rowsPerPage rows", () => {
    expect(targetIndex(TEN, "G", "pageUp", 3)).toBe(3); // G(6) - 3 -> D(3)
  });

  it("pageUp clamps at the first row rather than undershooting", () => {
    expect(targetIndex(TEN, "C", "pageUp", 3)).toBe(0); // C(2) - 3 = -1 -> clamp 0
  });

  it("a rowsPerPage of 0 still moves exactly one row — paging never gets stuck", () => {
    expect(targetIndex(TEN, "C", "pageDown", 0)).toBe(3);
    expect(targetIndex(TEN, "C", "pageUp", 0)).toBe(1);
  });

  it("a rowsPerPage of 1 behaves identically to 0 — both clamp to a one-row stride", () => {
    expect(targetIndex(TEN, "C", "pageDown", 1)).toBe(3);
    expect(targetIndex(TEN, "C", "pageUp", 1)).toBe(1);
  });

  it("pageDown/pageUp with a null fromId fall back the same way down/up do, before applying the stride", () => {
    // pageDown: as if starting one-before-the-first-row (virtual index -1), then + stride.
    expect(targetIndex(TEN, null, "pageDown", 3)).toBe(2);
    // pageUp: as if starting one-after-the-last-row (virtual index rowCount=10), then - stride.
    expect(targetIndex(TEN, null, "pageUp", 3)).toBe(7);
  });
});

describe("targetIndex: empty tree", () => {
  const empty = flatOf([]);
  const everyTarget: MoveTarget[] = ["first", "last", "up", "down", "pageUp", "pageDown"];

  it("returns null for every move target, with either a null or a non-empty fromId", () => {
    for (const to of everyTarget) {
      expect(targetIndex(empty, null, to, 5)).toBeNull();
      expect(targetIndex(empty, "anything", to, 5)).toBeNull();
    }
  });
});

describe("parentIndex", () => {
  it("returns the parent row's index for a nested row", () => {
    const flat = flatOf([row("folder-a"), row("a1", "folder-a"), row("a2", "folder-a")]);
    expect(parentIndex(flat, "a1")).toBe(0);
    expect(parentIndex(flat, "a2")).toBe(0);
  });

  it("returns null for a root row — there is no parent row to focus", () => {
    const flat = flatOf([row("folder-a"), row("a1", "folder-a")]);
    expect(parentIndex(flat, "folder-a")).toBeNull();
  });

  it("returns null when id isn't a currently visible row", () => {
    const flat = flatOf([row("folder-a"), row("a1", "folder-a")]);
    expect(parentIndex(flat, "not-a-real-id")).toBeNull();
  });

  it("returns null (defensively) rather than throwing when parentId points at no row of its own", () => {
    // A malformed FlatTree — flatten() itself never produces this (a visible
    // child's parent is always visible too) — proving the `?? null` fallback
    // degrades safely instead of crashing an arrow-key press.
    const flat = flatOf([row("orphan", "missing-parent")]);
    expect(parentIndex(flat, "orphan")).toBeNull();
  });
});

describe("firstChildIndex", () => {
  it("returns the index right after an expanded folder with visible children", () => {
    const flat = flatOf([row("folder-a"), row("a1", "folder-a"), row("a2", "folder-a")]);
    expect(firstChildIndex(flat, "folder-a")).toBe(1);
  });

  it("returns null for a collapsed folder — its children were never made into rows at all", () => {
    // "collapsed" at the FlatTree level just means: nothing in `rows` carries
    // parentId "folder-b". Compare folder-a above, which DOES have a1 right
    // after it with a matching parentId.
    const flat = flatOf([row("folder-a"), row("a1", "folder-a"), row("folder-b"), row("sibling-of-b")]);
    expect(firstChildIndex(flat, "folder-b")).toBeNull();
  });

  it("returns null for a childless (empty) expanded folder", () => {
    const flat = flatOf([row("empty-folder"), row("next-root")]);
    expect(firstChildIndex(flat, "empty-folder")).toBeNull();
  });

  it("returns null for a leaf", () => {
    const flat = flatOf([row("leaf-a"), row("leaf-b")]);
    expect(firstChildIndex(flat, "leaf-a")).toBeNull();
  });

  it("returns null when id is the very last row — there is no next row to even check", () => {
    const flat = flatOf([row("only-root")]);
    expect(firstChildIndex(flat, "only-root")).toBeNull();
  });

  it("returns null when id isn't present in the tree at all", () => {
    const flat = flatOf([row("some-root")]);
    expect(firstChildIndex(flat, "ghost")).toBeNull();
  });

  it("is not fooled by an unrelated row that merely sits at parent-depth+1 (proves parentId, not depth, decides this)", () => {
    const flat = flatOf([
      row("root-a", null, 0),
      row("folder-b", "root-a", 1), // COLLAPSED: contributes no rows of its own
      // NOT folder-b's child — parented by root-a instead — despite sitting at
      // depth 2, exactly the depth a real child of folder-b (depth 1) would
      // have. A depth-only check (`nextRow.depth === thisRow.depth + 1`) would
      // wrongly call this folder-b's first child; consulting parentId directly
      // does not.
      row("decoy", "root-a", 2),
    ]);
    expect(firstChildIndex(flat, "folder-b")).toBeNull();
  });
});
