import { describe, it } from "node:test";
import { expect } from "expect";
import type { TreeRowModel } from "./types";
import type { FlatTree } from "./flatten";
import {
  applyIntent,
  applyRowClick,
  applyTwistieClick,
  type ApplyIntentCtx,
} from "./dispatch";

const row = (
  id: string,
  opts: {
    parentId?: string | null;
    depth?: number;
    expandable?: boolean;
    expanded?: boolean;
  } = {},
): TreeRowModel<string> => ({
  node: id,
  id,
  depth: opts.depth ?? 0,
  parentId: opts.parentId ?? null,
  expandable: opts.expandable ?? false,
  expanded: opts.expanded ?? false,
  posInSet: 1,
  setSize: 1,
});

function flatOf(rows: TreeRowModel<string>[]): FlatTree<string> {
  return {
    rows,
    indexById: new Map(rows.map((r, i) => [r.id, i])),
    defaultExpanded: [],
  };
}

const TREE = flatOf([
  row("folder-a", { expandable: true, expanded: true }),
  row("a1", { parentId: "folder-a", depth: 1 }),
  row("a2", { parentId: "folder-a", depth: 1 }),
  row("folder-b", { expandable: true, expanded: false }),
  row("leaf-c"),
  row("folder-empty", { expandable: true, expanded: true }),
]);

const rowIn = (id: string): TreeRowModel<string> =>
  TREE.rows[TREE.indexById.get(id)!];

function ctx(
  overrides: Partial<ApplyIntentCtx<string>> = {},
): ApplyIntentCtx<string> {
  return {
    flat: TREE,
    focused: null,
    selection: [],
    anchor: null,
    rowsPerPage: 5,
    ...overrides,
  };
}

describe("applyIntent: move", () => {
  it("focuses the next row down and resets the anchor to it", () => {
    const actions = applyIntent(
      { kind: "move", to: "down" },
      ctx({ focused: "folder-a", selection: ["leaf-c"] }),
    );
    expect(actions).toEqual([
      { kind: "focus", id: "a1", scroll: true },
      { kind: "setAnchor", id: "a1" },
    ]);
  });

  it("never emits a setSelection action — keyboard focus is a roving cursor, not a select (listWidget.js:281-298: onUpArrow/onDownArrow never call setSelection)", () => {
    const actions = applyIntent(
      { kind: "move", to: "down" },
      ctx({ focused: "folder-a", selection: ["leaf-c"] }),
    );
    expect(actions.some((a) => a.kind === "setSelection")).toBe(false);
  });

  it("resets the anchor even when one was already pointing somewhere else", () => {
    const actions = applyIntent(
      { kind: "move", to: "down" },
      ctx({ focused: "a1", anchor: "folder-a" }),
    );
    expect(actions).toContainEqual({ kind: "setAnchor", id: "a2" });
  });

  it("with nothing focused, a down move lands on the first row (navigate.ts's null-fromId contract)", () => {
    expect(
      applyIntent({ kind: "move", to: "down" }, ctx({ focused: null })),
    ).toEqual([
      { kind: "focus", id: "folder-a", scroll: true },
      { kind: "setAnchor", id: "folder-a" },
    ]);
  });

  it("pageDown threads rowsPerPage through to targetIndex, not some hardcoded stride", () => {
    expect(
      applyIntent(
        { kind: "move", to: "pageDown" },
        ctx({ focused: "folder-a", rowsPerPage: 3 }),
      ),
    ).toEqual([
      { kind: "focus", id: "folder-b", scroll: true },
      { kind: "setAnchor", id: "folder-b" },
    ]);
  });

  it("returns no actions against an empty tree", () => {
    const empty = flatOf([]);
    expect(
      applyIntent({ kind: "move", to: "down" }, ctx({ flat: empty })),
    ).toEqual([]);
    expect(
      applyIntent(
        { kind: "move", to: "first" },
        ctx({ flat: empty, focused: "anything" }),
      ),
    ).toEqual([]);
  });
});

