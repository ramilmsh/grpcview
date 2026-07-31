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
//   - shift+ArrowUp/shift+ArrowDown extend the selection from the anchor — T2
//     (multi-select), landed 2026-07-31. Resolved before the blanket modifier
//     gate below, same reason as the mac-only pair above: shift is a real,
//     exact modifier the gate would otherwise reject outright. See
//     onlyShiftHeld's call site for a finding this forced: this exact
//     vendored bundle does not itself implement shift+Arrow-extends either,
//     in much the same way Home/End turned out to be workbench-layer rather
//     than widget-layer (see platform.ts's neighbor comment) — full trace
//     through listWidget.js/abstractTree.js lives there, not repeated here.
//   - cmd+A (macOS) / ctrl+A (everywhere else) selects all VISIBLE rows — T2.
//     PLATFORM-CONDITIONAL like cmd+ArrowDown/Delete above, but unlike either
//     of those it is live on BOTH platforms rather than mac-only-vs-unclaimed
//     — just against a different exact modifier per platform, mirroring
//     listWidget.js:264's own gate (`platform.isMacintosh ? e.metaKey :
//     e.ctrlKey`). Reuses onlyMetaHeld on macOS and a new mirror-image
//     onlyCtrlHeld elsewhere; see the call site for resolution order
//     relative to the mac-only pair.
//   - Escape clears the selection — T2, every platform, no modifier. Its own
//     `case` below says what this table deliberately does NOT make Escape do
//     here (cancel rename; cancel typeahead) and why.

export interface KeyStroke {
  key: string;
  shiftKey: boolean;
  metaKey: boolean;
  ctrlKey: boolean;
  altKey: boolean;
}

export type TreeIntent =
  | { kind: "move"; to: "up" | "down" | "first" | "last" | "pageUp" | "pageDown" }
  // shift+ArrowUp/shift+ArrowDown (T2). Deliberately its OWN variant rather
  // than a `shiftKey` flag riding on "move": a plain move changes focus only
  // (decision 4, focus ≠ selection), while this one ALSO grows the selection
  // from the anchor — selection.ts's rangeSelection (already written at T0,
  // see that file's header) against useTreeState.ts's `anchor` state (whose
  // own comment already says "wired at T2"). Narrower than "move"'s `to` on
  // purpose: only up/down, because only the bare arrows are resolved in
  // shifted form below — see the "T2 non-goal" reasoning at this file's
  // onlyShiftHeld call site for why pageUp/pageDown/first/last have no
  // shifted counterpart here.
  | { kind: "extend"; to: "up" | "down" }
  | { kind: "collapseOrParent" } // Left
  | { kind: "expandOrFirstChild" } // Right
  | { kind: "toggle" } // Space
  | { kind: "open" }
  | { kind: "rename" }
  | { kind: "delete" }
  | { kind: "selectAll" } // cmd/ctrl+A (T2) — every visible row; selection.ts's selectAll
  | { kind: "clearSelection" }; // Escape (T2)

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

// Exactly ctrl, no other modifier — the Windows/Linux mirror of onlyMetaHeld
// above. Needed because cmd/ctrl+A (T2's select-all) is live on EVERY
// platform, unlike the two mac-only bindings onlyMetaHeld guards: it's the
// same "exact modifier" shape, just gated on a different physical key per
// platform. See keyToIntent's select-all resolution, and listWidget.js:264
// (`platform.isMacintosh ? e.metaKey : e.ctrlKey`), which this function and
// onlyMetaHeld together exist to mirror — one branch each.
function onlyCtrlHeld(stroke: KeyStroke): boolean {
  return stroke.ctrlKey && !stroke.shiftKey && !stroke.metaKey && !stroke.altKey;
}

