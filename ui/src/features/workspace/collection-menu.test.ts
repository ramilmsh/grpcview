import { describe, it } from "node:test";
import { fn } from "jest-mock";
import { expect } from "expect";
import type { Item } from "@grpcview/v1/workspace_pb";
import type { ItemWithPath } from "@/lib/format";
import {
  collectionMenuItems,
  type CollectionMenuActions,
} from "./collection-menu";

const slugify = (name: string): string => name.toLowerCase();

const folder = (name: string, path: string[] = []): ItemWithPath => ({
  item: {
    name,
    slug: slugify(name),
    content: { case: "folder", value: { items: [] } },
  } as unknown as Item,
  collection: ".",
  path,
  slugPath: path.map(slugify),
});

const request = (name: string, path: string[] = []): ItemWithPath => ({
  item: {
    name,
    slug: slugify(name),
    content: { case: "request", value: {} },
  } as unknown as Item,
  collection: ".",
  path,
  slugPath: path.map(slugify),
});

const spies = (): CollectionMenuActions &
  Record<keyof CollectionMenuActions, ReturnType<typeof fn>> => ({
  newRequest: fn(),
  newFolder: fn(),
  newCollection: fn(),
  startRename: fn(),
  requestDelete: fn(),
  editFolderMetadata: fn(),
});

const labels = (items: { label: string }[]): string[] =>
  items.map((i) => i.label);

const CAN_CREATE = { canCreateRequest: true };

describe("collectionMenuItems: empty space / the collection root", () => {
  it("offers the root's creation actions, plus the workspace-level one behind a separator", () => {
    const items = collectionMenuItems([], spies(), CAN_CREATE);
    expect(labels(items)).toEqual([
      "New request",
      "New folder",
      "New collection\u2026",
    ]);
    expect(items.map((i) => i.separatorBefore ?? false)).toEqual([
      false,
      false,
      true,
    ]);
  });

  it("routes New collection at the workspace, with no parent to speak of", () => {
    const actions = spies();
    collectionMenuItems([], actions, CAN_CREATE)[2].onSelect();
    expect(actions.newCollection).toHaveBeenCalledWith();
  });

  it("targets the root (null parent) for both", () => {
    const actions = spies();
    const items = collectionMenuItems([], actions, CAN_CREATE);
    items[0].onSelect();
    items[1].onSelect();
    expect(actions.newRequest).toHaveBeenCalledWith(null);
    expect(actions.newFolder).toHaveBeenCalledWith(null);
  });

  it("disables — never omits — New request when there are no services yet", () => {
    const items = collectionMenuItems([], spies(), { canCreateRequest: false });
    expect(labels(items)).toEqual([
      "New request",
      "New folder",
      "New collection\u2026",
    ]);
    expect(items[0].disabled).toBe(true);
    expect(items[1].disabled).toBeUndefined();
    // A collection needs no service, so this one is reachable in an empty workspace too.
    expect(items[2].disabled).toBeUndefined();
  });
});

describe("collectionMenuItems: a single REQUEST row", () => {
  it("offers rename and delete only — no creation actions for a leaf", () => {
    const items = collectionMenuItems([request("Ping")], spies(), CAN_CREATE);
    expect(labels(items)).toEqual(["Rename", "Delete request"]);
  });

  it("labels delete with the confirm dialog's own wording, and marks it danger", () => {
    const items = collectionMenuItems([request("Ping")], spies(), CAN_CREATE);
    expect(items[1].danger).toBe(true);
    expect(items[0].danger).toBeUndefined();
  });

  it("renames that row, and deletes exactly that one row", () => {
    const actions = spies();
    const item = request("Ping");
    const items = collectionMenuItems([item], actions, CAN_CREATE);
    items[0].onSelect();
    items[1].onSelect();
    expect(actions.startRename).toHaveBeenCalledWith(item);
    expect(actions.requestDelete).toHaveBeenCalledWith([item]);
  });
});

describe("collectionMenuItems: a single FOLDER row", () => {
  it("offers creation, folder metadata, rename and delete, in that order", () => {
    const items = collectionMenuItems([folder("Alpha")], spies(), CAN_CREATE);
    expect(labels(items)).toEqual([
      "New request",
      "New folder",
      "Folder metadata",
      "Rename",
      "Delete folder",
    ]);
  });

  it("separates the creation group, the metadata group and rename/delete", () => {
    const items = collectionMenuItems([folder("Alpha")], spies(), CAN_CREATE);
    expect(items.map((i) => i.separatorBefore ?? false)).toEqual([
      false,
      false,
      true,
      true,
      false,
    ]);
  });

  it("creates INSIDE the clicked folder, not at the root", () => {
    const actions = spies();
    const alpha = folder("Alpha");
    const items = collectionMenuItems([alpha], actions, CAN_CREATE);
    items[0].onSelect();
    items[1].onSelect();
    expect(actions.newRequest).toHaveBeenCalledWith(alpha);
    expect(actions.newFolder).toHaveBeenCalledWith(alpha);
  });

  it("routes folder metadata to the clicked folder", () => {
    const actions = spies();
    const alpha = folder("Alpha");
    collectionMenuItems([alpha], actions, CAN_CREATE)[2].onSelect();
    expect(actions.editFolderMetadata).toHaveBeenCalledWith(alpha);
  });

  it("still disables New request with no services, leaving New folder alone", () => {
    const items = collectionMenuItems([folder("Alpha")], spies(), {
      canCreateRequest: false,
    });
    expect(items[0].disabled).toBe(true);
    expect(items[1].disabled).toBeUndefined();
  });
});

describe("collectionMenuItems: a MULTI-row selection", () => {
  it("offers DELETE ONLY — the single-target items are omitted, not greyed", () => {
    const items = collectionMenuItems(
      [request("Ping"), request("Pong")],
      spies(),
      CAN_CREATE,
    );
    expect(labels(items)).toEqual(["Delete 2 requests"]);
  });

  it("pluralizes by kind through deleteConfirmCopy rather than a second pluralizer", () => {
    expect(
      labels(
        collectionMenuItems([folder("A"), folder("B")], spies(), CAN_CREATE),
      ),
    ).toEqual(["Delete 2 folders"]);
    expect(
      labels(
        collectionMenuItems([folder("A"), request("B")], spies(), CAN_CREATE),
      ),
    ).toEqual(["Delete 2 items"]);
  });

  it("counts the PRUNED batch in the label, so a folder plus its own child reads as one delete", () => {
    const alpha = folder("Alpha");
    const child = request("Ping", ["Alpha"]);
    const items = collectionMenuItems([alpha, child], spies(), CAN_CREATE);
    expect(labels(items)).toEqual(["Delete folder"]);
  });

  it("hands the PRUNED batch to requestDelete too, not the raw selection", () => {
    const actions = spies();
    const alpha = folder("Alpha");
    const child = request("Ping", ["Alpha"]);
    collectionMenuItems([alpha, child], actions, CAN_CREATE)[0].onSelect();
    expect(actions.requestDelete).toHaveBeenCalledWith([alpha]);
  });

  it("narrows on the RAW selection length, so two rows never offer Rename even when pruning leaves one", () => {
    const items = collectionMenuItems(
      [folder("Alpha"), request("Ping", ["Alpha"])],
      spies(),
      CAN_CREATE,
    );
    expect(labels(items)).not.toContain("Rename");
    expect(items).toHaveLength(1);
  });
});