describe("applyIntent: extend (shift+ArrowUp/ArrowDown)", () => {
  it("extends downward from an existing anchor, growing the range by one row", () => {
    const actions = applyIntent(
      { kind: "extend", to: "down" },
      ctx({ focused: "a1", anchor: "folder-a" }),
    );
    expect(actions).toEqual([
      { kind: "focus", id: "a2", scroll: true },
      { kind: "setAnchor", id: "folder-a" },
      { kind: "setSelection", ids: ["folder-a", "a1", "a2"] },
    ]);
  });

  it("extends upward from an existing anchor the same way", () => {
    const actions = applyIntent(
      { kind: "extend", to: "up" },
      ctx({ focused: "folder-b", anchor: "leaf-c" }),
    );
    expect(actions).toEqual([
      { kind: "focus", id: "a2", scroll: true },
      { kind: "setAnchor", id: "leaf-c" },
      { kind: "setSelection", ids: ["a2", "folder-b", "leaf-c"] },
    ]);
  });

  it("bootstraps the anchor from the CURRENT focused row when there is no anchor yet", () => {
    const actions = applyIntent(
      { kind: "extend", to: "down" },
      ctx({ focused: "a1", anchor: null }),
    );
    expect(actions).toEqual([
      { kind: "focus", id: "a2", scroll: true },
      { kind: "setAnchor", id: "a1" },
      { kind: "setSelection", ids: ["a1", "a2"] },
    ]);
  });

  it("a second extend in the same direction keeps the SAME bootstrapped anchor, growing the range further", () => {
    const second = applyIntent(
      { kind: "extend", to: "down" },
      ctx({ focused: "a2", anchor: "a1" }),
    );
    expect(second).toEqual([
      { kind: "focus", id: "folder-b", scroll: true },
      { kind: "setAnchor", id: "a1" },
      { kind: "setSelection", ids: ["a1", "a2", "folder-b"] },
    ]);
  });

  it("with neither anchor nor focus yet, the first shift+arrow just focuses one row with a single-row selection", () => {
    const actions = applyIntent(
      { kind: "extend", to: "down" },
      ctx({ focused: null, anchor: null }),
    );
    expect(actions).toEqual([
      { kind: "focus", id: "folder-a", scroll: true },
      { kind: "setAnchor", id: null },
      { kind: "setSelection", ids: ["folder-a"] },
    ]);
  });

  it("returns no actions against an empty tree", () => {
    expect(
      applyIntent({ kind: "extend", to: "down" }, ctx({ flat: flatOf([]) })),
    ).toEqual([]);
  });
});

describe("applyIntent: collapseOrParent (ArrowLeft)", () => {
  it("collapses an expanded expandable row IN PLACE — no focus change", () => {
    expect(
      applyIntent({ kind: "collapseOrParent" }, ctx({ focused: "folder-a" })),
    ).toEqual([{ kind: "setExpanded", id: "folder-a", expanded: false }]);
  });

  it("focuses the parent of a nested leaf rather than collapsing anything", () => {
    expect(
      applyIntent({ kind: "collapseOrParent" }, ctx({ focused: "a1" })),
    ).toEqual([{ kind: "focus", id: "folder-a", scroll: true }]);
  });

  it("focuses the parent of an already-collapsed nested folder too, not just a leaf", () => {
    const nested = flatOf([
      row("root", { expandable: true, expanded: true }),
      row("child-folder", {
        parentId: "root",
        depth: 1,
        expandable: true,
        expanded: false,
      }),
    ]);
    expect(
      applyIntent(
        { kind: "collapseOrParent" },
        ctx({ flat: nested, focused: "child-folder" }),
      ),
    ).toEqual([{ kind: "focus", id: "root", scroll: true }]);
  });

  it("is a no-op on a root — there is no parent row to focus", () => {
    expect(
      applyIntent({ kind: "collapseOrParent" }, ctx({ focused: "folder-b" })),
    ).toEqual([]);
    expect(
      applyIntent({ kind: "collapseOrParent" }, ctx({ focused: "leaf-c" })),
    ).toEqual([]);
  });

  it("is a no-op with nothing focused", () => {
    expect(
      applyIntent({ kind: "collapseOrParent" }, ctx({ focused: null })),
    ).toEqual([]);
  });

  it("never emits a setAnchor action, even on the branch that DOES move focus — expansion-only intents never touch the anchor (abstractTree.js:2037-2056's onLeftArrow has no setAnchor call on either branch)", () => {
    const actions = applyIntent(
      { kind: "collapseOrParent" },
      ctx({ focused: "a1", anchor: "leaf-c" }),
    );
    expect(actions.some((a) => a.kind === "setAnchor")).toBe(false);
  });
});

