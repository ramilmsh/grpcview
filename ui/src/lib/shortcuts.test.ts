import { describe, it } from "node:test";
import { expect } from "expect";
import { keyToIntent } from "@/components/tree/keymap";
import {
  combosFor,
  formatCombo,
  isPlainShortcut,
  isTypingTarget,
  matchShortcut,
  monacoKeybinding,
  registerShortcut,
  runShortcut,
  SHORTCUT_GROUPS,
  SHORTCUT_IDS,
  shortcutDef,
  shortcutLabel,
  shortcutLabels,
  shouldDispatch,
  withShortcut,
  type Combo,
  type KeyEventLike,
} from "./shortcuts";

const ev = (over: Partial<KeyEventLike>): KeyEventLike => ({
  key: "",
  code: "",
  metaKey: false,
  ctrlKey: false,
  altKey: false,
  shiftKey: false,
  ...over,
});

describe("labels", () => {
  it("Apple: symbols, no separators", () => {
    expect(shortcutLabel("invoke", true)).toBe("⌘↵");
    expect(shortcutLabel("quickOpen", true)).toBe("⌘K");
    expect(shortcutLabel("closeTab", true)).toBe("⌥W");
    expect(shortcutLabel("format", true)).toBe("⇧⌥F");
    expect(shortcutLabel("prevTab", true)).toBe("⌥[");
    expect(shortcutLabel("nextTab", true)).toBe("⌥]");
    expect(shortcutLabel("cheatSheet", true)).toBe("?");
  });
  it("others: words joined with +", () => {
    expect(shortcutLabel("invoke", false)).toBe("Ctrl+Enter");
    expect(shortcutLabel("quickOpen", false)).toBe("Ctrl+K");
    expect(shortcutLabel("closeTab", false)).toBe("Alt+W");
    expect(shortcutLabel("format", false)).toBe("Shift+Alt+F");
    expect(shortcutLabel("prevTab", false)).toBe("Alt+[");
    expect(shortcutLabel("save", false)).toBe("Ctrl+S");
  });
  it("platform-only combos drop out", () => {
    expect(shortcutLabels("treeDelete", true)).toEqual(["⌘⌫"]);
    expect(shortcutLabels("treeDelete", false)).toEqual(["Delete"]);
    expect(shortcutLabels("treeRename", true)).toEqual(["F2", "↵"]);
    expect(shortcutLabels("treeRename", false)).toEqual(["F2"]);
    expect(formatCombo({ key: " " }, false)).toBe("Space");
  });
  it("withShortcut appends the label", () => {
    expect(withShortcut("Invoke", "invoke", true)).toBe("Invoke (⌘↵)");
    expect(withShortcut("Close", "closeTab", false)).toBe("Close (Alt+W)");
  });
});

describe("matchShortcut", () => {
  it("Mod is ⌘ on Apple, Ctrl elsewhere — never the other", () => {
    const cmdK = ev({ key: "k", code: "KeyK", metaKey: true });
    const ctrlK = ev({ key: "k", code: "KeyK", ctrlKey: true });
    expect(matchShortcut(cmdK, true)).toBe("quickOpen");
    expect(matchShortcut(ctrlK, true)).toBeNull();
    expect(matchShortcut(ctrlK, false)).toBe("quickOpen");
    expect(matchShortcut(cmdK, false)).toBeNull();
    expect(
      matchShortcut(ev({ key: "Enter", code: "Enter", metaKey: true }), true),
    ).toBe("invoke");
    expect(
      matchShortcut(ev({ key: "Enter", code: "Enter", ctrlKey: true }), false),
    ).toBe("invoke");
  });
  it("letters/brackets match by code, so ⌥'s symbol keys still match", () => {
    const opt = (key: string, code: string) => ev({ key, code, altKey: true });
    expect(matchShortcut(opt("∑", "KeyW"), true)).toBe("closeTab");
    expect(matchShortcut(opt("Dead", "KeyN"), true)).toBe("newRequest");
    expect(matchShortcut(opt("“", "BracketLeft"), true)).toBe("prevTab");
    expect(matchShortcut(opt("‘", "BracketRight"), true)).toBe("nextTab");
    expect(matchShortcut(opt("w", "KeyW"), false)).toBe("closeTab");
    // ⇧⌥F reports "Ï" on macOS; format is an editor binding, not a global one.
    const fmt = ev({ key: "Ï", code: "KeyF", altKey: true, shiftKey: true });
    expect(matchShortcut(fmt, true)).toBeNull();
    expect(matchShortcut(fmt, true, ["editor"])).toBe("format");
  });
  it("modifiers are exact", () => {
    expect(
      matchShortcut(
        ev({ key: "W", code: "KeyW", altKey: true, shiftKey: true }),
      ),
    ).toBeNull();
    expect(
      matchShortcut(
        ev({ key: "k", code: "KeyK", metaKey: true, altKey: true }),
        true,
      ),
    ).toBeNull();
  });
  it("? ignores shift; Esc matches by key", () => {
    expect(matchShortcut(ev({ key: "?", code: "Slash", shiftKey: true }))).toBe(
      "cheatSheet",
    );
    expect(matchShortcut(ev({ key: "?", code: "Minus" }))).toBe("cheatSheet");
    expect(matchShortcut(ev({ key: "?", ctrlKey: true }), false)).toBeNull();
    expect(matchShortcut(ev({ key: "Escape", code: "Escape" }))).toBeNull();
  });
});

