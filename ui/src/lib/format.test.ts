import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";
import { DurationSchema } from "@bufbuild/protobuf/wkt";
import type { Item, Method, Service } from "@grpcview/v1/workspace_pb";
import {
  childPathOf,
  findByKey,
  idleTimeoutLabel,
  itemKey,
  middleEllipsis,
  pruneNestedSelections,
  resolveMethod,
  rootItemsOf,
  serviceName,
  slugKeyIn,
  uptimeLabel,
  type ItemWithPath,
} from "./format";

const COLL = ".";

// Slugs default to the lower-kebab of the name, matching store.slugify, but the
// fixtures below pass them explicitly wherever a test turns on slug vs name.
const slugify = (name: string): string => name.toLowerCase().replace(/\s+/g, "-");

const folder = (
  name: string,
  path: string[],
  children: ItemWithPath[],
  slug = slugify(name)
): ItemWithPath => ({
  item: { name, slug, content: { case: "folder", value: { items: [] } } } as unknown as Item,
  collection: COLL,
  path,
  slugPath: path.map(slugify),
  children,
});

const request = (name: string, path: string[], slug = slugify(name)): ItemWithPath => ({
  item: { name, slug, content: { case: "request", value: {} } } as unknown as Item,
  collection: COLL,
  path,
  slugPath: path.map(slugify),
});

const tree: ItemWithPath[] = [
  request("Ping", []),
  folder("Users", [], [
    request("GetUser", ["Users"]),
    folder("Admin", ["Users"], [request("Ban", ["Users", "Admin"])]),
  ]),
];

// A wire Item tree, for rootItemsOf/slugKeyIn (which read protos, not ItemWithPath).
const wireRequest = (name: string, slug = slugify(name)): Item =>
  ({ name, slug, content: { case: "request", value: {} } }) as unknown as Item;

const wireFolder = (name: string, items: Item[], slug = slugify(name)): Item =>
  ({ name, slug, content: { case: "folder", value: { items } } }) as unknown as Item;

const service = (pkg: string, name: string, methods: string[]): Service =>
  ({
    package: pkg,
    name,
    methods: methods.map((m) => ({ name: m }) as unknown as Method),
  }) as unknown as Service;

describe("serviceName", () => {
  it("joins a package and a name with a dot", () => {
    expect(serviceName(service("echo.v1", "EchoService", []))).toBe("echo.v1.EchoService");
  });

  it("is just the name in the EMPTY package — a leading dot matches nothing", () => {
    expect(serviceName(service("", "EchoService", []))).toBe("EchoService");
  });
});

describe("resolveMethod", () => {
  it("finds a method on a packaged service", () => {
    const services = [service("echo.v1", "EchoService", ["Echo"])];
    expect(resolveMethod(services, "echo.v1.EchoService", "Echo")?.name).toBe("Echo");
  });

  it("finds a method on a service in the EMPTY package (regression: the leading dot)", () => {
    const services = [service("", "EchoService", ["Echo"])];
    expect(resolveMethod(services, "EchoService", "Echo")?.name).toBe("Echo");
  });
});

describe("itemKey", () => {
  it("derives the key from the item's slug path, prefixed by its collection id", () => {
    expect(itemKey(tree[1].children![1].children![0])).toBe("./users/admin/ban");
  });

  it("prefixes a DIFFERENT collection id, so two collections never collide", () => {
    const other = { ...request("Ping", []), collection: "apis/echo" };
    expect(itemKey(other)).toBe("apis/echo/ping");
  });

  it("is UNCHANGED by a rename — the same slug under a new display name", () => {
    const before = itemKey(request("GetUser", ["Users"], "get-user"));
    const after = itemKey(request("FetchUser", ["Users"], "get-user"));
    expect(after).toBe(before);
    expect(after).toBe("./users/get-user");
  });

  it("does change when the slug does, which is what a real MOVE produces", () => {
    const before = itemKey(request("GetUser", ["Users"], "get-user"));
    const after = itemKey(request("GetUser", ["Users"], "get-user-2"));
    expect(after).not.toBe(before);
  });
});

describe("rootItemsOf", () => {
  const root = wireFolder("Coll", [
    wireRequest("Ping"),
    wireFolder("Users", [wireRequest("Get User")]),
  ]);

  it("threads the collection id and the slug path down every level", () => {
    const items = rootItemsOf(root, "apis/echo");
    expect(itemKey(items[0])).toBe("apis/echo/ping");
    expect(itemKey(items[1].children![0])).toBe("apis/echo/users/get-user");
  });

  it("keeps `path` on DISPLAY names, since that is what the RPCs address by", () => {
    const items = rootItemsOf(root, ".");
    expect(items[1].children![0].path).toEqual(["Users"]);
    expect(items[1].children![0].slugPath).toEqual(["users"]);
  });

  it("is empty when there is no root folder", () => {
    expect(rootItemsOf(undefined, ".")).toEqual([]);
  });
});

