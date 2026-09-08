import { describe, it } from "node:test";
import { fn } from "jest-mock";
import { expect } from "expect";
import type { OpenTab } from "@/lib/ui-store";
import { tabMenuItems, type TabMenuActions } from "./tab-menu";

const tab = (key: string): OpenTab => ({
  key,
  name: key,
  collection: ".",
  kind: "request",
});

const spies = (): TabMenuActions &
  Record<keyof TabMenuActions, ReturnType<typeof fn>> => ({
  close: fn(),
  closeOthers: fn(),
  closeToTheRight: fn(),
  closeAll: fn(),
});

const labels = (items: { label: string }[]): string[] =>
  items.map((i) => i.label);

describe("tabMenuItems", () => {
  it("always offers all four actions, in order", () => {
    const items = tabMenuItems(
      tab("b"),
      [tab("a"), tab("b"), tab("c")],
      spies(),
    );
    expect(labels(items)).toEqual([
      "Close",
      "Close others",
      "Close to the right",
      "Close all",
    ]);
  });

  it("routes each action at the right-clicked tab's key", () => {
    const actions = spies();
    const items = tabMenuItems(
      tab("b"),
      [tab("a"), tab("b"), tab("c")],
      actions,
    );
    items[0].onSelect();
    items[1].onSelect();
    items[2].onSelect();
    items[3].onSelect();
    expect(actions.close).toHaveBeenCalledWith("b");
    expect(actions.closeOthers).toHaveBeenCalledWith("b");
    expect(actions.closeToTheRight).toHaveBeenCalledWith("b");
    expect(actions.closeAll).toHaveBeenCalledWith();
  });

  it("disables Close others on the only open tab", () => {
    const items = tabMenuItems(tab("a"), [tab("a")], spies());
    expect(items[1].disabled).toBe(true);
  });

  it("disables Close to the right on the last tab", () => {
    const items = tabMenuItems(
      tab("c"),
      [tab("a"), tab("b"), tab("c")],
      spies(),
    );
    expect(items[2].disabled).toBe(true);
  });

  it("leaves both enabled on a middle tab among several", () => {
    const items = tabMenuItems(
      tab("b"),
      [tab("a"), tab("b"), tab("c")],
      spies(),
    );
    expect(items[1].disabled).toBeUndefined();
    expect(items[2].disabled).toBeUndefined();
  });

  it("separates Close all from the rest", () => {
    const items = tabMenuItems(tab("a"), [tab("a")], spies());
    expect(items.map((i) => i.separatorBefore ?? false)).toEqual([
      false,
      false,
      false,
      true,
    ]);
  });
});
