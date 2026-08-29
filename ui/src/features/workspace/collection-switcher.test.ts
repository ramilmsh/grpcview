import { describe, it } from "node:test";
import { fn } from "jest-mock";
import { expect } from "expect";
import type { CollectionSummary } from "@grpcview/v1/service_pb";
import {
  collectionSwitcherItems,
  collectionSwitcherLabel,
} from "./collection-switcher";

const summary = (id: string, name: string): CollectionSummary =>
  ({ id, name }) as unknown as CollectionSummary;

const labels = (items: { label: string }[]): string[] =>
  items.map((i) => i.label);

const noActions = { select() {}, rename() {}, createNew() {} };

describe("collectionSwitcherLabel", () => {
  const unique = [summary("api", "API"), summary("web", "Web")];

  it("is the name alone when no other collection shares it", () => {
    expect(collectionSwitcherLabel(unique[0], unique, "web")).toBe("   API");
  });

  it("marks the active one, keeping the row in place", () => {
    expect(collectionSwitcherLabel(unique[1], unique, "web")).toBe("● Web");
  });

  it("appends the id only to the rows a shared name would otherwise make identical", () => {
    const dupes = [
      summary("services/pay", "requests"),
      summary("services/ledger", "requests"),
      summary("web", "Web"),
    ];
    expect(labels(collectionSwitcherItems(dupes, "web", noActions))).toEqual([
      "   requests — services/pay",
      "   requests — services/ledger",
      "● Web",
      "Rename collection…",
      "New collection…",
    ]);
  });
});

describe("collectionSwitcherItems", () => {
  const collections = [summary("api", "API"), summary("web", "Web")];

  it("switches to the collection its row names", () => {
    const select = fn();
    const items = collectionSwitcherItems(collections, "api", {
      ...noActions,
      select,
    });
    items[1].onSelect();
    expect(select).toHaveBeenCalledWith("web");
  });

  it("puts the two write actions last, behind ONE separator, rename before create", () => {
    const items = collectionSwitcherItems(collections, "api", noActions);
    expect(labels(items.slice(2))).toEqual([
      "Rename collection…",
      "New collection…",
    ]);
    expect(items[2].separatorBefore).toBe(true);
    expect(items[3].separatorBefore).toBe(false);
  });

  it("renames on the rename row — it takes no argument, it acts on the active collection", () => {
    const rename = fn();
    const items = collectionSwitcherItems(collections, "api", {
      ...noActions,
      rename,
    });
    items[2].onSelect();
    expect(rename).toHaveBeenCalledWith();
  });

  // The TopBar renders whether or not the workspace lists a collection, so this list has to
  // stand alone: with no rows above it, the create action is the whole menu and needs no rule.
  // Rename goes with them — there is no collection for it to act on.
  it("is the create action alone, unseparated, in a workspace with no collections", () => {
    const createNew = fn();
    const items = collectionSwitcherItems([], null, {
      ...noActions,
      createNew,
    });
    expect(labels(items)).toEqual(["New collection…"]);
    expect(items[0].separatorBefore).toBe(false);
    items[0].onSelect();
    expect(createNew).toHaveBeenCalled();
  });

  // Collections exist but none is active (resolveActiveCollection hasn't settled on one):
  // the rows still switch, and only rename — which needs an active one — is dropped.
  it("drops rename, keeping create, when there are collections but no active one", () => {
    const items = collectionSwitcherItems(collections, null, noActions);
    expect(labels(items)).toEqual(["   API", "   Web", "New collection…"]);
    expect(items[2].separatorBefore).toBe(true);
  });
});
