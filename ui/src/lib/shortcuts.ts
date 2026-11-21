// The one registry of keyboard shortcuts: every combo, label and description in the app
// comes from here — the global listener, Monaco bindings, tooltips and the cheat sheet.
// Pure: platform is a parameter (defaulting to IS_MAC), so both sides are testable.
import { IS_MAC } from "@/components/tree/platform";

// `mod` is ⌘ (metaKey) on Apple platforms, Ctrl elsewhere. `key` is a letter ("K"), a
// bracket, a KeyboardEvent.key name ("Enter", "ArrowUp", " ") or "?".
export interface Combo {
  mod?: boolean;
  alt?: boolean;
  shift?: boolean;
  key: string;
}

// A combo that differs per platform; a missing side means "not bound there".
export type PlatformCombo = Combo | { apple?: Combo; other?: Combo };

export type ShortcutGroup =
  "Requests" | "Navigation" | "Editor" | "Tree" | "Method picker";

export const SHORTCUT_GROUPS: readonly ShortcutGroup[] = [
  "Requests",
  "Navigation",
  "Editor",
  "Tree",
  "Method picker",
];

export interface ShortcutDef {
  group: ShortcutGroup;
  description: string;
  combos: readonly PlatformCombo[];
  // "global": the window listener dispatches it (and request editors bind it too);
  // "editor": only Monaco bindings dispatch it; "info": documented, handled elsewhere
  // (tree keymap.ts, method picker) — listed so the cheat sheet is complete.
  dispatch: "global" | "editor" | "info";
  // The tree intent kind this entry documents; keymap.ts is checked against it in tests.
  treeIntent?: string;
}

const mod = (key: string, extra: Omit<Combo, "key" | "mod"> = {}): Combo => ({
  mod: true,
  key,
  ...extra,
});
const alt = (key: string): Combo => ({ alt: true, key });
const bare = (key: string): Combo => ({ key });

export const SHORTCUTS = {
  invoke: {
    group: "Requests",
    description: "Invoke the open request",
    combos: [mod("Enter")],
    dispatch: "global",
  },
  save: {
    group: "Requests",
    description: "Save (requests save automatically)",
    combos: [mod("S")],
    dispatch: "global",
  },
  newRequest: {
    group: "Requests",
    description: "New request",
    combos: [alt("N")],
    dispatch: "global",
  },
  quickOpen: {
    group: "Navigation",
    description: "Quick open a request",
    combos: [mod("K")],
    dispatch: "global",
  },
  closeTab: {
    group: "Navigation",
    description: "Close tab",
    combos: [alt("W")],
    dispatch: "global",
  },
  prevTab: {
    group: "Navigation",
    description: "Previous tab",
    combos: [alt("[")],
    dispatch: "global",
  },
  nextTab: {
    group: "Navigation",
    description: "Next tab",
    combos: [alt("]")],
    dispatch: "global",
  },
  cheatSheet: {
    group: "Navigation",
    description: "Show keyboard shortcuts",
    combos: [bare("?")],
    dispatch: "global",
  },
  format: {
    group: "Editor",
    description: "Format body / metadata",
    combos: [{ shift: true, alt: true, key: "F" }],
    dispatch: "editor",
  },
  selectBody: {
    group: "Editor",
    description: "Select the editable region",
    combos: [mod("A")],
    dispatch: "editor",
  },
  scriptRun: {
    group: "Editor",
    description: "Scripts: test run",
    combos: [mod("Enter")],
    dispatch: "editor",
  },
  scriptSave: {
    group: "Editor",
    description: "Scripts: save",
    combos: [mod("S")],
    dispatch: "editor",
  },
  treeMove: {
    group: "Tree",
    description: "Move focus",
    combos: [
      bare("ArrowUp"),
      bare("ArrowDown"),
      bare("Home"),
      bare("End"),
      bare("PageUp"),
      bare("PageDown"),
    ],
    dispatch: "info",
    treeIntent: "move",
  },
  treeExtend: {
    group: "Tree",
    description: "Extend selection",
    combos: [
      { shift: true, key: "ArrowUp" },
      { shift: true, key: "ArrowDown" },
    ],
    dispatch: "info",
    treeIntent: "extend",
  },
  treeCollapse: {
    group: "Tree",
    description: "Collapse / go to parent",
    combos: [bare("ArrowLeft")],
    dispatch: "info",
    treeIntent: "collapseOrParent",
  },
  treeExpand: {
    group: "Tree",
    description: "Expand / go to first child",
    combos: [bare("ArrowRight")],
    dispatch: "info",
    treeIntent: "expandOrFirstChild",
  },
  treeToggle: {
    group: "Tree",
    description: "Toggle selection",
    combos: [bare(" ")],
    dispatch: "info",
    treeIntent: "toggle",
  },
  treeOpen: {
    group: "Tree",
    description: "Open",
    combos: [{ apple: mod("ArrowDown"), other: bare("Enter") }],
    dispatch: "info",
    treeIntent: "open",
  },
  treeRename: {
    group: "Tree",
    description: "Rename",
    combos: [bare("F2"), { apple: bare("Enter") }],
    dispatch: "info",
    treeIntent: "rename",
  },
  treeDelete: {
    group: "Tree",
    description: "Delete",
    combos: [{ apple: mod("Backspace"), other: bare("Delete") }],
    dispatch: "info",
    treeIntent: "delete",
  },
  treeSelectAll: {
    group: "Tree",
    description: "Select all",
    combos: [mod("A")],
    dispatch: "info",
    treeIntent: "selectAll",
  },
  treeClear: {
    group: "Tree",
    description: "Clear selection",
    combos: [bare("Escape")],
    dispatch: "info",
    treeIntent: "clearSelection",
  },
  pickerMove: {
    group: "Method picker",
    description: "Move",
    combos: [bare("ArrowUp"), bare("ArrowDown")],
    dispatch: "info",
  },
  pickerPick: {
    group: "Method picker",
    description: "Pick",
    combos: [bare("Enter")],
    dispatch: "info",
  },
  pickerClose: {
    group: "Method picker",
    description: "Clear the query, then close",
    combos: [bare("Escape")],
    dispatch: "info",
  },
} satisfies Record<string, ShortcutDef>;

