import { describe, expect, it } from "vitest";
import type { Item } from "@grpcview/v1/workspace_pb";
import { childPathOf, findByKey, itemKey, keyOf, pruneNestedSelections, type ItemWithPath } from "./format";

// The key convention is name-derived (see format.ts / the plan's "identity hazard"),
// so these helpers are what open tabs, drafts and invoke results hang off. A silent
// change here detaches a user's unsaved work from the request it belongs to — which
// is why they get tests before the tree rewrite starts moving items around.
//
// Items are built structurally rather than with the generated proto constructor: the
// runtime _pb modules are Bazel-generated and format.ts only imports Item as a TYPE,
// so a plain object with the fields under test is both sufficient and honest.
const folder = (name: string, path: string[], children: ItemWithPath[]): ItemWithPath => ({
  item: { name, content: { case: "folder", value: { items: [] } } } as unknown as Item,
  path,
  children,
});

const request = (name: string, path: string[]): ItemWithPath => ({
  item: { name, content: { case: "request", value: {} } } as unknown as Item,
  path,
});

const tree: ItemWithPath[] = [
  request("Ping", []),
  folder("Users", [], [
    request("GetUser", ["Users"]),
    folder("Admin", ["Users"], [request("Ban", ["Users", "Admin"])]),
  ]),
];

describe("keyOf", () => {
  it("joins a parent path and name with /", () => {
    expect(keyOf(["Users", "Admin"], "Ban")).toBe("Users/Admin/Ban");
  });

  it("is just the name at the root", () => {
    expect(keyOf([], "Ping")).toBe("Ping");
  });
});

describe("itemKey", () => {
  it("derives the key from the item's own path and name", () => {
    expect(itemKey(tree[1].children![1].children![0])).toBe("Users/Admin/Ban");
  });

  it("changes when the name changes — the property every rename path must remap", () => {
    const before = itemKey(request("GetUser", ["Users"]));
    const after = itemKey(request("FetchUser", ["Users"]));
    expect(before).not.toBe(after);
  });
});

describe("findByKey", () => {
  it("resolves a nested item", () => {
    expect(findByKey(tree, "Users/Admin/Ban")?.item.name).toBe("Ban");
  });

  it("resolves a root item", () => {
    expect(findByKey(tree, "Ping")?.item.name).toBe("Ping");
  });

  it("returns null for a key that no longer exists", () => {
    expect(findByKey(tree, "Users/Admin/Unban")).toBeNull();
  });

  it("returns null for a null key rather than guessing", () => {
    expect(findByKey(tree, null)).toBeNull();
  });

  it("does not match a folder's key against its child's name", () => {
    expect(findByKey(tree, "Ban")).toBeNull();
  });
});

describe("childPathOf", () => {
  it("appends a folder's own name, since children live under it", () => {
    expect(childPathOf(tree[1])).toEqual(["Users"]);
    expect(childPathOf(tree[1].children![1])).toEqual(["Users", "Admin"]);
  });

  it("is the empty root path for no parent", () => {
    expect(childPathOf(null)).toEqual([]);
  });
});

// pruneNestedSelections — the tree-rewrite T2 multi-select delete needs this
// to keep a confirm dialog's count (and the actual delete loop) honest when a
// folder AND one of its own descendants are both selected at once, reachable
// via shift+click across an expanded folder's rows or ctrl+click picking both
// individually. Reuses the SAME `tree` fixture as itemKey/findByKey above:
// Ping (root request), Users (folder: GetUser + Admin), Admin (nested folder:
// Ban).
describe("pruneNestedSelections", () => {
  const [ping, users] = tree;
  const [getUser, admin] = users.children!;
  const [ban] = admin.children!;

  it("keeps everything when nothing in the selection is nested under anything else", () => {
    expect(pruneNestedSelections([ping, getUser])).toEqual([ping, getUser]);
  });

  it("drops a DIRECT child when its folder is also in the selection", () => {
    expect(pruneNestedSelections([users, getUser])).toEqual([users]);
  });

  it("drops a GRANDCHILD when its top-level ancestor is also in the selection, even without the middle folder", () => {
    // Users + Ban, WITHOUT Admin itself — proves the check walks the full
    // path, not just an immediate parent/child relationship.
    expect(pruneNestedSelections([users, ban])).toEqual([users]);
  });

  it("is relative to what's actually IN the batch: Admin survives when Users (its own ancestor) isn't selected", () => {
    expect(pruneNestedSelections([admin, ban])).toEqual([admin]);
  });

  it("collapses a THREE-level selection (folder, its child folder, and that child's own child) to just the topmost ancestor in one pass", () => {
    expect(pruneNestedSelections([users, admin, ban])).toEqual([users]);
  });

  it("preserves the original relative order of the SURVIVING items, not tree order", () => {
    // GetUser is listed before Ping in the input, and before Users too —
    // GetUser is pruned (Users is its ancestor and is also selected), leaving
    // Ping then Users in exactly the order they appeared in the input.
    expect(pruneNestedSelections([getUser, ping, users])).toEqual([ping, users]);
  });

  it("is a no-op for a single-item selection", () => {
    expect(pruneNestedSelections([ban])).toEqual([ban]);
  });

  it("collapses EXACT duplicates to one, keeping the first occurrence", () => {
    // Ancestry alone never catches these: isStrictPrefix requires the prefix to
    // be strictly shorter, so an entry is not its own ancestor and two equal
    // entries each leave the other standing. Reachable via ui-store's
    // moveSubtree, which remaps treeSelection id-for-id — renaming one selected
    // row onto a name another selected row already has puts the same id in the
    // list twice, and both copies resolve to the one surviving row.
    expect(pruneNestedSelections([getUser, getUser])).toEqual([getUser]);
  });

  it("de-duplicates two DISTINCT objects that name the same path (what a rename collision actually produces)", () => {
    // Not the same object reference — the resolved-from-id path hands back
    // whatever findByKey produced per lookup — so identity has to be decided on
    // the path, not on ===.
    expect(pruneNestedSelections([request("GetUser", ["Users"]), request("GetUser", ["Users"])])).toEqual([
      request("GetUser", ["Users"]),
    ]);
  });

  it("de-duplicates and prunes in ONE pass: a duplicated descendant of a selected folder leaves just the folder", () => {
    expect(pruneNestedSelections([users, ban, ban])).toEqual([users]);
  });

  it("still distinguishes sibling names where one is a prefix STRING of the other", () => {
    // Guards the segment-array comparison against a future 'join the path and
    // compare strings' shortcut: "Users/Admin" is not an ancestor of
    // "Users/AdminTools", and neither is a duplicate of the other.
    const adminTools = folder("AdminTools", ["Users"], []);
    expect(pruneNestedSelections([admin, adminTools])).toEqual([admin, adminTools]);
  });

  it("is empty for an empty selection", () => {
    expect(pruneNestedSelections([])).toEqual([]);
  });
});
