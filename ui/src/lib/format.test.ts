import { describe, expect, it } from "vitest";
import type { Item } from "@grpcview/v1/workspace_pb";
import { childPathOf, findByKey, itemKey, keyOf, type ItemWithPath } from "./format";

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