export type ShortcutId = keyof typeof SHORTCUTS;

export const SHORTCUT_IDS = Object.keys(SHORTCUTS) as ShortcutId[];

export const shortcutDef = (id: ShortcutId): ShortcutDef => SHORTCUTS[id];

// The combos bound on this platform.
export function combosFor(id: ShortcutId, apple = IS_MAC): Combo[] {
  const out: Combo[] = [];
  for (const c of shortcutDef(id).combos) {
    const one = "key" in c ? c : apple ? c.apple : c.other;
    if (one) out.push(one);
  }
  return out;
}

const APPLE_KEYS: Record<string, string> = {
  Enter: "↵",
  Escape: "Esc",
  Backspace: "⌫",
  Delete: "⌦",
  ArrowUp: "↑",
  ArrowDown: "↓",
  ArrowLeft: "←",
  ArrowRight: "→",
  PageUp: "PgUp",
  PageDown: "PgDn",
  " ": "Space",
};
const OTHER_KEYS: Record<string, string> = {
  ...APPLE_KEYS,
  Enter: "Enter",
  Backspace: "Backspace",
  Delete: "Delete",
};

// Apple: symbols, no separators ("⇧⌥F", "⌘↵"); elsewhere "Shift+Alt+F", "Ctrl+Enter".
export function formatCombo(c: Combo, apple = IS_MAC): string {
  if (apple) {
    const key = APPLE_KEYS[c.key] ?? c.key;
    return `${c.shift ? "⇧" : ""}${c.alt ? "⌥" : ""}${c.mod ? "⌘" : ""}${key}`;
  }
  const parts: string[] = [];
  if (c.mod) parts.push("Ctrl");
  if (c.shift) parts.push("Shift");
  if (c.alt) parts.push("Alt");
  parts.push(OTHER_KEYS[c.key] ?? c.key);
  return parts.join("+");
}

// Every label of `id` on this platform; [] when it is not bound here.
export const shortcutLabels = (id: ShortcutId, apple = IS_MAC): string[] =>
  combosFor(id, apple).map((c) => formatCombo(c, apple));

// The primary label, e.g. for a tooltip.
export const shortcutLabel = (id: ShortcutId, apple = IS_MAC): string =>
  shortcutLabels(id, apple)[0] ?? "";

// "Invoke (⌘↵)".
export function withShortcut(text: string, id: ShortcutId, apple = IS_MAC) {
  const label = shortcutLabel(id, apple);
  return label ? `${text} (${label})` : text;
}

export interface KeyEventLike {
  key: string;
  code: string;
  metaKey: boolean;
  ctrlKey: boolean;
  altKey: boolean;
  shiftKey: boolean;
}

// Letters and brackets match on the PHYSICAL key: on macOS ⌥ rewrites `key`
// (⌥W → "∑", ⌥N → "Dead"), so `key` cannot be trusted there.
function codeFor(key: string): string | null {
  if (/^[A-Z]$/.test(key)) return `Key${key}`;
  if (/^[0-9]$/.test(key)) return `Digit${key}`;
  if (key === "[") return "BracketLeft";
  if (key === "]") return "BracketRight";
  return null;
}