describe("applyIntent: expandOrFirstChild (ArrowRight)", () => {
  it("expands a collapsed expandable row IN PLACE — no focus change", () => {
    expect(
      applyIntent({ kind: "expandOrFirstChild" }, ctx({ focused: "folder-b" })),
    ).toEqual([{ kind: "setExpanded", id: "folder-b", expanded: true }]);
  });

  it("focuses the first child of an already-expanded folder rather than expanding anything", () => {
    expect(
      applyIntent({ kind: "expandOrFirstChild" }, ctx({ focused: "folder-a" })),
    ).toEqual([{ kind: "focus", id: "a1", scroll: true }]);
  });

  it("is a no-op on a leaf — nothing to expand, and no child to focus", () => {
    expect(
      applyIntent({ kind: "expandOrFirstChild" }, ctx({ focused: "leaf-c" })),
    ).toEqual([]);
  });

  it("is a no-op on a childless expanded folder", () => {
    expect(
      applyIntent(
        { kind: "expandOrFirstChild" },
        ctx({ focused: "folder-empty" }),
      ),
    ).toEqual([]);
  });

  it("is a no-op with nothing focused", () => {
    expect(
      applyIntent({ kind: "expandOrFirstChild" }, ctx({ focused: null })),
    ).toEqual([]);
  });

  it("never emits a setAnchor action, even on the branch that moves focus (abstractTree.js:2057-2076's onRightArrow, same property as onLeftArrow)", () => {
    const actions = applyIntent(
      { kind: "expandOrFirstChild" },
      ctx({ focused: "folder-a", anchor: "leaf-c" }),
    );
    expect(actions.some((a) => a.kind === "setAnchor")).toBe(false);
  });
});

describe("applyIntent: toggle (Space)", () => {
  it("collapses an expanded row", () => {
    expect(
      applyIntent({ kind: "toggle" }, ctx({ focused: "folder-a" })),
    ).toEqual([{ kind: "setExpanded", id: "folder-a", expanded: false }]);
  });

  it("expands a collapsed row", () => {
    expect(
      applyIntent({ kind: "toggle" }, ctx({ focused: "folder-b" })),
    ).toEqual([{ kind: "setExpanded", id: "folder-b", expanded: true }]);
  });

  it("is a no-op on a leaf", () => {
    expect(applyIntent({ kind: "toggle" }, ctx({ focused: "leaf-c" }))).toEqual(
      [],
    );
  });

  it("is a no-op with nothing focused", () => {
    expect(applyIntent({ kind: "toggle" }, ctx({ focused: null }))).toEqual([]);
  });

  it("never touches selection or the anchor", () => {
    const actions = applyIntent(
      { kind: "toggle" },
      ctx({ focused: "folder-a", anchor: "leaf-c" }),
    );
    expect(
      actions.some((a) => a.kind === "setSelection" || a.kind === "setAnchor"),
    ).toBe(false);
  });
});

describe("applyIntent: open (Enter / cmd+ArrowDown)", () => {
  it("on a leaf: selects it and emits an open action", () => {
    expect(applyIntent({ kind: "open" }, ctx({ focused: "leaf-c" }))).toEqual([
      { kind: "setSelection", ids: ["leaf-c"] },
      { kind: "open", id: "leaf-c" },
    ]);
  });

  it("on an EXPANDED folder: selects it and COLLAPSES instead of opening — never emits an 'open' action for an expandable row", () => {
    const actions = applyIntent({ kind: "open" }, ctx({ focused: "folder-a" }));
    expect(actions).toEqual([
      { kind: "setSelection", ids: ["folder-a"] },
      { kind: "setExpanded", id: "folder-a", expanded: false },
    ]);
    expect(actions.some((a) => a.kind === "open")).toBe(false);
  });

  it("on a COLLAPSED folder: selects it and expands — the opposite fork of the toggle above", () => {
    expect(applyIntent({ kind: "open" }, ctx({ focused: "folder-b" }))).toEqual(
      [
        { kind: "setSelection", ids: ["folder-b"] },
        { kind: "setExpanded", id: "folder-b", expanded: true },
      ],
    );
  });

  it("is a no-op with nothing focused", () => {
    expect(applyIntent({ kind: "open" }, ctx({ focused: null }))).toEqual([]);
  });

  it("never touches the anchor — faithfully matches listWidget.js's onEnter (276-280), whose entire body is one setSelection call with no setAnchor", () => {
    const actions = applyIntent(
      { kind: "open" },
      ctx({ focused: "leaf-c", anchor: "folder-a" }),
    );
    expect(actions.some((a) => a.kind === "setAnchor")).toBe(false);
  });
});

describe("applyIntent: rename (F2 / mac Enter)", () => {
  it("requests rename for the focused row, whatever kind of row it is — renamability is the HOST's call, not this module's", () => {
    expect(applyIntent({ kind: "rename" }, ctx({ focused: "leaf-c" }))).toEqual(
      [{ kind: "requestRename", id: "leaf-c" }],
    );
    expect(
      applyIntent({ kind: "rename" }, ctx({ focused: "folder-a" })),
    ).toEqual([{ kind: "requestRename", id: "folder-a" }]);
  });

  it("is a no-op with nothing focused", () => {
    expect(applyIntent({ kind: "rename" }, ctx({ focused: null }))).toEqual([]);
  });
});

