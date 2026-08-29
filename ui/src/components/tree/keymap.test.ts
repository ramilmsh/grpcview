import { describe, it } from "node:test";
import { expect } from "expect";
import type { KeyStroke } from "./keymap";
import { keyToIntent } from "./keymap";

const stroke = (
  key: string,
  mods: Partial<Omit<KeyStroke, "key">> = {},
): KeyStroke => ({
  key,
  shiftKey: false,
  metaKey: false,
  ctrlKey: false,
  altKey: false,
  ...mods,
});

describe("keyToIntent: non-macOS (isMac=false)", () => {
  it("bare arrows move focus one row", () => {
    expect(keyToIntent(stroke("ArrowUp"), false)).toEqual({
      kind: "move",
      to: "up",
    });
    expect(keyToIntent(stroke("ArrowDown"), false)).toEqual({
      kind: "move",
      to: "down",
    });
  });

  it("Home/End move to the first/last row", () => {
    expect(keyToIntent(stroke("Home"), false)).toEqual({
      kind: "move",
      to: "first",
    });
    expect(keyToIntent(stroke("End"), false)).toEqual({
      kind: "move",
      to: "last",
    });
  });

  it("PageUp/PageDown move a page", () => {
    expect(keyToIntent(stroke("PageUp"), false)).toEqual({
      kind: "move",
      to: "pageUp",
    });
    expect(keyToIntent(stroke("PageDown"), false)).toEqual({
      kind: "move",
      to: "pageDown",
    });
  });

  it("ArrowLeft/ArrowRight are the structural collapse/expand intents", () => {
    expect(keyToIntent(stroke("ArrowLeft"), false)).toEqual({
      kind: "collapseOrParent",
    });
    expect(keyToIntent(stroke("ArrowRight"), false)).toEqual({
      kind: "expandOrFirstChild",
    });
  });

  it("Space toggles expand/collapse", () => {
    expect(keyToIntent(stroke(" "), false)).toEqual({ kind: "toggle" });
  });

  it("F2 renames", () => {
    expect(keyToIntent(stroke("F2"), false)).toEqual({ kind: "rename" });
  });

  it("Enter opens (not rename) on this platform", () => {
    expect(keyToIntent(stroke("Enter"), false)).toEqual({ kind: "open" });
  });

  it("Delete deletes", () => {
    expect(keyToIntent(stroke("Delete"), false)).toEqual({ kind: "delete" });
  });

  it("a bare Backspace is NOT delete here — the decision is only Delete is, off mac", () => {
    expect(keyToIntent(stroke("Backspace"), false)).toBeNull();
  });

  it("cmd+ArrowDown is not open here — that binding is macOS-only", () => {
    expect(
      keyToIntent(stroke("ArrowDown", { metaKey: true }), false),
    ).toBeNull();
  });

  it("cmd+Backspace is not delete here either — macOS's binding doesn't carry over", () => {
    expect(
      keyToIntent(stroke("Backspace", { metaKey: true }), false),
    ).toBeNull();
  });

  it("shift+ArrowUp/shift+ArrowDown extend the selection from the anchor (T2)", () => {
    expect(keyToIntent(stroke("ArrowUp", { shiftKey: true }), false)).toEqual({
      kind: "extend",
      to: "up",
    });
    expect(keyToIntent(stroke("ArrowDown", { shiftKey: true }), false)).toEqual(
      {
        kind: "extend",
        to: "down",
      },
    );
  });

  it("ctrl+A selects all visible rows on this platform (T2)", () => {
    expect(keyToIntent(stroke("a", { ctrlKey: true }), false)).toEqual({
      kind: "selectAll",
    });
  });

  it('ctrl+A is recognized under caps lock too, where the key arrives as "A" with shiftKey still false', () => {
    expect(keyToIntent(stroke("A", { ctrlKey: true }), false)).toEqual({
      kind: "selectAll",
    });
  });

  it("cmd+A does nothing here — select-all's macOS chord isn't live off-mac", () => {
    expect(keyToIntent(stroke("a", { metaKey: true }), false)).toBeNull();
  });

  it("Escape clears the selection (T2)", () => {
    expect(keyToIntent(stroke("Escape"), false)).toEqual({
      kind: "clearSelection",
    });
  });
});

