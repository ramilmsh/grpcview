import { describe, it } from "node:test";
import { expect } from "expect";
import type { Item } from "@grpcview/v1/workspace_pb";
import { rootItemsOf, type ItemWithPath } from "./format";
import { NEW_REQUEST_KEY, type OpenTab } from "./ui-store";
import { staleTabKeys } from "./stale-tabs";

const request = (slug: string): Item =>
  ({
    name: slug,
    slug,
    content: { case: "request", value: {} },
  }) as unknown as Item;

const folder = (slug: string, items: Item[]): Item =>
  ({
    name: slug,
    slug,
    content: { case: "folder", value: { items } },
  }) as unknown as Item;

// Collection "." holding users/{get,admin/ban} and a top-level "ping".
const tree = (): ItemWithPath[] =>
  rootItemsOf(
    folder("", [
      folder("users", [request("get"), folder("admin", [request("ban")])]),
      request("ping"),
    ]),
    ".",
  );

const tab = (
  key: string,
  kind: OpenTab["kind"] = "request",
  collection = ".",
): OpenTab => ({ key, name: key, collection, kind });

describe("staleTabKeys", () => {
  it("keeps every tab whose item is still there, at any depth", () => {
    const tabs = [
      tab("./ping"),
      tab("./users/get"),
      tab("./users/admin/ban"),
      tab("./users", "folder"),
      tab("./users/admin", "folder"),
    ];
    expect(staleTabKeys(tabs, new Map([[".", tree()]]))).toEqual([]);
  });

  it("names request and folder tabs whose item is gone", () => {
    const tabs = [
      tab("./ping"),
      tab("./gone"),
      tab("./users/admin/gone"),
      tab("./old-folder", "folder"),
    ];
    expect(staleTabKeys(tabs, new Map([[".", tree()]]))).toEqual([
      "./gone",
      "./users/admin/gone",
      "./old-folder",
    ]);
  });

  it("names every tab under a folder that is gone", () => {
    const tabs = [tab("./users/get"), tab("./users/admin/ban"), tab("./ping")];
    const withoutUsers = rootItemsOf(folder("", [request("ping")]), ".");
    expect(staleTabKeys(tabs, new Map([[".", withoutUsers]]))).toEqual([
      "./users/get",
      "./users/admin/ban",
    ]);
  });

  it("leaves tabs of a collection that has not loaded alone", () => {
    const tabs = [tab("other/x", "request", "other"), tab("./gone")];
    expect(staleTabKeys(tabs, new Map())).toEqual([]);
    expect(staleTabKeys(tabs, new Map([[".", tree()]]))).toEqual(["./gone"]);
  });

  it("treats a loaded EMPTY collection as loaded", () => {
    expect(staleTabKeys([tab("./ping")], new Map([[".", []]]))).toEqual([
      "./ping",
    ]);
  });

  it("never names the New request draft tab", () => {
    const tabs = [tab(NEW_REQUEST_KEY, "newRequest")];
    expect(staleTabKeys(tabs, new Map([[".", []]]))).toEqual([]);
  });

  it("does not match a sibling whose slug only shares a prefix", () => {
    const tabs = [tab("./pin")];
    expect(staleTabKeys(tabs, new Map([[".", tree()]]))).toEqual(["./pin"]);
  });

  it("looks a tab up in its own collection, not one parsed from its key", () => {
    // A slash-bearing collection id: "svc/reqs" holds "get".
    const items = rootItemsOf(folder("", [request("get")]), "svc/reqs");
    const tabs = [tab("svc/reqs/get", "request", "svc/reqs")];
    expect(staleTabKeys(tabs, new Map([["svc/reqs", items]]))).toEqual([]);
  });
});
