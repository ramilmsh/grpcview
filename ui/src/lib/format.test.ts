import { describe, expect, it } from "vitest";
import type { Item } from "@grpcview/v1/workspace_pb";
import { childPathOf, findByKey, itemKey, keyOf, pruneNestedSelections, type ItemWithPath } from "./format";

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
    expect(pruneNestedSelections([users, ban])).toEqual([users]);
  });

  it("is relative to what's actually IN the batch: Admin survives when Users (its own ancestor) isn't selected", () => {
    expect(pruneNestedSelections([admin, ban])).toEqual([admin]);
  });

  it("collapses a THREE-level selection (folder, its child folder, and that child's own child) to just the topmost ancestor in one pass", () => {
    expect(pruneNestedSelections([users, admin, ban])).toEqual([users]);
  });

  it("preserves the original relative order of the SURVIVING items, not tree order", () => {
    expect(pruneNestedSelections([getUser, ping, users])).toEqual([ping, users]);
  });

  it("is a no-op for a single-item selection", () => {
    expect(pruneNestedSelections([ban])).toEqual([ban]);
  });

  it("collapses EXACT duplicates to one, keeping the first occurrence", () => {
    expect(pruneNestedSelections([getUser, getUser])).toEqual([getUser]);
  });

  it("de-duplicates two DISTINCT objects that name the same path (what a rename collision actually produces)", () => {
    expect(pruneNestedSelections([request("GetUser", ["Users"]), request("GetUser", ["Users"])])).toEqual([
      request("GetUser", ["Users"]),
    ]);
  });

  it("de-duplicates and prunes in ONE pass: a duplicated descendant of a selected folder leaves just the folder", () => {
    expect(pruneNestedSelections([users, ban, ban])).toEqual([users]);
  });

  it("still distinguishes sibling names where one is a prefix STRING of the other", () => {
    const adminTools = folder("AdminTools", ["Users"], []);
    expect(pruneNestedSelections([admin, adminTools])).toEqual([admin, adminTools]);
  });

  it("is empty for an empty selection", () => {
    expect(pruneNestedSelections([])).toEqual([]);
  });
});
