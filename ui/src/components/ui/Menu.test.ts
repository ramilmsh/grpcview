import { describe, expect, it } from "vitest";
import { menuPosition, stepMenuIndex } from "./Menu";

const enabled = (n: number): { disabled?: boolean }[] => Array.from({ length: n }, () => ({}));

describe("stepMenuIndex: plain movement", () => {
  it("moves one item per step in each direction", () => {
    expect(stepMenuIndex(enabled(4), 1, 1)).toBe(2);
    expect(stepMenuIndex(enabled(4), 1, -1)).toBe(0);
  });

  it("wraps at both ends", () => {
    expect(stepMenuIndex(enabled(3), 2, 1)).toBe(0);
    expect(stepMenuIndex(enabled(3), 0, -1)).toBe(2);
  });

  it("is a no-op-shaped identity on a single-item menu (it wraps onto itself)", () => {
    expect(stepMenuIndex(enabled(1), 0, 1)).toBe(0);
    expect(stepMenuIndex(enabled(1), 0, -1)).toBe(0);
  });
});

describe("stepMenuIndex: from null (a menu with nothing highlighted yet)", () => {
  it("forward from null is the FIRST item (Home, and the initial highlight)", () => {
    expect(stepMenuIndex(enabled(3), null, 1)).toBe(0);
  });

  it("backward from null is the LAST item (End)", () => {
    expect(stepMenuIndex(enabled(3), null, -1)).toBe(2);
  });

  it("Home/End skip a disabled item at the edge rather than landing on it", () => {
    const items = [{ disabled: true }, {}, {}, { disabled: true }];
    expect(stepMenuIndex(items, null, 1)).toBe(1);
    expect(stepMenuIndex(items, null, -1)).toBe(2);
  });
});

describe("stepMenuIndex: disabled items", () => {
  it("skips a run of disabled items in one step", () => {
    const items = [{}, { disabled: true }, { disabled: true }, {}];
    expect(stepMenuIndex(items, 0, 1)).toBe(3);
    expect(stepMenuIndex(items, 3, -1)).toBe(0);
  });

  it("skips disabled items across the wrap boundary", () => {
    const items = [{ disabled: true }, {}, { disabled: true }];
    expect(stepMenuIndex(items, 1, 1)).toBe(1);
  });

  it("returns null when every item is disabled, rather than spinning", () => {
    expect(stepMenuIndex([{ disabled: true }, { disabled: true }], 0, 1)).toBeNull();
    expect(stepMenuIndex([{ disabled: true }], null, 1)).toBeNull();
  });

  it("returns null for an empty menu", () => {
    expect(stepMenuIndex([], null, 1)).toBeNull();
    expect(stepMenuIndex([], 0, -1)).toBeNull();
  });
});

describe("menuPosition: the common case", () => {
  it("places the card at the click when it fits", () => {
    expect(
      menuPosition({ x: 40, y: 60, width: 180, height: 120, viewportWidth: 1200, viewportHeight: 800 })
    ).toEqual({ left: 40, top: 60 });
  });
});

describe("menuPosition: flipping", () => {
  it("flips UP when the card would overflow the bottom", () => {
    const pos = menuPosition({
      x: 40,
      y: 760,
      width: 180,
      height: 120,
      viewportWidth: 1200,
      viewportHeight: 800,
    });
    expect(pos).toEqual({ left: 40, top: 640 });
  });

  it("flips LEFT when the card would overflow the right edge", () => {
    const pos = menuPosition({
      x: 1150,
      y: 60,
      width: 180,
      height: 120,
      viewportWidth: 1200,
      viewportHeight: 800,
    });
    expect(pos).toEqual({ left: 970, top: 60 });
  });

  it("flips each axis independently", () => {
    const pos = menuPosition({
      x: 40,
      y: 700,
      width: 180,
      height: 200,
      viewportWidth: 1200,
      viewportHeight: 800,
    });
    expect(pos).toEqual({ left: 40, top: 500 });
  });

  it("counts the margin as part of the overflow test, so the card never touches the edge", () => {
    const pos = menuPosition({
      x: 40,
      y: 675,
      width: 180,
      height: 120,
      viewportWidth: 1200,
      viewportHeight: 800,
    });
    expect(pos.top).toBe(555);
  });
});

describe("menuPosition: clamping (what the flip alone cannot fix)", () => {
  it("clamps a flip that would go off the TOP/LEFT back to the margin", () => {
    const pos = menuPosition({
      x: 4,
      y: 30,
      width: 180,
      height: 90,
      viewportWidth: 200,
      viewportHeight: 100,
    });
    expect(pos).toEqual({ left: 6, top: 6 });
  });

  it("pins a card taller than the viewport at the top margin", () => {
    const pos = menuPosition({
      x: 40,
      y: 300,
      width: 180,
      height: 900,
      viewportWidth: 1200,
      viewportHeight: 800,
    });
    expect(pos.top).toBe(6);
  });

  it("honours an explicit margin", () => {
    const pos = menuPosition({
      x: 1190,
      y: 20,
      width: 180,
      height: 120,
      viewportWidth: 1200,
      viewportHeight: 800,
      margin: 20,
    });
    expect(pos).toEqual({ left: 1000, top: 20 });
  });
});
