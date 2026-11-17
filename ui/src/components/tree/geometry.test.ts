import { describe, it } from "node:test";
import { expect } from "expect";
import { flatten } from "./flatten";
import {
  DEFAULT_INDENT,
  ROW_PAD,
  TWISTIE_WIDTH,
  depthLeft,
  guideLeft,
  guideLevels,
} from "./geometry";
import type { TreeAdapter } from "./types";

interface N {
  id: string;
  children?: N[];
}

const adapterOf = (roots: N[]): TreeAdapter<N> => ({
  getId: (n) => n.id,
  getChildren: (n) => (n ? (n.children ?? []) : roots),
  getCollapsibleState: (n) => (n.children ? "expanded" : "none"),
  getParent: () => undefined,
  getTreeItem: (n) => ({ label: n.id }),
  getTypeaheadLabel: (n) => n.id,
});

describe("geometry", () => {
  it("steps 18px per level, with v4's columns", () => {
    expect(DEFAULT_INDENT).toBe(18);
    // content (folder icon / request tag) = pad + depth*18 + twistie + 8px gap
    const content = (d: number) =>
      depthLeft(d, DEFAULT_INDENT) + TWISTIE_WIDTH + 8;
    expect([0, 1, 2].map(content)).toEqual([30, 48, 66]);
    expect([0, 1, 2].map((l) => guideLeft(l, DEFAULT_INDENT))).toEqual([
      15, 33, 51,
    ]);
    expect(depthLeft(0, DEFAULT_INDENT)).toBe(ROW_PAD);
  });

  it("a level's guide sits inside its folder's twistie column", () => {
    for (const level of [0, 1, 3]) {
      const g = guideLeft(level, DEFAULT_INDENT);
      expect(g).toBeGreaterThan(depthLeft(level, DEFAULT_INDENT));
      expect(g).toBeLessThan(depthLeft(level, DEFAULT_INDENT) + TWISTIE_WIDTH);
    }
  });

  it("a folder's guide covers exactly its descendant rows", () => {
    const roots: N[] = [
      { id: "a" },
      {
        id: "F",
        children: [{ id: "F/x" }, { id: "G", children: [{ id: "G/y" }] }],
      },
      { id: "b" },
      { id: "c" },
    ];
    const flat = flatten(adapterOf(roots), new Set(["F", "G"]));
    const withLevel0 = flat.rows
      .filter((r) => guideLevels(r.depth).includes(0))
      .map((r) => r.id);
    expect(withLevel0).toEqual(["F/x", "G", "G/y"]);
    const withLevel1 = flat.rows
      .filter((r) => guideLevels(r.depth).includes(1))
      .map((r) => r.id);
    expect(withLevel1).toEqual(["G/y"]);
  });
});