describe("shouldDispatch", () => {
  it("plain keys never fire while typing; modified ones do", () => {
    expect(isPlainShortcut("cheatSheet")).toBe(true);
    expect(isPlainShortcut("closeTab")).toBe(false);
    expect(shouldDispatch("cheatSheet", { typing: true, overlays: [] })).toBe(
      false,
    );
    expect(shouldDispatch("cheatSheet", { typing: false, overlays: [] })).toBe(
      true,
    );
    expect(shouldDispatch("quickOpen", { typing: true, overlays: [] })).toBe(
      true,
    );
  });
  it("an open overlay keeps every key but its owner's", () => {
    expect(
      shouldDispatch("quickOpen", { typing: false, overlays: [null] }),
    ).toBe(false);
    expect(
      shouldDispatch("quickOpen", { typing: true, overlays: ["quickOpen"] }),
    ).toBe(true);
    expect(
      shouldDispatch("closeTab", { typing: true, overlays: ["quickOpen"] }),
    ).toBe(false);
    expect(
      shouldDispatch("cheatSheet", { typing: false, overlays: ["cheatSheet"] }),
    ).toBe(true);
  });
  it("isTypingTarget covers inputs, contenteditable and Monaco", () => {
    expect(isTypingTarget({ tagName: "INPUT" })).toBe(true);
    expect(isTypingTarget({ tagName: "TEXTAREA" })).toBe(true);
    expect(isTypingTarget({ tagName: "DIV", isContentEditable: true })).toBe(
      true,
    );
    expect(
      isTypingTarget({
        tagName: "DIV",
        closest: (s) => (s === ".monaco-editor" ? {} : null),
      }),
    ).toBe(true);
    expect(isTypingTarget({ tagName: "BUTTON", closest: () => null })).toBe(
      false,
    );
    expect(isTypingTarget(null)).toBe(false);
  });
});

describe("monacoKeybinding", () => {
  const m = {
    KeyMod: { CtrlCmd: 2048, Shift: 1024, Alt: 512 },
    KeyCode: { Enter: 3, KeyF: 36, KeyS: 49, BracketLeft: 92 },
  };
  it("maps mods and keys", () => {
    expect(monacoKeybinding({ mod: true, key: "Enter" }, m)).toBe(2048 | 3);
    expect(monacoKeybinding({ shift: true, alt: true, key: "F" }, m)).toBe(
      1024 | 512 | 36,
    );
    expect(monacoKeybinding({ alt: true, key: "[" }, m)).toBe(512 | 92);
    expect(() => monacoKeybinding({ key: "?" }, m)).toThrow();
  });
});

describe("handler bus", () => {
  it("newest registration wins; false declines; unregister restores", () => {
    const calls: string[] = [];
    expect(runShortcut("nextTab")).toBe(false);
    const a = registerShortcut("nextTab", () => void calls.push("a"));
    const b = registerShortcut("nextTab", () => false);
    expect(runShortcut("nextTab")).toBe(false);
    b();
    expect(runShortcut("nextTab")).toBe(true);
    a();
    expect(runShortcut("nextTab")).toBe(false);
    expect(calls).toEqual(["a"]);
  });
});

describe("registry", () => {
  it("every entry is in a known group and labelled on some platform", () => {
    for (const id of SHORTCUT_IDS) {
      expect(SHORTCUT_GROUPS).toContain(shortcutDef(id).group);
      expect(
        shortcutLabels(id, true).length + shortcutLabels(id, false).length,
      ).toBeGreaterThan(0);
    }
  });
  it("no two global shortcuts share a combo", () => {
    for (const apple of [true, false]) {
      const seen = new Map<string, string>();
      for (const id of SHORTCUT_IDS) {
        if (shortcutDef(id).dispatch !== "global") continue;
        for (const c of combosFor(id, apple)) {
          const label = formatCombo(c, apple);
          expect(seen.get(label)).toBeUndefined();
          seen.set(label, id);
        }
      }
    }
  });
  it("tree entries document exactly what keymap.ts does", () => {
    const stroke = (c: Combo, apple: boolean) => ({
      key: c.key,
      shiftKey: !!c.shift,
      altKey: !!c.alt,
      metaKey: apple && !!c.mod,
      ctrlKey: !apple && !!c.mod,
    });
    for (const apple of [true, false]) {
      for (const id of SHORTCUT_IDS) {
        const intent = shortcutDef(id).treeIntent;
        if (!intent) continue;
        for (const c of combosFor(id, apple))
          expect([id, keyToIntent(stroke(c, apple), apple)?.kind]).toEqual([
            id,
            intent,
          ]);
      }
    }
  });
});
