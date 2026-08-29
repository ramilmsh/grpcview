import { describe, it } from "node:test";
import { expect } from "expect";
import type { Item } from "@grpcview/v1/workspace_pb";
import type { ItemWithPath } from "@/lib/format";
import { deleteConfirmCopy } from "./delete-confirm";

const folder = (name: string): ItemWithPath => ({
  item: {
    name,
    slug: name.toLowerCase(),
    content: { case: "folder", value: { items: [] } },
  } as unknown as Item,
  collection: ".",
  path: [],
  slugPath: [],
});

const request = (name: string): ItemWithPath => ({
  item: {
    name,
    slug: name.toLowerCase(),
    content: { case: "request", value: {} },
  } as unknown as Item,
  collection: ".",
  path: [],
  slugPath: [],
});

describe("deleteConfirmCopy: a single item (unchanged wording from before T2)", () => {
  it("names a request with a bare '?', no warning", () => {
    expect(deleteConfirmCopy([request("Ping")])).toEqual({
      title: "Delete request",
      emphasis: "Ping",
      suffix: "?",
    });
  });

  it("names a folder with the 'everything inside it' warning", () => {
    expect(deleteConfirmCopy([folder("Calls")])).toEqual({
      title: "Delete folder",
      emphasis: "Calls",
      suffix: " and everything inside it?",
    });
  });
});

describe("deleteConfirmCopy: N > 1, homogeneous", () => {
  it("an all-request batch reads as 'N requests?' with no folder warning", () => {
    expect(
      deleteConfirmCopy([request("Ping"), request("Pong"), request("Pang")]),
    ).toEqual({
      title: "Delete 3 requests",
      emphasis: "3 requests",
      suffix: "?",
    });
  });

  it("an all-folder batch reads as 'N folders and everything inside them?'", () => {
    expect(deleteConfirmCopy([folder("Calls"), folder("Admin")])).toEqual({
      title: "Delete 2 folders",
      emphasis: "2 folders",
      suffix: " and everything inside them?",
    });
  });
});

describe("deleteConfirmCopy: N > 1, mixed folder + request selection", () => {
  it("reads as 'N items', with an explicit sentence warning that folders take their contents", () => {
    expect(
      deleteConfirmCopy([folder("Calls"), request("Ping"), request("Pong")]),
    ).toEqual({
      title: "Delete 3 items",
      emphasis: "3 items",
      suffix:
        "? Folders in the selection will be deleted along with everything inside them.",
    });
  });

  it("the folder-warning sentence appears even with just ONE folder among many requests", () => {
    const copy = deleteConfirmCopy([
      request("A"),
      request("B"),
      request("C"),
      folder("D"),
    ]);
    expect(copy.suffix).toContain("Folders in the selection will be deleted");
  });
});

describe("deleteConfirmCopy: defensive n===0 (never actually shown — confirm.length > 0 gates the dialog)", () => {
  it("says nothing it cannot back up: no count, no folder/request kind, no 'everything inside' claim", () => {
    expect(deleteConfirmCopy([])).toEqual({
      title: "Delete",
      emphasis: "nothing",
      suffix: "?",
    });
  });
});
