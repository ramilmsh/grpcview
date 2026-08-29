// Pure keyboard/mouse decisions: intent or click -> TreeAction[]. Tree.tsx is the
// interpreter that applies them.

import type { FlatTree } from "./flatten";
import type { TreeIntent } from "./keymap";
import type { TreeRowModel } from "./types";
import {
  targetIndex,
  parentIndex,
  firstChildIndex,
  descendantIds,
} from "./navigate";
import {
  rangeSelection,
  replaceSelection,
  selectAll,
  toggleSelection,
} from "./selection";

export type TreeAction =
  // `scroll` is per-producer: keyboard moves bring the row into view, mouse
  // focus must not (scrollIntoView is not a no-op for a partially clipped row).
  | { kind: "focus"; id: string; scroll: boolean }
  // A desired final state, not a toggle.
  | { kind: "setExpanded"; id: string; expanded: boolean }
  | { kind: "setSelection"; ids: readonly string[] }
  | { kind: "setAnchor"; id: string | null }
  // A real activation — never emitted for an expandable row, which toggles instead.
  | { kind: "open"; id: string }
  | { kind: "requestRename"; id: string }
  | { kind: "delete"; ids: readonly string[] };

export interface ApplyIntentCtx<T> {
  flat: FlatTree<T>;
  focused: string | null;
  selection: readonly string[];
  anchor: string | null;
  rowsPerPage: number;
}

function rowFor<T>(
  flat: FlatTree<T>,
  id: string | null,
): TreeRowModel<T> | null {
  if (id === null) return null;
  return flat.rows[flat.indexById.get(id) ?? -1] ?? null;
}

function resolveDeleteIds(
  focused: string | null,
  selection: readonly string[],
): string[] {
  if (focused === null) return [...selection];
  return selection.includes(focused) ? [...selection] : [focused];
}

export function applyIntent<T>(
  intent: TreeIntent,
  ctx: ApplyIntentCtx<T>,
): TreeAction[] {
  const { flat, focused, selection, anchor, rowsPerPage } = ctx;
  const focusedRow = rowFor(flat, focused);

  switch (intent.kind) {
    case "move": {
      const idx = targetIndex(flat, focused, intent.to, rowsPerPage);
      if (idx === null) return [];
      const id = flat.rows[idx].id;
      return [
        { kind: "focus", id, scroll: true },
        { kind: "setAnchor", id },
      ];
    }

    case "extend": {
      const idx = targetIndex(flat, focused, intent.to, rowsPerPage);
      if (idx === null) return [];
      const id = flat.rows[idx].id;

      // On the first shift+arrow the anchor bootstraps to the row focus is
      // leaving; on later ones it is already the pivot, so the range grows.
      const effectiveAnchor = anchor ?? focused;

      return [
        { kind: "focus", id, scroll: true },
        { kind: "setAnchor", id: effectiveAnchor },
        {
          kind: "setSelection",
          ids: rangeSelection(flat.rows, effectiveAnchor, id),
        },
      ];
    }

    case "collapseOrParent": {
      if (!focusedRow) return [];
      if (focusedRow.expandable && focusedRow.expanded) {
        return [{ kind: "setExpanded", id: focusedRow.id, expanded: false }];
      }
      const idx = parentIndex(flat, focusedRow.id);
      if (idx === null) return [];
      return [{ kind: "focus", id: flat.rows[idx].id, scroll: true }];
    }

    case "expandOrFirstChild": {
      if (!focusedRow) return [];
      if (focusedRow.expandable && !focusedRow.expanded) {
        return [{ kind: "setExpanded", id: focusedRow.id, expanded: true }];
      }
      const idx = firstChildIndex(flat, focusedRow.id);
      if (idx === null) return [];
      return [{ kind: "focus", id: flat.rows[idx].id, scroll: true }];
    }

    case "toggle":
      return focusedRow?.expandable
        ? [
            {
              kind: "setExpanded",
              id: focusedRow.id,
              expanded: !focusedRow.expanded,
            },
          ]
        : [];

    case "open": {
      // Enter on a folder toggles rather than "opening" it, as in VS Code.
      if (!focusedRow) return [];
      const actions: TreeAction[] = [
        { kind: "setSelection", ids: [focusedRow.id] },
      ];
      if (focusedRow.expandable) {
        actions.push({
          kind: "setExpanded",
          id: focusedRow.id,
          expanded: !focusedRow.expanded,
        });
      } else {
        actions.push({ kind: "open", id: focusedRow.id });
      }
      return actions;
    }

    case "rename":
      return focusedRow ? [{ kind: "requestRename", id: focusedRow.id }] : [];

    case "delete": {
      const ids = resolveDeleteIds(focusedRow?.id ?? null, selection);
      if (ids.length === 0) return [];
      return [{ kind: "delete", ids }];
    }

    case "selectAll":
      return [
        { kind: "setSelection", ids: selectAll(flat.rows) },
        { kind: "setAnchor", id: null },
      ];

    case "clearSelection":
      if (selection.length === 0) return [];
      return [
        { kind: "setSelection", ids: [] },
        { kind: "setAnchor", id: null },
      ];
  }
}