describe("keyToIntent: macOS (isMac=true)", () => {
  it("bare arrows move focus one row, same as off-mac", () => {
    expect(keyToIntent(stroke("ArrowUp"), true)).toEqual({
      kind: "move",
      to: "up",
    });
    expect(keyToIntent(stroke("ArrowDown"), true)).toEqual({
      kind: "move",
      to: "down",
    });
  });

  it("Home/End move to the first/last row", () => {
    expect(keyToIntent(stroke("Home"), true)).toEqual({
      kind: "move",
      to: "first",
    });
    expect(keyToIntent(stroke("End"), true)).toEqual({
      kind: "move",
      to: "last",
    });
  });

  it("PageUp/PageDown move a page", () => {
    expect(keyToIntent(stroke("PageUp"), true)).toEqual({
      kind: "move",
      to: "pageUp",
    });
    expect(keyToIntent(stroke("PageDown"), true)).toEqual({
      kind: "move",
      to: "pageDown",
    });
  });

  it("ArrowLeft/ArrowRight are the structural collapse/expand intents", () => {
    expect(keyToIntent(stroke("ArrowLeft"), true)).toEqual({
      kind: "collapseOrParent",
    });
    expect(keyToIntent(stroke("ArrowRight"), true)).toEqual({
      kind: "expandOrFirstChild",
    });
  });

  it("Space toggles expand/collapse", () => {
    expect(keyToIntent(stroke(" "), true)).toEqual({ kind: "toggle" });
  });

  it("F2 renames — mac keeps this binding too, as a second way in", () => {
    expect(keyToIntent(stroke("F2"), true)).toEqual({ kind: "rename" });
  });

  it("Enter renames (not open) here — the one intent that is platform-split", () => {
    expect(keyToIntent(stroke("Enter"), true)).toEqual({ kind: "rename" });
  });

  it("cmd+ArrowDown opens — mac's substitute, since Enter is taken by rename", () => {
    expect(keyToIntent(stroke("ArrowDown", { metaKey: true }), true)).toEqual({
      kind: "open",
    });
  });

  it("cmd+Backspace deletes, matching VS Code's moveFileToTrash binding", () => {
    expect(keyToIntent(stroke("Backspace", { metaKey: true }), true)).toEqual({
      kind: "delete",
    });
  });

  it("a bare Delete is NOT bound here — VS Code replaces it with cmd+Backspace, not both", () => {
    expect(keyToIntent(stroke("Delete"), true)).toBeNull();
  });

  it("a bare Backspace (no cmd) is NOT delete here either — only cmd+Backspace is", () => {
    expect(keyToIntent(stroke("Backspace"), true)).toBeNull();
  });

  it("shift+ArrowUp/shift+ArrowDown extend the selection from the anchor (T2), same as off-mac", () => {
    expect(keyToIntent(stroke("ArrowUp", { shiftKey: true }), true)).toEqual({
      kind: "extend",
      to: "up",
    });
    expect(keyToIntent(stroke("ArrowDown", { shiftKey: true }), true)).toEqual({
      kind: "extend",
      to: "down",
    });
  });

  it("cmd+A selects all visible rows on this platform (T2)", () => {
    expect(keyToIntent(stroke("a", { metaKey: true }), true)).toEqual({
      kind: "selectAll",
    });
  });

  it('cmd+A is recognized under caps lock too, where the key arrives as "A" with shiftKey still false', () => {
    expect(keyToIntent(stroke("A", { metaKey: true }), true)).toEqual({
      kind: "selectAll",
    });
  });

  it("ctrl+A does nothing here — select-all's off-mac chord isn't live on macOS", () => {
    expect(keyToIntent(stroke("a", { ctrlKey: true }), true)).toBeNull();
  });

  it("Escape clears the selection (T2), same as off-mac", () => {
    expect(keyToIntent(stroke("Escape"), true)).toEqual({
      kind: "clearSelection",
    });
  });
});

describe("keyToIntent: unclaimed combinations return null, on either platform", () => {
  it("a plain letter is untouched — typeahead is T3 and must not be pre-empted here", () => {
    expect(keyToIntent(stroke("a"), false)).toBeNull();
    expect(keyToIntent(stroke("a"), true)).toBeNull();
  });

  it("alt+ArrowUp returns null — not a combination this table claims", () => {
    expect(keyToIntent(stroke("ArrowUp", { altKey: true }), false)).toBeNull();
    expect(keyToIntent(stroke("ArrowUp", { altKey: true }), true)).toBeNull();
  });

  it("ctrl+ArrowLeft returns null, same reasoning", () => {
    expect(
      keyToIntent(stroke("ArrowLeft", { ctrlKey: true }), false),
    ).toBeNull();
  });

  it("shift+Home / shift+End / shift+PageDown all return null — only the bare keys are claimed", () => {
    expect(keyToIntent(stroke("Home", { shiftKey: true }), false)).toBeNull();
    expect(keyToIntent(stroke("End", { shiftKey: true }), false)).toBeNull();
    expect(
      keyToIntent(stroke("PageDown", { shiftKey: true }), false),
    ).toBeNull();
  });

  it("shift+F2 and cmd+F2 both return null — F2's binding is bare-only on every platform", () => {
    expect(keyToIntent(stroke("F2", { shiftKey: true }), false)).toBeNull();
    expect(keyToIntent(stroke("F2", { metaKey: true }), true)).toBeNull();
  });

  it("cmd+Enter returns null on mac — Enter's mac binding is bare-only too", () => {
    expect(keyToIntent(stroke("Enter", { metaKey: true }), true)).toBeNull();
  });

  it("an extra shift alongside cmd rejects the mac-only bindings — the modifier match is exact", () => {
    expect(
      keyToIntent(stroke("ArrowDown", { metaKey: true, shiftKey: true }), true),
    ).toBeNull();
    expect(
      keyToIntent(stroke("Backspace", { metaKey: true, shiftKey: true }), true),
    ).toBeNull();
  });

  it("shift+PageUp also returns null, same reasoning as its Home/End/PageDown siblings above", () => {
    expect(keyToIntent(stroke("PageUp", { shiftKey: true }), false)).toBeNull();
    expect(keyToIntent(stroke("PageUp", { shiftKey: true }), true)).toBeNull();
  });

  it("ctrl+shift+A and cmd+alt+A both return null — select-all's modifier match is exact, same reasoning as the mac-only pair above", () => {
    expect(
      keyToIntent(stroke("a", { ctrlKey: true, shiftKey: true }), false),
    ).toBeNull();
    expect(
      keyToIntent(stroke("a", { metaKey: true, altKey: true }), true),
    ).toBeNull();
  });

  it("shift+Escape and cmd+Escape both return null — Escape's binding is bare-only, like F2's", () => {
    expect(keyToIntent(stroke("Escape", { shiftKey: true }), false)).toBeNull();
    expect(keyToIntent(stroke("Escape", { metaKey: true }), true)).toBeNull();
  });
});