// Exactly shift, no other modifier — the shape shift+ArrowUp/shift+ArrowDown
// (T2's "extend" intent) needs to clear the blanket modifier gate below, same
// reasoning as onlyMetaHeld/onlyCtrlHeld: shift+ctrl+ArrowDown or
// shift+alt+ArrowUp must fall through to null, not be silently accepted as
// extending the selection.
function onlyShiftHeld(stroke: KeyStroke): boolean {
  return stroke.shiftKey && !stroke.metaKey && !stroke.ctrlKey && !stroke.altKey;
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

  // cmd+A (macOS) / ctrl+A (everywhere else) — select all VISIBLE rows (T2).
  // Resolved here, before the blanket gate, for the same reason as the
  // mac-only pair above: it rides a real, exact modifier the gate would
  // otherwise reject outright. Unlike that pair, though, it is NOT mac-only —
  // it's live on every platform, just against a different exact chord per
  // platform — so it can't fold into the `isMac && onlyMetaHeld(stroke)` `if`
  // above; it needs its own check, against whichever modifier THIS platform's
  // real list widget actually gates it on: listWidget.js:264 — `case
  // KeyCode.KeyA: if (this.multipleSelectionSupport && (platform.isMacintosh
  // ? e.metaKey : e.ctrlKey)) this.onCtrlA(e);`.
  //
  // Both `key === "a"` and `key === "A"` are accepted, not just the lowercase
  // form an unshifted letter normally produces: shiftKey is already excluded
  // by onlyMetaHeld/onlyCtrlHeld below, so the only remaining way `.key`
  // reports the uppercase form here is caps lock — a real, reachable browser
  // state, and one this table shouldn't treat as "unclaimed" merely because
  // it changes which case the character arrives in.
  if (key === "a" || key === "A") {
    if (isMac ? onlyMetaHeld(stroke) : onlyCtrlHeld(stroke)) return { kind: "selectAll" };
    // ctrl+shift+A, cmd+alt+A, and the OTHER platform's modifier (ctrl+A on
    // mac, cmd+A off mac) all fall through to the blanket gate below and
    // return null — exact match, not "at least": an extra modifier riding
    // along, or the wrong platform's modifier entirely, is a DIFFERENT chord
    // from the one this table claims, same reasoning as onlyMetaHeld's own
    // comment.
  }

  // shift+ArrowUp / shift+ArrowDown — extend the selection from the anchor
  // (T2). Resolved here, before the gate, for the same reason as cmd/ctrl+A
  // just above: shift is a real, exact modifier the gate would otherwise
  // reject. This table claims ONLY the bare arrows in shifted form — a
  // deliberately narrower scope than "every navigation key, shifted" might
  // suggest, and worth tracing through the actual vendored source rather than
  // assuming shift+PageUp/PageDown/Home/End are the same kind of row, merely
  // left for later:
  //
  // The vendored KeyboardController (listWidget.js:239-337) is the ONLY
  // keydown-driven selection logic the base list widget has (confirmed:
  // TypeNavigationController is typeahead, T3's concern; DOMFocusController
  // is Tab-key focus-trapping — neither touches selection). Its
  // onUpArrow/onDownArrow/onPageUpArrow/onPageDownArrow (lines 281-316) never
  // read `e.browserEvent.shiftKey` at all — each one unconditionally calls
  // `this.list.setAnchor(el)` on the row it just focused, which is the
  // OPPOSITE of preserving an old anchor to extend a range from. The only
  // place this file reads `shiftKey` for a selection decision at all is
  // `isSelectionRangeChangeEvent` (listWidget.js:523-524), and that is wired
  // ONLY into `MouseController` (constructed at line 1166) for shift-CLICK —
  // never into `KeyboardController`. abstractTree.js doesn't layer anything
  // extra on top either: its own keydown wiring (abstractTree.js:1814-1818)
  // filters the stream down to Left/Right/Space only.
  //
  // So, precisely: shift+ArrowUp/Down extending a selection has NO traceable
  // implementation anywhere in this vendored bundle — and neither does
  // shift+PageUp/PageDown/Home/End; all five are in the same boat. The most
  // likely explanation, consistent with this file's own established
  // precedent for Home/End (see the file header and platform.ts's neighbor
  // comment), is that real VS Code wires ALL of these at the WORKBENCH
  // keybinding layer (`vs/platform/list/browser/listService.ts`, presumably
  // commands named like `list.expandSelectionUp`/`list.expandSelectionDown`)
  // rather than inside this reusable base widget — but that file is not
  // vendored here, so this is inference from absence, offered as exactly
  // that rather than a citation this repo can actually back.
  //
  // Given that, "implement bare shift+Up/Down, leave shift+PageUp/PageDown/
  // Home/End unclaimed" is a PRODUCT decision for T2 (it is what the plan's
  // key table asks for — "shift+↑ shift+↓ | extend selection from the
  // anchor", nothing about the other four) rather than a decision the
  // vendored bundle hands us either way. The four unclaimed combinations are
  // therefore a deliberate T2 NON-goal, not an oversight: nothing here claims
  // them because nothing in the plan's table asks for them, and — per the
  // paragraph above — there would be no VS Code widget-layer behavior to be
  // faithful to even if it did.
  if (onlyShiftHeld(stroke)) {
    if (key === "ArrowUp") return { kind: "extend", to: "up" };
    if (key === "ArrowDown") return { kind: "extend", to: "down" };
    // shift+Home, shift+End, shift+PageUp, shift+PageDown, shift+F2, shift+
    // (any letter), … all fall through to the blanket gate below and return
    // null — see the long comment just above for why the four navigation
    // ones specifically are a deliberate non-goal rather than a gap.
  }

  // Every remaining row is a BARE key: no shift, no meta/cmd, no ctrl, no alt
  // — with the two T2 exceptions just above already resolved (cmd/ctrl+A,
  // then shift+ArrowUp/Down), both of which ride a real, exact modifier this
  // gate would otherwise reject wholesale. Every OTHER modified combination
  // this table doesn't mention (alt+ArrowUp, ctrl+Home, cmd+F2, shift+Space,
  // shift+Home, shift+PageUp, shift+PageDown, …) still returns null here:
  // none of those is a row in VS Code's own table either — or, for the
  // shift+Home/PageUp/PageDown trio specifically, isn't even a row this
  // table's SOURCE (the vendored widget) can be shown to claim in shifted
  // form at all; see onlyShiftHeld's call site above for that finding — so
  // the browser/OS keeps whatever default behavior it already had for it.
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
    case "Escape":
      // Clears the selection (T2) — every platform, no modifier: Escape
      // rides no cmd/ctrl/shift/alt combination in VS Code, so a MODIFIED
      // Escape isn't a row this table (or VS Code's) claims — it's correctly
      // rejected by the blanket gate above like any other modified key, same
      // as every other bare-key row in this switch. Per the plan's key
      // table, Escape also cancels typeahead and cancels rename — this
      // intent deliberately covers NEITHER of those:
      //   - rename-cancel needs no intent at all: the tree's own rename box
      //     handles its own Escape keydown (components/tree/RenameInput.tsx,
      //     T4b — through T0/T1 this was EditableName's box), and T1's
      //     isEditableTarget guard — Tree.tsx's handleKeyDown bails out on it
      //     BEFORE building any intent, see this file's own header and the
      //     hard-constraint this phase was told to preserve — means a
      //     keystroke inside that input never reaches keyToIntent at all.
      //     There is nothing left for a TreeIntent to do here.
      //   - typeahead-cancel is T3, not built yet — the same "not this
      //     table's job until the feature exists" reasoning T1 used for
      //     shift+Arrow before T2 existed (see this file's history). When T3
      //     lands it will need its own intent, or a deliberate decision to
      //     fold into this one — not a silent overload of "clearSelection"
      //     to mean two unrelated things.
      //
      // Unconditional, like every other row in this table: keyToIntent has
      // no notion of "is anything currently selected" (no DOM, no tree
      // state at all — see the file header), so it always emits this intent
      // for a bare Escape. listWidget.js's own onEscape (lines 324-332) only
      // actually clears when `this.list.getSelection().length` is nonzero,
      // leaving the key untouched (no preventDefault) otherwise — replicating
      // that no-op-when-empty conditionality is the CONSUMER's job (a later
      // phase's dispatch, once it has real selection state to check), the
      // same way "is the focused row even renamable" is a consumer-side
      // decision even though F2 unconditionally produces a rename intent
      // here regardless of what row is focused. (As of T4a/T4b that consumer
      // no longer refuses anything: folders rename too.)
      return { kind: "clearSelection" };
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
