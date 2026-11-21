// The React/DOM/Monaco half of shortcuts.ts: registering handlers, the one window
// listener, and binding registry shortcuts inside a Monaco editor.
import { useEffect, useRef } from "react";
import type * as Monaco from "monaco-editor";
import {
  combosFor,
  EDITOR_GLOBAL_IDS,
  matchShortcut,
  monacoKeybinding,
  registerShortcut,
  runShortcut,
  shortcutDef,
  shouldDispatch,
  isTypingTarget,
  type ShortcutId,
} from "./shortcuts";

// Registers `handler` for `id` while mounted and `enabled`. Return false to decline (the
// key then keeps its browser default).
export function useShortcut(
  id: ShortcutId,
  handler: () => boolean | void,
  enabled = true,
): void {
  const ref = useRef(handler);
  useEffect(() => {
    ref.current = handler;
  });
  useEffect(
    () => (enabled ? registerShortcut(id, () => ref.current()) : undefined),
    [id, enabled],
  );
}

// Anything that owns the keyboard while open. An overlay a shortcut opened carries
// data-shortcut-owner, so that shortcut can still close it.
const OVERLAY = '[role="dialog"], [role="menu"], [data-popover]';

function openOverlays(): (string | null)[] {
  return [...document.querySelectorAll(OVERLAY)].map((el) =>
    el.getAttribute("data-shortcut-owner"),
  );
}

// The one window keydown listener, bubble phase: anything that handled the key first
// (a popover's Esc, Monaco's own bindings, the tree) preventDefaults and is left alone.
export function useGlobalShortcuts(): void {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented || e.isComposing) return;
      const id = matchShortcut(e);
      if (id === null) return;
      const typing = isTypingTarget(e.target as HTMLElement | null);
      if (!shouldDispatch(id, { typing, overlays: openOverlays() })) return;
      if (runShortcut(id)) {
        e.preventDefault();
        e.stopPropagation();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
}

let editorSeq = 0;

// Binds registry shortcuts inside `editor`: each of `globals` runs through the handler bus
// (so ⌘K/⌘↵ are not swallowed by Monaco's own keymap), each of `local` runs its own
// function. Actions, not addCommand: addCommand's handler is shared by every editor (last
// mounted wins); a per-editor context key scopes each binding to its own editor, and as a
// later dynamic binding it overrides Monaco's default for the same keys (⇧⌥F format).
export function bindEditorShortcuts(
  editor: Monaco.editor.IStandaloneCodeEditor,
  m: typeof Monaco,
  local: Partial<Record<ShortcutId, () => void>>,
  globals: readonly ShortcutId[] = EDITOR_GLOBAL_IDS,
): void {
  const ctx = `grpcviewEditor${++editorSeq}`;
  editor.createContextKey(ctx, true);
  const ids = new Set<ShortcutId>([
    ...globals,
    ...(Object.keys(local) as ShortcutId[]),
  ]);
  for (const id of ids) {
    const keybindings = combosFor(id).map((c) => monacoKeybinding(c, m));
    if (keybindings.length === 0) continue;
    const own = local[id];
    editor.addAction({
      id: `grpcview.shortcut.${id}.${ctx}`,
      label: shortcutDef(id).description,
      keybindings,
      keybindingContext: ctx,
      run: () => {
        if (own) own();
        else runShortcut(id);
      },
    });
  }
}