export function matchesCombo(
  e: KeyEventLike,
  c: Combo,
  apple = IS_MAC,
): boolean {
  const modHeld = apple ? e.metaKey : e.ctrlKey;
  const strayHeld = apple ? e.ctrlKey : e.metaKey;
  if (strayHeld || modHeld !== !!c.mod || e.altKey !== !!c.alt) return false;
  // "?" is shifted on most layouts and unshifted on some: shift is not part of it.
  if (c.key !== "?" && e.shiftKey !== !!c.shift) return false;
  const code = codeFor(c.key);
  return code ? e.code === code : e.key === c.key;
}

// The first dispatchable shortcut `e` triggers, or null. `kinds` selects which tiers.
export function matchShortcut(
  e: KeyEventLike,
  apple = IS_MAC,
  kinds: readonly ShortcutDef["dispatch"][] = ["global"],
): ShortcutId | null {
  for (const id of SHORTCUT_IDS) {
    if (!kinds.includes(shortcutDef(id).dispatch)) continue;
    if (combosFor(id, apple).some((c) => matchesCombo(e, c, apple))) return id;
  }
  return null;
}

// A shortcut with no modifier ("?", Esc) would eat typing, so it never fires from a text
// control or an editor.
export const isPlainShortcut = (id: ShortcutId, apple = IS_MAC): boolean =>
  combosFor(id, apple).every((c) => !c.mod && !c.alt);

export interface TargetLike {
  tagName?: string;
  isContentEditable?: boolean;
  closest?: (selector: string) => unknown;
}

export function isTypingTarget(t: TargetLike | null | undefined): boolean {
  if (!t) return false;
  if (t.isContentEditable || t.tagName === "INPUT" || t.tagName === "TEXTAREA")
    return true;
  return !!t.closest?.(".monaco-editor");
}

// Whether the global listener may run `id` now. Open dialogs/popovers/menus keep every
// key, except the one overlay a shortcut owns (⌘K closes quick open, ? the cheat sheet).
export function shouldDispatch(
  id: ShortcutId,
  ctx: { typing: boolean; overlays: readonly (string | null)[] },
  apple = IS_MAC,
): boolean {
  if (ctx.overlays.some((owner) => owner !== id)) return false;
  return !(ctx.typing && isPlainShortcut(id, apple));
}

// The shortcuts the request editors bind in Monaco, so its own keymap (⌘K chords, ⌘↵
// insert-line) does not shadow them.
export const EDITOR_GLOBAL_IDS: readonly ShortcutId[] = [
  "invoke",
  "save",
  "quickOpen",
  "newRequest",
  "closeTab",
  "prevTab",
  "nextTab",
];

export interface MonacoKeys {
  KeyMod: { CtrlCmd: number; Shift: number; Alt: number };
  KeyCode: object;
}

const MONACO_KEYS: Record<string, string> = {
  "[": "BracketLeft",
  "]": "BracketRight",
  ArrowUp: "UpArrow",
  ArrowDown: "DownArrow",
  ArrowLeft: "LeftArrow",
  ArrowRight: "RightArrow",
  " ": "Space",
};

// A combo as a Monaco keybinding number (KeyMod.CtrlCmd is ⌘ on macOS, Ctrl elsewhere).
export function monacoKeybinding(c: Combo, m: MonacoKeys): number {
  const name = /^[A-Z]$/.test(c.key)
    ? `Key${c.key}`
    : (MONACO_KEYS[c.key] ?? c.key);
  const code = (m.KeyCode as Record<string, number | undefined>)[name];
  if (code === undefined) throw new Error(`no Monaco key for ${c.key}`);
  return (
    code |
    (c.mod ? m.KeyMod.CtrlCmd : 0) |
    (c.shift ? m.KeyMod.Shift : 0) |
    (c.alt ? m.KeyMod.Alt : 0)
  );
}

// The handler bus: components register what a shortcut does while they are mounted; the
// global listener and Monaco bindings both run through it. The newest registration wins.
type Handler = () => boolean | void;
const handlers = new Map<ShortcutId, Handler[]>();

export function registerShortcut(id: ShortcutId, h: Handler): () => void {
  const stack = handlers.get(id) ?? [];
  stack.push(h);
  handlers.set(id, stack);
  return () => {
    const s = handlers.get(id);
    const i = s?.lastIndexOf(h) ?? -1;
    if (s && i !== -1) s.splice(i, 1);
  };
}

// Runs the current handler; false when none is registered or it declined (returned false).
export function runShortcut(id: ShortcutId): boolean {
  const stack = handlers.get(id);
  const h = stack?.[stack.length - 1];
  if (!h) return false;
  return h() !== false;
}
