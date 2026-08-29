import { describe, it } from "node:test";
import { expect } from "expect";
import type { TreeRowModel } from "./types";
import { rangeSelection, replaceSelection, selectAll, toggleSelection } from "./selection";

const row = (id: string): TreeRowModel<string> => ({
  node: id,
  id,
  depth: 0,
  parentId: null,
  expandable: false,
  expanded: false,
  posInSet: 1,
  setSize: 1,
});

const rows = ["A", "B", "C", "D", "E"].map(row);

describe("replaceSelection", () => {
  it("replaces the selection with just the given id", () => {
    expect(replaceSelection("C")).toEqual(["C"]);
  });
});

describe("toggleSelection", () => {
  it("appends an id that isn't selected yet", () => {
    expect(toggleSelection(["A"], "B")).toEqual(["A", "B"]);
  });

  it("removes an id that is already selected, leaving the rest in place", () => {
    expect(toggleSelection(["C", "A", "B"], "A")).toEqual(["C", "B"]);
  });

  it("toggling the last selected id off leaves an empty selection", () => {
    expect(toggleSelection(["A"], "A")).toEqual([]);
  });

  it("round-trips: toggling the same id twice restores the original selection", () => {
    const once = toggleSelection(["A", "B"], "C");
    const twice = toggleSelection(once, "C");
    expect(twice).toEqual(["A", "B"]);
  });
});

describe("rangeSelection", () => {
  it("selects inclusive from anchor to focus in ascending order", () => {
    expect(rangeSelection(rows, "B", "D")).toEqual(["B", "C", "D"]);
  });

  it("selects the same rows for a reversed anchor/focus pair", () => {
    expect(rangeSelection(rows, "D", "B")).toEqual(["B", "C", "D"]);
  });

  it("is a single-row range when anchor and focus are the same row", () => {
    expect(rangeSelection(rows, "C", "C")).toEqual(["C"]);
  });

  it("degenerates to just the focus row when there is no anchor yet (null)", () => {
    expect(rangeSelection(rows, null, "C")).toEqual(["C"]);
  });

  it("degenerates to just the focus row when the anchor id is missing from rows", () => {
    expect(rangeSelection(rows, "Z", "C")).toEqual(["C"]);
  });

  it("selects nothing when the focus row itself isn't in rows", () => {
    expect(rangeSelection(rows, "A", "Z")).toEqual([]);
  });
});

describe("selectAll", () => {
  it("selects every row in visible order", () => {
    expect(selectAll(rows)).toEqual(["A", "B", "C", "D", "E"]);
  });

  it("is empty for an empty row array", () => {
    expect(selectAll([])).toEqual([]);
  });
});