describe("applyIntent: delete (Delete / cmd+Backspace)", () => {
  it("acts on the WHOLE selection when the focused row is part of it", () => {
    const actions = applyIntent(
      { kind: "delete" },
      ctx({ focused: "a2", selection: ["folder-a", "a1", "a2"] }),
    );
    expect(actions).toEqual([
      { kind: "delete", ids: ["folder-a", "a1", "a2"] },
    ]);
  });

  it("acts on just the focused row when the selection is empty — T1's original single-row behavior, unchanged", () => {
    expect(
      applyIntent(
        { kind: "delete" },
        ctx({ focused: "leaf-c", selection: [] }),
      ),
    ).toEqual([{ kind: "delete", ids: ["leaf-c"] }]);
  });

  it("acts on just the focused row, discarding a stale selection, when focus has moved outside it", () => {
    const actions = applyIntent(
      { kind: "delete" },
      ctx({ focused: "folder-b", selection: ["folder-a", "a1", "a2"] }),
    );
    expect(actions).toEqual([{ kind: "delete", ids: ["folder-b"] }]);
  });

  it("acts on the SELECTION when nothing is focused — reachable via Tab-in then cmd/ctrl+A, which never sets focus", () => {
    expect(
      applyIntent(
        { kind: "delete" },
        ctx({ focused: null, selection: ["a1", "a2"] }),
      ),
    ).toEqual([{ kind: "delete", ids: ["a1", "a2"] }]);
  });

  it("is a no-op only when there is NEITHER focus NOR selection", () => {
    expect(
      applyIntent({ kind: "delete" }, ctx({ focused: null, selection: [] })),
    ).toEqual([]);
  });

  it("treats a stale focused id (no longer a visible row) exactly like nothing focused — falling back to the selection", () => {
    expect(
      applyIntent(
        { kind: "delete" },
        ctx({ focused: "ghost", selection: ["a1"] }),
      ),
    ).toEqual([{ kind: "delete", ids: ["a1"] }]);
    expect(
      applyIntent({ kind: "delete" }, ctx({ focused: "ghost", selection: [] })),
    ).toEqual([]);
  });
});

describe("applyIntent: selectAll (cmd/ctrl+A)", () => {
  it("selects every visible row in order and clears the anchor", () => {
    expect(applyIntent({ kind: "selectAll" }, ctx({ anchor: "a1" }))).toEqual([
      {
        kind: "setSelection",
        ids: ["folder-a", "a1", "a2", "folder-b", "leaf-c", "folder-empty"],
      },
      { kind: "setAnchor", id: null },
    ]);
  });

  it("never touches focus — matches listWidget.js's onCtrlA (317-323), which never calls view.setFocus", () => {
    const actions = applyIntent({ kind: "selectAll" }, ctx({ focused: "a1" }));
    expect(actions.some((a) => a.kind === "focus")).toBe(false);
  });

  it("against an empty tree, still emits both actions with an empty selection — an empty select-all is a valid answer, not 'nothing to do'", () => {
    expect(
      applyIntent({ kind: "selectAll" }, ctx({ flat: flatOf([]) })),
    ).toEqual([
      { kind: "setSelection", ids: [] },
      { kind: "setAnchor", id: null },
    ]);
  });
});

describe("applyIntent: clearSelection (Escape)", () => {
  it("clears a nonempty selection and the anchor", () => {
    expect(
      applyIntent(
        { kind: "clearSelection" },
        ctx({ selection: ["a1", "a2"], anchor: "a1" }),
      ),
    ).toEqual([
      { kind: "setSelection", ids: [] },
      { kind: "setAnchor", id: null },
    ]);
  });

  it("is a no-op when the selection is already empty — mirrors listWidget.js's onEscape guard (324-332), which only acts `if (this.list.getSelection().length)`", () => {
    expect(
      applyIntent({ kind: "clearSelection" }, ctx({ selection: [] })),
    ).toEqual([]);
  });

  it("never touches focus", () => {
    const actions = applyIntent(
      { kind: "clearSelection" },
      ctx({ selection: ["a1"], focused: "a1" }),
    );
    expect(actions.some((a) => a.kind === "focus")).toBe(false);
  });
});

