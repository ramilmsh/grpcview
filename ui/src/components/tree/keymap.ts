// key event -> intent, ONE table, no DOM (docs/design/tree-rewrite-plan.md's
// module table: "keymap.ts — key event → intent, one table, no DOM"). Takes a
// plain KeyStroke object below, never a React.KeyboardEvent or a DOM
// KeyboardEvent — so this file needs no DOM and no React to unit-test
// (keymap.test.ts), and Tree.tsx (the only real caller, wired up in the phase
// after this one) is the one place that has to know how to turn a real keyboard
// event into this shape. `isMac` is likewise a plain boolean PARAMETER rather
// than something this file resolves itself — see platform.ts's header comment
// for why that split exists (short version: it's what lets both platforms' rows
// be exercised from the one process vitest runs, in one test file).
//
// This module knows nothing about the tree's actual rows, indices, or expansion
// state — only intents. The array arithmetic a "move" (or "collapseOrParent" /
// "expandOrFirstChild") intent implies once it reaches a real FlatTree lives in
// ./navigate.ts instead, deliberately: keeping that arithmetic out of here is
// what lets this file stay a flat, readable TABLE (every row a `case`), and
// keeping key events out of navigate.ts is what lets that file be tested against
// plain row-array fixtures with no notion of "F2" or "shiftKey" at all.
//
// THE PLATFORM SPLIT IS THE POINT of this table (tree-rewrite-plan.md's "VS Code
// UX spec" — settled 2026-07-31, reversing an earlier draft that bound one table
// everywhere, identically on every platform). Row by row:
//   - Enter is the one key whose INTENT differs by platform: VS Code's
//     renameFile binds plain Enter on macOS; everywhere else Enter opens.
//   - F2 renames on every platform (VS Code binds it everywhere too — macOS just
//     has a second way in, via Enter).
//   - cmd+ArrowDown opens, but ONLY on macOS: macOS's Enter is taken by rename,
//     so VS Code gives it a distinct "open" gesture. Windows/Linux never need
//     one, since their Enter already opens — so cmd+ArrowDown there is simply
//     unclaimed (null).
//   - Delete's macOS binding is cmd+Backspace (VS Code's moveFileToTrash), not a
//     bare key at all — see the "Delete"/default cases below for the full
//     reasoning, including the one place this table leaves a modifier
//     combination unspecified and this file has to make (and justify) the call.
//   - shift+Arrow is deliberately UNHANDLED here — extending a selection is T2
//     (multi-select), not built yet. See the comment at the modifier gate below:
//     a future reader seeing no "shift" row should read this as the boundary of
//     T1's scope, not as an oversight.

export interface KeyStroke {
  key: string;
  shiftKey: boolean;
  metaKey: boolean;
  ctrlKey: boolean;
  altKey: boolean;
}

export type TreeIntent =
  | { kind: "move"; to: "up" | "down" | "first" | "last" | "pageUp" | "pageDown" }
  | { kind: "collapseOrParent" } // Left
  | { kind: "expandOrFirstChild" } // Right
  | { kind: "toggle" } // Space
  | { kind: "open" }
  | { kind: "rename" }
  | { kind: "delete" };

// Exactly cmd, no other modifier riding along — the shape of BOTH macOS-only
// bindings below. Written as an exact match (not just "metaKey is down") so e.g.
// cmd+shift+Backspace or cmd+alt+ArrowDown fall through to null instead of being
// silently accepted as the same intent as the unmodified combo.
function onlyMetaHeld(stroke: KeyStroke): boolean {
  return stroke.metaKey && !stroke.shiftKey && !stroke.ctrlKey && !stroke.altKey;
}

// No shift/meta/ctrl/alt at all — the shape of every OTHER row in this table.
function noModifiersHeld(stroke: KeyStroke): boolean {
  return !stroke.shiftKey && !stroke.metaKey && !stroke.ctrlKey && !stroke.altKey;
}

export function keyToIntent(stroke: KeyStroke, isMac: boolean): TreeIntent | null {
  const { key } = stroke;

  // The two macOS-only bindings ride exactly cmd, which the blanket modifier
  // gate just below rejects outright for every other row — so they're resolved
  // first, before that gate ever runs.
  if (isMac && onlyMetaHeld(stroke)) {
    if (key === "ArrowDown") return { kind: "open" }; // macOS's Enter is taken by rename
    if (key === "Backspace") return { kind: "delete" }; // VS Code's moveFileToTrash binding
    // Any other bare-cmd key (cmd+F2, cmd+ArrowUp, …) falls through to the
    // modifier gate below and returns null: not a row this table claims.
  }

  // Every remaining row is a BARE key: no shift, no meta/cmd, no ctrl, no alt.
  // This is where shift+Arrow is rejected (see the file header — T2's job, not
  // T1's), and where every other unclaimed modifier combination this table
  // doesn't mention (alt+ArrowUp, ctrl+Home, cmd+F2, shift+Space, …) returns
  // null too: none of those is a row in VS Code's own table either, so the
  // browser/OS keeps whatever default behavior it already had for it.
  if (!noModifiersHeld(stroke)) return null;

  switch (key) {
    case "ArrowUp":
      return { kind: "move", to: "up" };
    case "ArrowDown":
      return { kind: "move", to: "down" };
    case "Home":
      return { kind: "move", to: "first" };
    case "End":
      return { kind: "move", to: "last" };
    case "PageUp":
      return { kind: "move", to: "pageUp" };
    case "PageDown":
      return { kind: "move", to: "pageDown" };
    case "ArrowLeft":
      return { kind: "collapseOrParent" };
    case "ArrowRight":
      return { kind: "expandOrFirstChild" };
    case " ":
      return { kind: "toggle" };
    case "F2":
      return { kind: "rename" }; // every platform
    case "Enter":
      // The one intent that differs by platform — see the file header's "THE
      // PLATFORM SPLIT" paragraph.
      return isMac ? { kind: "rename" } : { kind: "open" };
    case "Delete":
      // A bare Delete key is macOS's one deliberately UNCLAIMED row: VS Code's
      // keybinding rule REPLACES the Windows/Linux primary (`KeyCode.Delete`)
      // with `cmd+Backspace` for mac rather than keeping both active, and most
      // Mac keyboards have no dedicated forward-delete key that produces a bare
      // "Delete" without holding Fn anyway — so macOS's only delete gesture is
      // the cmd+Backspace case handled above, and a bare Delete here is null.
      return isMac ? null : { kind: "delete" };
    default:
      // Includes every plain letter — typeahead is T3 and must not be silently
      // pre-empted here — and a bare "Backspace" on EVERY platform, which is the
      // one place this table had to decide something the plan left implicit
      // ("Delete" (and "Backspace"? decide and justify)). Decided: no. VS Code's
      // own Windows/Linux binding is `Delete` alone with no Backspace
      // alternative; the Backspace FORM only exists at all on macOS, and even
      // there only combined with cmd (handled above the modifier gate). Binding
      // a bare Backspace to delete a tree row would be a genuinely new gesture
      // this table has no VS Code precedent for, and Backspace already carries
      // meaning it would collide with elsewhere (browser back-navigation in some
      // UAs; "delete the previous character" in any text-input context) — a
      // surprising rebind "VS Code familiarity over optimality" gives no reason
      // to introduce.
      return null;
  }
}
