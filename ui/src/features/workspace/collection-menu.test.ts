import { describe, expect, it, vi } from "vitest";
import type { Item } from "@grpcview/v1/workspace_pb";
import type { ItemWithPath } from "@/lib/format";
import { collectionMenuItems, type CollectionMenuActions } from "./collection-menu";

// "Driven off the current selection" is the substance of the T5 spec line
// (docs/design/tree-rewrite-plan.md), and this is where it is checked: what the
// menu OFFERS for an empty/single/multi selection, and that each item fires the
// handler it claims. Nothing here renders — vitest runs `environment: "node"` with
// no jsdom, so there is no menu to click; collection-menu.ts exists as its own
// module for exactly that reason (and to stay out of CollectionPanel's
// monaco-importing module graph — see its header, and delete-confirm.ts's).
//
// Fixtures built structurally rather than with the generated proto constructor,
// like delete-confirm.test.ts / lib/format.test.ts / request-tree.test.tsx — this
// module only imports Item as a TYPE.
const folder = (name: string, path: string[] = []): ItemWithPath => ({
  item: { name, content: { case: "folder", value: { items: [] } } } as unknown as Item,
  path,
});

const request = (name: string, path: string[] = []): ItemWithPath => ({
  item: { name, content: { case: "request", value: {} } } as unknown as Item,
  path,
});

const spies = (): CollectionMenuActions & Record<keyof CollectionMenuActions, ReturnType<typeof vi.fn>> => ({
  newRequest: vi.fn(),
  newFolder: vi.fn(),
  startRename: vi.fn(),
  requestDelete: vi.fn(),
  editFolderMetadata: vi.fn(),
});

const labels = (items: { label: string }[]): string[] => items.map((i) => i.label);

const CAN_CREATE = { canCreateRequest: true };

describe("collectionMenuItems: empty space / the collection root", () => {
  it("offers only the root's two creation actions", () => {
    const items = collectionMenuItems([], spies(), CAN_CREATE);
    expect(labels(items)).toEqual(["New request", "New folder"]);
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
    // Mirrors the header + button's own `disabled={services.length === 0}`: the
    // action exists and is blocked by a fixable condition (add a definition
    // source), which is precisely the case a greyed row communicates better than
    // an absent one.
    const items = collectionMenuItems([], spies(), { canCreateRequest: false });
    expect(labels(items)).toEqual(["New request", "New folder"]);
    expect(items[0].disabled).toBe(true);
    expect(items[1].disabled).toBeUndefined();
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
      false, // New request — nothing above it
      false, // New folder — same group
      true, // Folder metadata
      true, // Rename
      false, // Delete — deliberately grouped WITH rename, not separated from it
    ]);
  });

  it("creates INSIDE the clicked folder, not at the root", () => {
    // The whole reason submitFolder stopped hardcoding `path: []` (T5).
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
    const items = collectionMenuItems([folder("Alpha")], spies(), { canCreateRequest: false });
    expect(items[0].disabled).toBe(true);
    expect(items[1].disabled).toBeUndefined();
  });
});

describe("collectionMenuItems: a MULTI-row selection", () => {
  it("offers DELETE ONLY — the single-target items are omitted, not greyed", () => {
    const items = collectionMenuItems(
      [request("Ping"), request("Pong")],
      spies(),
      CAN_CREATE
    );
    expect(labels(items)).toEqual(["Delete 2 requests"]);
  });

  it("pluralizes by kind through deleteConfirmCopy rather than a second pluralizer", () => {
    expect(labels(collectionMenuItems([folder("A"), folder("B")], spies(), CAN_CREATE))).toEqual([
      "Delete 2 folders",
    ]);
    expect(
      labels(collectionMenuItems([folder("A"), request("B")], spies(), CAN_CREATE))
    ).toEqual(["Delete 2 items"]);
  });

  it("counts the PRUNED batch in the label, so a folder plus its own child reads as one delete", () => {
    // pruneNestedSelections drops the descendant (lib/format.ts). The label must
    // agree with the confirm dialog that follows, and the dialog runs against the
    // same pruned list.
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
    // Deliberate: the user sees two rows highlighted, so the menu describes two
    // rows. Pruning is a delete-time honesty measure about redundant operations,
    // not a claim that the descendant row is unselected.
    const items = collectionMenuItems(
      [folder("Alpha"), request("Ping", ["Alpha"])],
      spies(),
      CAN_CREATE
    );
    expect(labels(items)).not.toContain("Rename");
    expect(items).toHaveLength(1);
  });
});