// Already resolved to the platform-correct chord by the caller: `modKey` is cmd
// on macOS, ctrl elsewhere.
export interface ClickMods {
  shiftKey: boolean;
  modKey: boolean;
  // A right-click gesture. The same gesture also fires `contextmenu`, and
  // Tree.tsx's handler there owns focus/anchor/selection for it — so this
  // function emits nothing at all.
  rightButton: boolean;
}

export type ApplyClickCtx<T> = Omit<ApplyIntentCtx<T>, "rowsPerPage">;

// shift is checked before modKey, so a shift+cmd+click takes the range branch.
export function applyRowClick<T>(
  row: TreeRowModel<T>,
  mods: ClickMods,
  ctx: ApplyClickCtx<T>,
): TreeAction[] {
  const { flat, focused, selection, anchor } = ctx;

  if (mods.rightButton) return [];

  if (mods.shiftKey) {
    // Focus moves to the clicked row; the anchor only bootstraps if unset.
    const effectiveAnchor = anchor ?? focused;
    return [
      { kind: "focus", id: row.id, scroll: false },
      { kind: "setAnchor", id: effectiveAnchor },
      {
        kind: "setSelection",
        ids: rangeSelection(flat.rows, effectiveAnchor, row.id),
      },
    ];
  }

  if (mods.modKey) {
    // Pure selection: never opens, never touches expansion.
    return [
      { kind: "focus", id: row.id, scroll: false },
      { kind: "setAnchor", id: row.id },
      { kind: "setSelection", ids: toggleSelection(selection, row.id) },
    ];
  }

  const actions: TreeAction[] = [
    { kind: "setSelection", ids: replaceSelection(row.id) },
    { kind: "focus", id: row.id, scroll: false },
    { kind: "setAnchor", id: row.id },
  ];
  if (row.expandable) {
    actions.push({ kind: "setExpanded", id: row.id, expanded: !row.expanded });
  } else {
    actions.push({ kind: "open", id: row.id });
  }
  return actions;
}

// A twistie click toggles without changing selection, modifiers ignored — but a
// COLLAPSE hides rows, so a focus or selection pointing inside is rebased onto the
// folder. Without that, aria-activedescendant names nothing (and ArrowDown jumps
// to row 0), and Delete acts on rows the user cannot see.
export function applyTwistieClick<T>(
  row: TreeRowModel<T>,
  ctx: ApplyClickCtx<T>,
): TreeAction[] {
  const { flat, focused, selection } = ctx;
  const actions: TreeAction[] = [
    { kind: "setExpanded", id: row.id, expanded: !row.expanded },
  ];
  if (!row.expanded) return actions;

  const hidden = new Set(descendantIds(flat, row.id));
  if (hidden.size === 0) return actions;

  if (focused !== null && hidden.has(focused)) {
    actions.push({ kind: "focus", id: row.id, scroll: false });
  }

  const rebased: string[] = [];
  let changed = false;
  for (const id of selection) {
    const mapped = hidden.has(id) ? row.id : id;
    if (mapped !== id) changed = true;
    if (!rebased.includes(mapped)) rebased.push(mapped);
    else changed = true;
  }
  if (changed) actions.push({ kind: "setSelection", ids: rebased });

  return actions;
}
