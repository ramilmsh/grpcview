import { describe, expect, it } from "vitest";
import { isRightClickGesture } from "./Tree";

// isRightClickGesture is the guard handleRowClick uses to keep a RIGHT-CLICK
// gesture out of the click branches (Tree.tsx, right above `export function
// Tree`) — the finding the plan's §"The review T2 was owed" deferred to T5:
// "macOS ctrl+click falls into the plain-click branch and opens the row ... VS
// Code's MouseController does guard it (isMouseRightClick → skip setSelection) —
// add that guard at T5". What applyRowClick DOES once told is covered in
// dispatch.test.ts; this file covers only the platform decision.
//
// isMac is a parameter rather than a read of platform.ts's IS_MAC, so both
// platforms are exercisable from one test process with no navigator sniffing —
// the same shape keymap.ts's keyToIntent uses. Duck-typed over the two fields it
// reads, which is what lets it run under vitest's `node` environment with no
// jsdom (vitest.config.ts).
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
    // The term that actually fires, and the one VS Code does not have: Firefox
    // delivers a macOS ctrl+click as a `click` with button 0 and ctrlKey true, so
    // a button===2 check alone would miss it. Chromium fires no `click` at all
    // for that gesture, which is why the defect was originally filed as "not
    // reproducible" and deferred rather than dropped.
    expect(isRightClickGesture({ button: 0, ctrlKey: true }, IS_MAC)).toBe(true);
  });

  it("is FALSE for ctrl+click on Windows/Linux — ctrl is the multi-select modKey there", () => {
    // Ungating this term would break cmd/ctrl+click's toggle-one-row branch
    // (applyRowClick) on every non-mac platform, which is a far worse regression
    // than the one it fixes.
    expect(isRightClickGesture({ button: 0, ctrlKey: true }, NOT_MAC)).toBe(false);
  });
});