describe("applyIntent: every intent against an empty tree", () => {
  const empty = flatOf([]);
  const emptyCtx = (
    overrides: Partial<ApplyIntentCtx<string>> = {},
  ): ApplyIntentCtx<string> => ctx({ flat: empty, ...overrides });

  it("move/extend/collapseOrParent/expandOrFirstChild/toggle/open/rename/delete all return no actions", () => {
    expect(applyIntent({ kind: "move", to: "down" }, emptyCtx())).toEqual([]);
    expect(applyIntent({ kind: "move", to: "first" }, emptyCtx())).toEqual([]);
    expect(applyIntent({ kind: "extend", to: "up" }, emptyCtx())).toEqual([]);
    expect(applyIntent({ kind: "collapseOrParent" }, emptyCtx())).toEqual([]);
    expect(applyIntent({ kind: "expandOrFirstChild" }, emptyCtx())).toEqual([]);
    expect(applyIntent({ kind: "toggle" }, emptyCtx())).toEqual([]);
    expect(applyIntent({ kind: "open" }, emptyCtx())).toEqual([]);
    expect(applyIntent({ kind: "rename" }, emptyCtx())).toEqual([]);
    expect(applyIntent({ kind: "delete" }, emptyCtx())).toEqual([]);
  });

  it("selectAll still emits its two actions — an empty selection is a valid answer, not a no-op", () => {
    expect(applyIntent({ kind: "selectAll" }, emptyCtx())).toEqual([
      { kind: "setSelection", ids: [] },
      { kind: "setAnchor", id: null },
    ]);
  });

  it("clearSelection is a genuine no-op too, absent any selection to clear", () => {
    expect(applyIntent({ kind: "clearSelection" }, emptyCtx())).toEqual([]);
  });
});

describe("applyIntent: every intent against a null focused id (nonempty tree)", () => {
  const nullFocusCtx = (
    overrides: Partial<ApplyIntentCtx<string>> = {},
  ): ApplyIntentCtx<string> => ctx({ focused: null, ...overrides });

  it("collapseOrParent/expandOrFirstChild/toggle/open/rename all return no actions — there is no focused row to act on", () => {
    expect(applyIntent({ kind: "collapseOrParent" }, nullFocusCtx())).toEqual(
      [],
    );
    expect(applyIntent({ kind: "expandOrFirstChild" }, nullFocusCtx())).toEqual(
      [],
    );
    expect(applyIntent({ kind: "toggle" }, nullFocusCtx())).toEqual([]);
    expect(applyIntent({ kind: "open" }, nullFocusCtx())).toEqual([]);
    expect(applyIntent({ kind: "rename" }, nullFocusCtx())).toEqual([]);
    expect(applyIntent({ kind: "delete" }, nullFocusCtx())).toEqual([]);
  });

  it("delete is the ONE exception: with no focus it falls back to the selection rather than doing nothing", () => {
    expect(
      applyIntent(
        { kind: "delete" },
        nullFocusCtx({ selection: ["a1", "a2"] }),
      ),
    ).toEqual([{ kind: "delete", ids: ["a1", "a2"] }]);
  });

  it("move and extend instead fall back to the first/last row — navigate.ts's null-fromId contract, not a no-op", () => {
    expect(applyIntent({ kind: "move", to: "down" }, nullFocusCtx())).toEqual([
      { kind: "focus", id: "folder-a", scroll: true },
      { kind: "setAnchor", id: "folder-a" },
    ]);
    expect(applyIntent({ kind: "move", to: "up" }, nullFocusCtx())).toEqual([
      { kind: "focus", id: "folder-empty", scroll: true },
      { kind: "setAnchor", id: "folder-empty" },
    ]);
  });

  it("selectAll and clearSelection are unaffected by focus either way", () => {
    expect(applyIntent({ kind: "selectAll" }, nullFocusCtx())).toEqual([
      {
        kind: "setSelection",
        ids: ["folder-a", "a1", "a2", "folder-b", "leaf-c", "folder-empty"],
      },
      { kind: "setAnchor", id: null },
    ]);
    expect(
      applyIntent(
        { kind: "clearSelection" },
        nullFocusCtx({ selection: ["a1"] }),
      ),
    ).toEqual([
      { kind: "setSelection", ids: [] },
      { kind: "setAnchor", id: null },
    ]);
  });
});