describe("slugKeyIn", () => {
  // The shape a Move response has after "Get User" landed in Admin, where an
  // existing sibling already held the "get-user" slug (store.Move's uniqueSlug).
  const root = wireFolder("Coll", [
    wireFolder("Users", [
      wireFolder("Admin", [
        wireRequest("Other", "get-user"),
        wireRequest("Get User", "get-user-2"),
      ]),
    ]),
  ]);

  it("reads back the RE-SLUGGED key of a moved item, which names cannot predict", () => {
    expect(slugKeyIn(".", root, ["Users", "Admin"], "Get User")).toBe(
      "./users/admin/get-user-2"
    );
  });

  it("finds an item at the collection root", () => {
    const flat = wireFolder("Coll", [wireRequest("Ping")]);
    expect(slugKeyIn("apis/echo", flat, [], "Ping")).toBe("apis/echo/ping");
  });

  it("returns null when a folder on the path is missing", () => {
    expect(slugKeyIn(".", root, ["Users", "Nope"], "Get User")).toBeNull();
  });

  it("returns null when the leaf is missing", () => {
    expect(slugKeyIn(".", root, ["Users", "Admin"], "Unban")).toBeNull();
  });

  it("returns null for no root at all", () => {
    expect(slugKeyIn(".", undefined, [], "Ping")).toBeNull();
  });

  it("will not walk THROUGH a request as if it were a folder", () => {
    const flat = wireFolder("Coll", [wireRequest("Ping")]);
    expect(slugKeyIn(".", flat, ["Ping"], "Ping")).toBeNull();
  });
});

describe("findByKey", () => {
  it("resolves a nested item", () => {
    expect(findByKey(tree, "./users/admin/ban")?.item.name).toBe("Ban");
  });

  it("resolves a root item", () => {
    expect(findByKey(tree, "./ping")?.item.name).toBe("Ping");
  });

  it("returns null for a key that no longer exists", () => {
    expect(findByKey(tree, "./users/admin/unban")).toBeNull();
  });

  it("returns null for a null key rather than guessing", () => {
    expect(findByKey(tree, null)).toBeNull();
  });

  it("does not match a folder's key against its child's name", () => {
    expect(findByKey(tree, "./ban")).toBeNull();
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

describe("middleEllipsis", () => {
  it("leaves a string that fits alone", () => {
    expect(middleEllipsis("bazel://a:b", 34)).toBe("bazel://a:b");
    expect(middleEllipsis("x".repeat(34), 34)).toBe("x".repeat(34));
  });

  it("keeps both ends of a long bazel source id and never exceeds the cap", () => {
    const id = "bazel://protos/acme/orbit/ledger/v1:ledger_proto";
    const short = middleEllipsis(id, 34);
    expect(short.length).toBe(34);
    expect(short.startsWith("bazel://protos")).toBe(true);
    expect(short.endsWith("v1:ledger_proto")).toBe(true);
  });

  it("holds at tiny caps", () => {
    expect(middleEllipsis("abcdef", 3)).toBe("a…f");
    expect(middleEllipsis("abcdef", 2)).toBe("a…");
  });
});

describe("uptimeLabel", () => {
  it("is empty for a zero/unset started_unix", () => {
    expect(uptimeLabel(0, 1_000_000)).toBe("");
  });

  it("renders seconds, minutes and hours at their own granularity", () => {
    const now = 1_000_000_000; // ms
    expect(uptimeLabel(now / 1000 - 45, now)).toBe("45s");
    expect(uptimeLabel(now / 1000 - 125, now)).toBe("2m 5s");
    expect(uptimeLabel(now / 1000 - 7500, now)).toBe("2h 5m");
    expect(uptimeLabel(now / 1000 - 90000, now)).toBe("1d 1h");
  });

  it("defaults `now` to the current clock when omitted", () => {
    const startedUnix = Math.floor(Date.now() / 1000) - 10;
    expect(uptimeLabel(startedUnix)).toMatch(/^(9|10|11)s$/);
  });
});

describe("idleTimeoutLabel", () => {
  it("reads an absent or zero duration as never idling out", () => {
    expect(idleTimeoutLabel(undefined)).toBe("never idles out");
    expect(idleTimeoutLabel(create(DurationSchema, { seconds: 0n, nanos: 0 }))).toBe(
      "never idles out"
    );
  });

  it("prefixes a real timeout with 'idle' so the column reads on its own", () => {
    expect(idleTimeoutLabel(create(DurationSchema, { seconds: 90n, nanos: 0 }))).toBe(
      "idle 1m 30s"
    );
  });

  it("drops a trailing zero component — 'idle 1h', not 'idle 1h 0m'", () => {
    expect(idleTimeoutLabel(create(DurationSchema, { seconds: 3600n, nanos: 0 }))).toBe(
      "idle 1h"
    );
    expect(idleTimeoutLabel(create(DurationSchema, { seconds: 86400n, nanos: 0 }))).toBe(
      "idle 1d"
    );
    expect(idleTimeoutLabel(create(DurationSchema, { seconds: 60n, nanos: 0 }))).toBe(
      "idle 1m"
    );
  });
});
