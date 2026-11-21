import { describe, it } from "node:test";
import { expect } from "expect";
import { create } from "@bufbuild/protobuf";
import { ItemSchema } from "@grpcview/v1/workspace_pb";
import { rootItemsOf } from "@/lib/format";
import { ancestorKeys, filterRequests, flattenRequests } from "./quick-open";

const req = (name: string, slug = name.toLowerCase()) =>
  create(ItemSchema, {
    name,
    slug,
    content: { case: "request", value: {} },
  });
const folder = (name: string, items: ReturnType<typeof req>[]) =>
  create(ItemSchema, {
    name,
    slug: name.toLowerCase(),
    content: { case: "folder", value: { items } },
  });

const ROOT = folder("root", [
  req("Echo"),
  folder("Orders", [req("ListOrders"), folder("Admin", [req("Purge")])]),
  req("Health"),
]);
const roots = rootItemsOf(ROOT, "c");

describe("flattenRequests", () => {
  it("lists requests depth first, skipping folders", () => {
    expect(flattenRequests(roots).map((r) => r.item.name)).toEqual([
      "Echo",
      "ListOrders",
      "Purge",
      "Health",
    ]);
  });
});

describe("filterRequests", () => {
  const all = flattenRequests(roots);
  it("lists everything on an empty query, with keys and folder paths", () => {
    const rows = filterRequests(all, " ");
    expect(rows.map((r) => [r.key, r.folder])).toEqual([
      ["c/echo", ""],
      ["c/orders/listorders", "Orders"],
      ["c/orders/admin/purge", "Orders / Admin"],
      ["c/health", ""],
    ]);
  });
  it("ranks name matches before path matches", () => {
    const rows = filterRequests(all, "oe");
    // "ListOrders" by name; "Purge" only through "Orders/Admin/Purge".
    expect(rows.map((r) => r.item.item.name)).toEqual(["ListOrders", "Purge"]);
    expect(rows[0].hits).toEqual([4, 7]);
    // Path matches only highlight within the name: here the folder took both.
    expect(rows[1].hits).toEqual([]);
  });
  it("is empty on a miss", () => {
    expect(filterRequests(all, "zzz")).toEqual([]);
  });
});

describe("ancestorKeys", () => {
  const purge = flattenRequests(roots)[2];
  it("expands every ancestor folder, plus the collection when tiered", () => {
    expect(ancestorKeys(purge, false)).toEqual(["c/orders", "c/orders/admin"]);
    expect(ancestorKeys(purge, true)).toEqual([
      "c",
      "c/orders",
      "c/orders/admin",
    ]);
    expect(ancestorKeys(flattenRequests(roots)[0], false)).toEqual([]);
  });
});