describe("applyRowClick: plain click", () => {
  it("on a leaf: selects it, focuses it, sets the anchor, and opens it", () => {
    expect(
      applyRowClick(
        rowIn("leaf-c"),
        { shiftKey: false, modKey: false, rightButton: false },
        ctx(),
      ),
    ).toEqual([
      { kind: "setSelection", ids: ["leaf-c"] },
      { kind: "focus", id: "leaf-c", scroll: false },
      { kind: "setAnchor", id: "leaf-c" },
      { kind: "open", id: "leaf-c" },
    ]);
  });

  it("on an EXPANDED folder: selects, focuses, anchors, and COLLAPSES instead of opening", () => {
    expect(
      applyRowClick(
        rowIn("folder-a"),
        { shiftKey: false, modKey: false, rightButton: false },
        ctx(),
      ),
    ).toEqual([
      { kind: "setSelection", ids: ["folder-a"] },
      { kind: "focus", id: "folder-a", scroll: false },
      { kind: "setAnchor", id: "folder-a" },
      { kind: "setExpanded", id: "folder-a", expanded: false },
    ]);
  });

  it("on a COLLAPSED folder: selects, focuses, anchors, and expands — the opposite fork", () => {
    expect(
      applyRowClick(
        rowIn("folder-b"),
        { shiftKey: false, modKey: false, rightButton: false },
        ctx(),
      ),
    ).toEqual([
      { kind: "setSelection", ids: ["folder-b"] },
      { kind: "focus", id: "folder-b", scroll: false },
      { kind: "setAnchor", id: "folder-b" },
      { kind: "setExpanded", id: "folder-b", expanded: true },
    ]);
  });

  it("REPLACES an existing multi-selection down to just the clicked row, regardless of leaf/folder", () => {
    const actions = applyRowClick(
      rowIn("leaf-c"),
      { shiftKey: false, modKey: false, rightButton: false },
      ctx({ selection: ["folder-a", "a1", "a2"] }),
    );
    expect(actions).toContainEqual({ kind: "setSelection", ids: ["leaf-c"] });
  });
});

describe("applyRowClick: cmd/ctrl+click (modKey)", () => {
  it("adds an unselected row to the selection, and moves BOTH focus and anchor to it", () => {
    const actions = applyRowClick(
      rowIn("a2"),
      { shiftKey: false, modKey: true, rightButton: false },
      ctx({ selection: ["a1"], anchor: "a1", focused: "a1" }),
    );
    expect(actions).toEqual([
      { kind: "focus", id: "a2", scroll: false },
      { kind: "setAnchor", id: "a2" },
      { kind: "setSelection", ids: ["a1", "a2"] },
    ]);
  });

  it("removes an already-selected row from the selection, leaving the rest in place", () => {
    const actions = applyRowClick(
      rowIn("a1"),
      { shiftKey: false, modKey: true, rightButton: false },
      ctx({ selection: ["folder-a", "a1", "a2"] }),
    );
    expect(actions).toEqual([
      { kind: "focus", id: "a1", scroll: false },
      { kind: "setAnchor", id: "a1" },
      { kind: "setSelection", ids: ["folder-a", "a2"] },
    ]);
  });

  it("never opens a leaf, and never toggles a folder's expansion", () => {
    const onLeaf = applyRowClick(
      rowIn("leaf-c"),
      { shiftKey: false, modKey: true, rightButton: false },
      ctx(),
    );
    expect(onLeaf.some((a) => a.kind === "open")).toBe(false);

    const onFolder = applyRowClick(
      rowIn("folder-a"),
      { shiftKey: false, modKey: true, rightButton: false },
      ctx(),
    );
    expect(onFolder.some((a) => a.kind === "setExpanded")).toBe(false);
  });
});

describe("applyRowClick: shift+click (shiftKey)", () => {
  it("extends the selection from an existing anchor to the clicked row, leaving the anchor unchanged regardless of current focus", () => {
    const actions = applyRowClick(
      rowIn("folder-b"),
      { shiftKey: true, modKey: false, rightButton: false },
      ctx({ anchor: "a1", focused: "folder-empty" }),
    );
    expect(actions).toEqual([
      { kind: "focus", id: "folder-b", scroll: false },
      { kind: "setAnchor", id: "a1" },
      { kind: "setSelection", ids: ["a1", "a2", "folder-b"] },
    ]);
  });

  it("bootstraps the anchor from the CURRENT focus when there is no anchor yet", () => {
    const actions = applyRowClick(
      rowIn("a2"),
      { shiftKey: true, modKey: false, rightButton: false },
      ctx({ anchor: null, focused: "folder-a" }),
    );
    expect(actions).toEqual([
      { kind: "focus", id: "a2", scroll: false },
      { kind: "setAnchor", id: "folder-a" },
      { kind: "setSelection", ids: ["folder-a", "a1", "a2"] },
    ]);
  });

  it("degrades to a single-row selection when the anchor no longer names a visible row — e.g. its item was renamed out from under it (§3's finding), same fallback selection.ts's rangeSelection already provides for a filtered-out anchor", () => {
    const actions = applyRowClick(
      rowIn("folder-b"),
      { shiftKey: true, modKey: false, rightButton: false },
      ctx({ anchor: "stale-pre-rename-key", focused: "a1" }),
    );
    expect(actions).toEqual([
      { kind: "focus", id: "folder-b", scroll: false },
      { kind: "setAnchor", id: "stale-pre-rename-key" },
      { kind: "setSelection", ids: ["folder-b"] },
    ]);
  });

  it("never opens or toggles expansion, even on a folder", () => {
    const actions = applyRowClick(
      rowIn("folder-b"),
      { shiftKey: true, modKey: false, rightButton: false },
      ctx({ anchor: "leaf-c" }),
    );
    expect(
      actions.some((a) => a.kind === "open" || a.kind === "setExpanded"),
    ).toBe(false);
  });
});

