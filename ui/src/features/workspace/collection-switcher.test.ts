import { describe, expect, it, vi } from "vitest";
import type { CollectionSummary } from "@grpcview/v1/service_pb";
import { collectionSwitcherItems, collectionSwitcherLabel } from "./collection-switcher";

const summary = (id: string, name: string): CollectionSummary =>
  ({ id, name }) as unknown as CollectionSummary;

const labels = (items: { label: string }[]): string[] => items.map((i) => i.label);

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
    expect(labels(collectionSwitcherItems(dupes, "web", { select() {}, createNew() {} }))).toEqual([
      "   requests — services/pay",
      "   requests — services/ledger",
      "● Web",
      "New collection…",
    ]);
  });
});

describe("collectionSwitcherItems", () => {
  const collections = [summary("api", "API"), summary("web", "Web")];

  it("switches to the collection its row names", () => {
    const select = vi.fn();
    const items = collectionSwitcherItems(collections, "api", { select, createNew() {} });
    items[1].onSelect();
    expect(select).toHaveBeenCalledWith("web");
  });

  it("puts creation last, behind a separator", () => {
    const items = collectionSwitcherItems(collections, "api", { select() {}, createNew() {} });
    expect(items[2].label).toBe("New collection…");
    expect(items[2].separatorBefore).toBe(true);
  });

  // The TopBar renders whether or not the workspace lists a collection, so this list has to
  // stand alone: with no rows above it, the create action is the whole menu and needs no rule.
  it("is the create action alone, unseparated, in a workspace with no collections", () => {
    const createNew = vi.fn();
    const items = collectionSwitcherItems([], null, { select() {}, createNew });
    expect(labels(items)).toEqual(["New collection…"]);
    expect(items[0].separatorBefore).toBe(false);
    items[0].onSelect();
    expect(createNew).toHaveBeenCalled();
  });
});
