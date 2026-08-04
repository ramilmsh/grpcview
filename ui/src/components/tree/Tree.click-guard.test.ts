import { describe, expect, it } from "vitest";
import { isRightClickGesture } from "./Tree";

const IS_MAC = true;
const NOT_MAC = false;

describe("isRightClickGesture: the secondary button", () => {
  it("is true for button 2 on either platform — monaco's own isMouseRightClick", () => {
    expect(isRightClickGesture({ button: 2, ctrlKey: false }, IS_MAC)).toBe(true);
    expect(isRightClickGesture({ button: 2, ctrlKey: false }, NOT_MAC)).toBe(true);
  });

  it("is false for the primary button with no modifier", () => {
    expect(isRightClickGesture({ button: 0, ctrlKey: false }, IS_MAC)).toBe(false);
    expect(isRightClickGesture({ button: 0, ctrlKey: false }, NOT_MAC)).toBe(false);
  });

  it("is false for the middle button — an auxiliary click is not a context click", () => {
    expect(isRightClickGesture({ button: 1, ctrlKey: false }, IS_MAC)).toBe(false);
  });
});

describe("isRightClickGesture: macOS ctrl+click", () => {
  it("is true — ctrl+click IS the secondary-click gesture on macOS", () => {
    expect(isRightClickGesture({ button: 0, ctrlKey: true }, IS_MAC)).toBe(true);
  });

  it("is FALSE for ctrl+click on non-macOS — ctrl is the multi-select modKey there", () => {
    expect(isRightClickGesture({ button: 0, ctrlKey: true }, NOT_MAC)).toBe(false);
  });
});