describe("applyRowClick: modifier precedence", () => {
  it("when both shiftKey and modKey are held, shift wins — mirrors listWidget.js's changeSelection testing isSelectionRangeChangeEvent before isSelectionSingleChangeEvent", () => {
    const actions = applyRowClick(
      rowIn("folder-b"),
      { shiftKey: true, modKey: true, rightButton: false },
      ctx({ anchor: "a1", focused: "a1" }),
    );
    expect(actions).toEqual([
      { kind: "focus", id: "folder-b", scroll: false },
      { kind: "setAnchor", id: "a1" },
      { kind: "setSelection", ids: ["a1", "a2", "folder-b"] },
    ]);
  });
});

describe("applyRowClick: a right-click gesture that reached a click handler", () => {
  it("emits nothing at all — the contextmenu handler owns this gesture", () => {
    expect(
      applyRowClick(
        rowIn("leaf-c"),
        { shiftKey: false, modKey: false, rightButton: true },
        ctx(),
      ),
    ).toEqual([]);
  });

  it("does not open a leaf (the concrete defect: macOS ctrl+click opened the row)", () => {
    const actions = applyRowClick(
      rowIn("leaf-c"),
      { shiftKey: false, modKey: false, rightButton: true },
      ctx(),
    );
    expect(actions.some((a) => a.kind === "open")).toBe(false);
  });

  it("does not toggle a folder either", () => {
    const actions = applyRowClick(
      rowIn("folder-a"),
      { shiftKey: false, modKey: false, rightButton: true },
      ctx(),
    );
    expect(actions.some((a) => a.kind === "setExpanded")).toBe(false);
  });

  it("wins over BOTH modifier branches — checked before shift and before modKey", () => {
    expect(
      applyRowClick(
        rowIn("folder-b"),
        { shiftKey: true, modKey: false, rightButton: true },
        ctx({ anchor: "a1", focused: "a1" }),
      ),
    ).toEqual([]);
    expect(
      applyRowClick(
        rowIn("folder-b"),
        { shiftKey: false, modKey: true, rightButton: true },
        ctx({ selection: ["a1"] }),
      ),
    ).toEqual([]);
  });
});

describe("the focus action's scroll flag, by producer", () => {
  const focusFlags = (actions: ReturnType<typeof applyIntent>): boolean[] =>
    actions.flatMap((a) => (a.kind === "focus" ? [a.scroll] : []));

  it("every KEYBOARD focus scrolls (move, extend, ArrowLeft-to-parent, ArrowRight-to-child)", () => {
    expect(
      focusFlags(applyIntent({ kind: "move", to: "last" }, ctx())),
    ).toEqual([true]);
    expect(
      focusFlags(
        applyIntent(
          { kind: "move", to: "pageDown" },
          ctx({ focused: "folder-a" }),
        ),
      ),
    ).toEqual([true]);
    expect(
      focusFlags(
        applyIntent({ kind: "extend", to: "down" }, ctx({ focused: "a1" })),
      ),
    ).toEqual([true]);
    expect(
      focusFlags(
        applyIntent({ kind: "collapseOrParent" }, ctx({ focused: "a1" })),
      ),
    ).toEqual([true]);
    expect(
      focusFlags(
        applyIntent(
          { kind: "expandOrFirstChild" },
          ctx({ focused: "folder-a" }),
        ),
      ),
    ).toEqual([true]);
  });

  it("every MOUSE focus does not (plain click, cmd/ctrl+click, shift+click, twistie collapse)", () => {
    expect(
      focusFlags(
        applyRowClick(
          rowIn("leaf-c"),
          { shiftKey: false, modKey: false, rightButton: false },
          ctx(),
        ),
      ),
    ).toEqual([false]);
    expect(
      focusFlags(
        applyRowClick(
          rowIn("leaf-c"),
          { shiftKey: false, modKey: true, rightButton: false },
          ctx(),
        ),
      ),
    ).toEqual([false]);
    expect(
      focusFlags(
        applyRowClick(
          rowIn("leaf-c"),
          { shiftKey: true, modKey: false, rightButton: false },
          ctx({ anchor: "a1" }),
        ),
      ),
    ).toEqual([false]);
    expect(
      focusFlags(applyTwistieClick(rowIn("folder-a"), ctx({ focused: "a1" }))),
    ).toEqual([false]);
  });
});

describe("applyTwistieClick: expanding", () => {
  it("just expands — nothing is hidden, so focus and selection are never touched", () => {
    expect(
      applyTwistieClick(
        rowIn("folder-b"),
        ctx({ focused: "leaf-c", selection: ["leaf-c"] }),
      ),
    ).toEqual([{ kind: "setExpanded", id: "folder-b", expanded: true }]);
  });
});

describe("applyTwistieClick: collapsing", () => {
  it("collapses and rebases a focus that was on a hidden child onto the folder", () => {
    expect(
      applyTwistieClick(rowIn("folder-a"), ctx({ focused: "a2" })),
    ).toEqual([
      { kind: "setExpanded", id: "folder-a", expanded: false },
      { kind: "focus", id: "folder-a", scroll: false },
    ]);
  });

  it("leaves a focus that is ELSEWHERE in the tree exactly where it is", () => {
    expect(
      applyTwistieClick(rowIn("folder-a"), ctx({ focused: "leaf-c" })),
    ).toEqual([{ kind: "setExpanded", id: "folder-a", expanded: false }]);
  });

  it("leaves a focus already ON the folder alone — the descendant test is STRICT, so no redundant focus action", () => {
    expect(
      applyTwistieClick(rowIn("folder-a"), ctx({ focused: "folder-a" })),
    ).toEqual([{ kind: "setExpanded", id: "folder-a", expanded: false }]);
  });

  it("replaces hidden SELECTION entries with the folder, so what is painted is what Delete acts on", () => {
    expect(
      applyTwistieClick(
        rowIn("folder-a"),
        ctx({ selection: ["a1", "leaf-c"] }),
      ),
    ).toEqual([
      { kind: "setExpanded", id: "folder-a", expanded: false },
      { kind: "setSelection", ids: ["folder-a", "leaf-c"] },
    ]);
  });

  it("collapses several hidden siblings onto ONE folder entry, and never duplicates a folder already selected", () => {
    expect(
      applyTwistieClick(
        rowIn("folder-a"),
        ctx({ selection: ["folder-a", "a1", "a2"] }),
      ),
    ).toEqual([
      { kind: "setExpanded", id: "folder-a", expanded: false },
      { kind: "setSelection", ids: ["folder-a"] },
    ]);
  });

  it("preserves the order of the entries it does not touch", () => {
    expect(
      applyTwistieClick(
        rowIn("folder-a"),
        ctx({ selection: ["leaf-c", "a1", "folder-b"] }),
      ),
    ).toEqual([
      { kind: "setExpanded", id: "folder-a", expanded: false },
      { kind: "setSelection", ids: ["leaf-c", "folder-a", "folder-b"] },
    ]);
  });

  it("emits no setSelection at all when nothing selected was hidden — an ordinary collapse stays a one-action list", () => {
    expect(
      applyTwistieClick(
        rowIn("folder-a"),
        ctx({ selection: ["leaf-c", "folder-b"] }),
      ),
    ).toEqual([{ kind: "setExpanded", id: "folder-a", expanded: false }]);
  });

  it("rebases both halves at once, and reaches DEEPLY nested descendants, not just direct children", () => {
    const nested = flatOf([
      row("root", { expandable: true, expanded: true }),
      row("mid", {
        parentId: "root",
        depth: 1,
        expandable: true,
        expanded: true,
      }),
      row("deep", { parentId: "mid", depth: 2 }),
      row("after", {}),
    ]);
    const actions = applyTwistieClick(nested.rows[0], {
      flat: nested,
      focused: "deep",
      selection: ["deep", "mid", "after"],
      anchor: null,
    });
    expect(actions).toEqual([
      { kind: "setExpanded", id: "root", expanded: false },
      { kind: "focus", id: "root", scroll: false },
      { kind: "setSelection", ids: ["root", "after"] },
    ]);
  });

  it("collapsing an EMPTY expanded folder hides nothing, so it emits only the setExpanded", () => {
    expect(
      applyTwistieClick(
        rowIn("folder-empty"),
        ctx({ focused: "a1", selection: ["a1"] }),
      ),
    ).toEqual([{ kind: "setExpanded", id: "folder-empty", expanded: false }]);
  });
});
