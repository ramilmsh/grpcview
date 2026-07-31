// Intent -> action DISPATCH, extracted out of Tree.tsx's handleKeyDown, per the
// plan's own forwarding note written at T1 ("What T1 settled": "For T2: the
// intent → action dispatch is a switch inside Tree.tsx's handleKeyDown ... T2
// has to edit that same switch (shift+arrow, cmd+A, Escape); extract it into a
// pure applyIntent(intent, ctx) → actions then, rather than now and again
// immediately after." — docs/design/tree-rewrite-plan.md). This is that
// extraction, done AS this phase rather than deferred again, carrying every T1
// intent over with UNCHANGED behavior (see each case's own comment for the
// specific citation proving that), plus this phase's two brand-new intents
// ("extend", "selectAll") and "clearSelection", and the one T1 behavior this
// phase is explicitly asked to change ("delete", now selection-aware).
//
// Pure and DOM-free, like every sibling module's table (keymap.ts's own
// header: "one table, no DOM"; navigate.ts: "pure, DOM-free"; selection.ts:
// "Pure and DOM-free"). applyIntent takes a plain ctx object built from
// component STATE (flat, focused, selection, anchor, rowsPerPage) — never a
// React event, a ref, or a callback — and returns a plain array of TreeAction,
// every one keyed by row id alone. That plainness is what makes this file
// trivially unit-testable (dispatch.test.ts) with no component, no render, no
// DOM — the exact gap the plan's T1 note called out: keyToIntent, the
// navigate.ts lookups, and isEditableTarget were each already covered, but the
// glue turning an intent into real focus/selection/expansion changes was not,
// because it used to live entirely inside a function that closed over
// component state and had nowhere to run in a DOM-free suite.
//
// Tree.tsx becomes the thin INTERPRETER the plan's note asks for: it builds
// the KeyStroke from the real event, resolves the intent via keyToIntent,
// measures rowsPerPage (still via findScrollport — that stays a Tree.tsx
// concern, since it is a DOM measurement no pure module here could perform),
// calls applyIntent, and then walks the returned actions performing each one —
// looking a real node up from flat.indexById only at the two points that need
// one (the "open" and "delete" actions' callbacks take a `T` node, not an id).
//
// TreeAction lives HERE, not in ./types, deliberately: types.ts is "the
// contract" (its own header comment) — the public TreeAdapter/TreeProps
// surface an adapter author or a caller of <Tree> needs to know about.
// TreeAction is neither: it is a private wiring detail between this file and
// Tree.tsx, invisible to both an adapter author and a <Tree> caller, so it
// belongs beside the one function that produces it and the one function
// (Tree.tsx's handleKeyDown) that consumes it — not in the module the plan
// calls out as the durable public contract.
//
// MOUSE, added this same phase (T2's own brief: "the parts that need the DOM
// and the host — mouse modifiers"): applyRowClick, at the bottom of this file,
// is the SECOND pure decision surface alongside applyIntent above, producing
// the identical TreeAction currency from a clicked row + its modifier keys
// instead of from a keyboard intent. It lives here rather than in a new
// sibling file for the same reason dispatch.ts itself exists at all — one
// file that owns "how does an interaction decide what happens to
// focus/selection/anchor/expansion", trivially unit-testable, with Tree.tsx
// staying the thin interpreter for BOTH input modalities. See applyRowClick's
// own header comment, near the bottom of this file, for the full reasoning.

import type { FlatTree } from "./flatten";
import type { TreeIntent } from "./keymap";
import type { TreeRowModel } from "./types";
import { targetIndex, parentIndex, firstChildIndex } from "./navigate";
import { rangeSelection, replaceSelection, selectAll, toggleSelection } from "./selection";

// What an intent RESOLVES to, once it has real rows/focus/selection/anchor to
// act against — plain data, never a node/callback/closure ("Actions are PLAIN
// DATA keyed by row id — no nodes, no callbacks, no closures", this phase's
// own brief), which is what makes these trivially assertable in
// dispatch.test.ts with a plain `toEqual(...)` and no component, no DOM, no
// render. Every id below is a row id (adapter.getId's return value), the same
// currency flat.indexById already keys on — never a `T` node — so this type
// doesn't need a generic parameter of its own (contrast applyIntent below,
// which DOES need one, purely to type `ctx.flat: FlatTree<T>`).
export type TreeAction =
  // Move the roving cursor to `id`. Tree.tsx's existing `focusRow` helper is
  // what actually applies this (setFocused + scrollIntoView) — dispatch
  // itself has no notion of scrolling, or of the DOM at all; see this file's
  // header.
  | { kind: "focus"; id: string }
  // Expand or collapse exactly `id`, to exactly `expanded` — a DESIRED final
  // state, not a "toggle from whatever it currently is" instruction, so the
  // interpreter never has to reason about what `id` was expanded to a moment
  // ago. Contrast Tree.tsx's own pre-existing `toggleExpanded(id,
  // currentlyExpanded)` helper (kept, and still used by handleTwistieClick,
  // which stays OUT of this phase's applyRowClick per its own comment at the
  // bottom of this file — the twistie never emits a TreeAction at all): two
  // different shapes for two different callers, on purpose, not one
  // interface awkwardly serving both. handleRowClick, as of this phase, is
  // the SAME shape applyIntent's "open"/toggle-a-folder fork already uses
  // below (`expanded: !focusedRow.expanded`) — see applyRowClick's own
  // plain-click branch for why reusing this action, not toggleExpanded, is
  // what let handleRowClick fold into the same applied-by-Tree.tsx currency
  // as the keyboard path.
  | { kind: "setExpanded"; id: string; expanded: boolean }
  | { kind: "setSelection"; ids: readonly string[] }
  // `id: null` clears the anchor. selectAll and clearSelection both do this —
  // see their citations below (listWidget.js's onCtrlA/onEscape both call
  // `this.list.setAnchor(undefined)`).
  | { kind: "setAnchor"; id: string | null }
  // A real ACTIVATION — Enter/cmd+↓ on a LEAF row. Never emitted for an
  // expandable row; see the "open" case below for why (VS Code's own Enter on
  // a folder toggles, it doesn't "open" one).
  | { kind: "open"; id: string }
  // Names which row should enter rename mode — feeds Tree.tsx's
  // onRenamingChange bridge prop (types.ts). Takes only an id, never a node:
  // onRenamingChange's own signature is `(id: string | null) => void`.
  | { kind: "requestRename"; id: string }
  | { kind: "delete"; ids: readonly string[] };

// The shape applyIntent's second argument takes — named (rather than left as
// the brief's inline object literal) because dispatch.test.ts builds one of
// these per test case, dozens of times over, and Tree.tsx's own interpreter
// needs to assemble one every keystroke; a name is worth it the moment more
// than one call site has to spell out the same five fields. Every field here
// is either a primitive, a readonly array of primitives, or FlatTree<T> itself
// (already-computed data Tree.tsx has on hand from useTreeState/its own
// rowsPerPage measurement) — never a resolved TreeRowModel, a callback, or
// anything DOM-shaped, matching the "plain data" discipline TreeAction is
// held to above.
export interface ApplyIntentCtx<T> {
  flat: FlatTree<T>;
  focused: string | null;
  selection: readonly string[];
  anchor: string | null;
  rowsPerPage: number;
}

// Resolves a possibly-stale id (a focused id whose row may have been hidden by
// a collapse, or filtered out from under it, since it was recorded in state)
// back to its row, or null if there is none. DUPLICATES Tree.tsx's own
// identically-shaped inline computation of `focusedRow` (see that file's
// render body) rather than importing across the boundary: ApplyIntentCtx
// above is deliberately PLAIN DATA (`focused: string | null`, not a
// pre-resolved TreeRowModel), matching the same discipline TreeAction itself
// is held to, so this lookup has to happen independently on each side of the
// boundary. Two lines, kept in sync by hand; not worth inventing a shared
// export across two files that otherwise have no reason to import one
// another.
function rowFor<T>(flat: FlatTree<T>, id: string | null): TreeRowModel<T> | null {
  if (id === null) return null;
  return flat.rows[flat.indexById.get(id) ?? -1] ?? null;
}

// Which rows Delete/cmd+Backspace acts on, now that this phase makes it
// selection-aware (T1: always exactly the focused row — Tree.tsx's original
// "delete" case read `if (focusedRow) onDelete?.([focusedRow.node]);`, an
// unconditional single-row call with no notion of `selection` at all). The
// three-way rule below is the standard "list command context" resolution real
// VS Code uses for multi-select commands (e.g. the Explorer's own
// `getContext(respectMultiSelection)`, in workbench-level
// `explorerService.ts`) — but that file is NOT vendored in this repo, only
// the base list/tree widgets under ui/node_modules/monaco-editor are, and
// workbench-level commands live entirely outside this bundle. Stated plainly,
// per this repo's own established precedent for exactly this situation
// (keymap.ts's shift-arrow trace; platform.ts's Home/End note): this is
// familiarity with the app's actual behavior, not a citation this repo can
// back with a file:line the way onEscape/onCtrlA below are backed.
//
// The rule:
//   - no focused row at all -> nothing to act on ([]). Mirrors T1 exactly:
//     the `if (focusedRow)` gate there is the same "no focus, no delete" idea,
//     just moved from an `if` around a callback to an early return here.
//   - a focused row that IS part of the current selection -> the WHOLE
//     selection. This is the actual multi-select case this phase exists to
//     add.
//   - a focused row that is NOT part of the current selection -> ONLY the
//     focused row, discarding whatever selection happens to be lying around.
//     "Selection is empty" is the degenerate case of this same branch
//     (`[].includes(x)` is always false), which is exactly why plain,
//     single-row Delete keeps working unchanged from T1: focus set, selection
//     empty -> that one row, nothing more. The genuinely NEW case this branch
//     handles is a nonempty but STALE selection: a plain (non-shift) arrow
//     move never touches selection at all (see the "move" case below,
//     listWidget.js:281-298) — so a user who ctrl+clicks three rows, then
//     arrows away with a bare ArrowDown, ends up with focus OUTSIDE the
//     three-row selection the mouse built, while that selection is still
//     sitting in state untouched. Deleting in that state should act on the
//     row the user is actually looking at right now, not silently discard
//     three rows they have, in effect, already navigated away from.
function resolveDeleteIds(focused: string | null, selection: readonly string[]): string[] {
  if (focused === null) return [];
  return selection.includes(focused) ? [...selection] : [focused];
}

export function applyIntent<T>(intent: TreeIntent, ctx: ApplyIntentCtx<T>): TreeAction[] {
  const { flat, focused, selection, anchor, rowsPerPage } = ctx;
  // Resolved ONCE, reused by every case below that needs a real row rather
  // than a raw id — exactly the six T1 cases that already worked this way
  // (collapseOrParent/expandOrFirstChild/toggle/open/rename/delete). "move"
  // and "extend" deliberately do NOT use this: they hand the raw `focused`
  // string straight to targetIndex, which already normalizes a null-or-stale
  // id itself (navigate.ts's own `fromIndex = ... ?? -1`, proven by
  // navigate.test.ts's "an unknown fromId ... is treated exactly like null").
  // Duplicating that normalization here would be redundant, not more correct.
  const focusedRow = rowFor(flat, focused);

  switch (intent.kind) {
    case "move": {
      const idx = targetIndex(flat, focused, intent.to, rowsPerPage);
      if (idx === null) return []; // empty tree — nothing to focus
      const id = flat.rows[idx].id;

      // PRESERVED from T1: a plain move is the roving CURSOR only, never
      // selection. Confirmed against the real widget: listWidget.js:281-298's
      // onUpArrow/onDownArrow (and :299-316's onPageUpArrow/onPageDownArrow)
      // call `this.list.focusNext/focusPrevious(...)` and then
      // `this.list.setAnchor(el)` on the row they just focused — NEVER
      // `this.list.setSelection(...)` anywhere in either pair of methods.
      // "open" below is the one intent allowed to touch selection, because it
      // is the one real activation.
      //
      // ANCHOR: reset to the row focus just landed on, on every plain move —
      // per that SAME citation, each of those four methods' last
      // focus-related call before returning is `this.list.setAnchor(el)`.
      // This is what makes a LATER shift+arrow extend from wherever the user
      // actually is, rather than from whatever old anchor a previous
      // shift-drag left behind. Extended here to "first"/"last" (Home/End)
      // too, even though — like Home/End themselves (platform.ts's own
      // comment; keymap.ts's file header) — that specific pair is
      // workbench-layer in real VS Code and so isn't independently traceable
      // in this vendored bundle: keeping ONE rule for every "move" sub-kind,
      // rather than carving out an unproven exception for two of six, is the
      // simpler and more predictable reading, and nothing in the plan's key
      // table asks Home/End to leave the anchor alone.
      return [
        { kind: "focus", id },
        { kind: "setAnchor", id },
      ];
    }

    case "extend": {
      // shift+ArrowUp/shift+ArrowDown (new this phase). NOT independently
      // traceable in this vendored bundle at all — see keymap.ts's own
      // onlyShiftHeld call site for the full trace proving neither
      // KeyboardController (listWidget.js) nor abstractTree.js's own keydown
      // wiring implements shifted arrows anywhere, the same conclusion this
      // codebase already reached for Home/End. What follows is this file's
      // OWN implementation of the plan's key table row ("extend selection
      // from the anchor"), not a transcription of vendored behavior — the
      // citations below are for the PIECES this reuses (rangeSelection,
      // targetIndex), not for "extend" as a whole.
      const idx = targetIndex(flat, focused, intent.to, rowsPerPage);
      if (idx === null) return []; // empty tree
      const id = flat.rows[idx].id;

      // Bootstrapping the anchor: "If there is no anchor yet, the anchor
      // becomes the row focus is leaving (i.e. the current focused row)
      // before extending" (this phase's brief, verbatim). `anchor ?? focused`
      // implements exactly that on the FIRST shift+arrow of a drag (anchor is
      // still null — from a previous plain move, per "move" above, or a
      // fresh mount — so it falls back to wherever focus currently sits,
      // BEFORE the step below carries focus onto the new row) and is a
      // harmless no-op on every SUBSEQUENT shift+arrow of the same drag
      // (anchor is already the real pivot from the first press, so `??`
      // never reaches `focused` again there). That "no-op on repeats" is what
      // lets a run of shift+Down presses keep GROWING the same range instead
      // of the anchor chasing focus one row at a time, which is the entire
      // point of an anchor existing at all.
      //
      // If BOTH anchor and focused are null (nothing has ever been focused at
      // all — reachable, e.g. the very first keystroke the tree ever
      // receives being shift+Down before any plain arrow press), this
      // resolves to null too; rangeSelection already degrades a null anchor
      // to a single-row selection (its own null-anchor branch, exercised by
      // selection.test.ts), and the very next keystroke picks up a real
      // anchor for free once `focused` is non-null from this press's own
      // "focus" action having been applied.
      const effectiveAnchor = anchor ?? focused;

      return [
        { kind: "focus", id },
        { kind: "setAnchor", id: effectiveAnchor },
        { kind: "setSelection", ids: rangeSelection(flat.rows, effectiveAnchor, id) },
      ];
    }

    case "collapseOrParent": {
      // PRESERVED from T1. Mirrors abstractTree.js:2037-2056's onLeftArrow
      // exactly: `model.setCollapsed(location, true)` first; only when that
      // reports no change (already collapsed, or a leaf with nothing to
      // collapse) does it walk to the parent and refocus there.
      //
      // Neither branch of that method ever calls `setAnchor` — collapsing in
      // place obviously doesn't move focus at all, but note that even the
      // PARENT-refocusing branch (a real focus change) still doesn't touch
      // it, unlike onUpArrow/onDownArrow's unconditional setAnchor(el) in the
      // "move" case above. That asymmetry is the actual source of this
      // phase's "do not touch the anchor from expansion-only intents"
      // instruction: Left/Right/Space are wired through a COMPLETELY
      // SEPARATE code path from the base widget's own arrow keys — confirmed
      // by listWidget.js:251-268 (KeyboardController's own keydown switch),
      // which has no LeftArrow/RightArrow case at all; those two keys are
      // claimed entirely at the TREE layer instead (abstractTree.js:1816-1818
      // wires exactly Left/Right/Space, filtered through the same
      // isInputElement guard). That separate path simply never calls
      // setAnchor, on either branch, so neither does this case.
      if (!focusedRow) return [];
      if (focusedRow.expandable && focusedRow.expanded) {
        return [{ kind: "setExpanded", id: focusedRow.id, expanded: false }];
      }
      const idx = parentIndex(flat, focusedRow.id);
      if (idx === null) return []; // a root, or already not a visible row
      return [{ kind: "focus", id: flat.rows[idx].id }];
    }

    case "expandOrFirstChild": {
      // PRESERVED from T1. Mirrors abstractTree.js:2057-2076's onRightArrow —
      // same structure, and the same "no setAnchor anywhere in this method"
      // property, as collapseOrParent's citation above.
      if (!focusedRow) return [];
      if (focusedRow.expandable && !focusedRow.expanded) {
        return [{ kind: "setExpanded", id: focusedRow.id, expanded: true }];
      }
      const idx = firstChildIndex(flat, focusedRow.id);
      if (idx === null) return [];
      return [{ kind: "focus", id: flat.rows[idx].id }];
    }

    case "toggle":
      // PRESERVED from T1: Space toggles expansion only, never selection or
      // anchor, and is a no-op on a leaf (nothing to toggle) or with nothing
      // focused (focusedRow?.expandable is undefined, and !undefined is
      // true, so this returns [] exactly like the explicit `!focusedRow`
      // guards elsewhere).
      return focusedRow?.expandable
        ? [{ kind: "setExpanded", id: focusedRow.id, expanded: !focusedRow.expanded }]
        : [];

    case "open": {
      // PRESERVED from T1, INCLUDING its load-bearing "toggle instead of
      // open" fork for an expandable row (Tree.tsx's own long-standing
      // comment on this case, carried over in spirit): VS Code's real Enter
      // on a folder toggles expansion — the same thing Space/a plain click
      // already does to a folder — rather than handing the folder to
      // `onOpen` as if it were an openable leaf, which would hand
      // CollectionPanel's openTab a folder ItemWithPath it has never had to
      // handle before.
      //
      // ANCHOR: deliberately UNTOUCHED here, matching listWidget.js:276-280's
      // onEnter exactly — `this.list.setSelection(this.list.getFocus(),
      // e.browserEvent)` is the ENTIRE method body; it never calls
      // `setAnchor`. Faithful-parity consequence, spelled out rather than
      // silently avoided: if the anchor was already pointing somewhere from
      // an earlier shift-drag, opening a single row here collapses the
      // SELECTION down to just that row but leaves the OLD anchor exactly
      // where it was — so a shift+arrow immediately afterward extends from
      // that stale pivot, not from the row that was just opened, and can
      // resurrect a range that looks like the very selection "open" just
      // replaced. This is the same faithful-to-upstream tradeoff this
      // codebase makes elsewhere on purpose (per the standing rule: copy VS
      // Code's UX exactly where an equivalent exists; deviations must be
      // deliberate and written down) rather than a gap — real VS Code's own
      // onEnter has the identical property, so reproducing it here is the
      // considered choice, not an oversight. Worth revisiting if it proves
      // surprising in the browser verification pass, but not changed
      // speculatively now with no evidence either way.
      if (!focusedRow) return [];
      const actions: TreeAction[] = [{ kind: "setSelection", ids: [focusedRow.id] }];
      if (focusedRow.expandable) {
        actions.push({ kind: "setExpanded", id: focusedRow.id, expanded: !focusedRow.expanded });
      } else {
        actions.push({ kind: "open", id: focusedRow.id });
      }
      return actions;
    }

    case "rename":
      // PRESERVED from T1: always names the focused row; whether it is
      // actually renamable (e.g. CollectionPanel refuses a folder id today,
      // since UpdateFolderRequest has no `name` field until T4a) is entirely
      // the HOST's call, not this module's — dispatch.ts has no more notion
      // of "folder" than Tree.tsx itself ever did.
      return focusedRow ? [{ kind: "requestRename", id: focusedRow.id }] : [];

    case "delete": {
      // T2 CHANGES this from T1 (which was always exactly [focusedRow.node],
      // regardless of `selection` — see resolveDeleteIds's own comment for
      // the full new rule and its honestly-unvendored justification).
      //
      // Passes focusedRow's id — already re-validated against `flat` above —
      // not the raw ctx.focused string, for the same reason every OTHER case
      // in this switch gates on focusedRow rather than ctx.focused directly:
      // a focused id whose row has since been hidden by a collapse or a
      // filter is "nothing focused" as far as every sibling intent is
      // concerned (rename/toggle/collapseOrParent/expandOrFirstChild/open all
      // early-return on `!focusedRow`), and delete should be no different —
      // a stale focus id shouldn't let a delete fire against a selection the
      // user can no longer even see the anchor row of.
      const ids = resolveDeleteIds(focusedRow?.id ?? null, selection);
      if (ids.length === 0) return [];
      return [{ kind: "delete", ids }];
    }

    case "selectAll":
      // cmd/ctrl+A (new this phase). Mirrors listWidget.js:317-323's onCtrlA
      // exactly: `this.list.setSelection(range(this.list.length),
      // e.browserEvent)` selects every element with NO regard for focus at
      // all (no `view.setFocus` call anywhere in the method — focus stays
      // wherever it already was), then `this.list.setAnchor(undefined)`
      // unconditionally clears the anchor. selectAll(flat.rows)
      // (selection.ts) is already exactly "every visible row" including the
      // empty-tree case ([] rows in, [] selection out) — so this always
      // returns the same two actions regardless of tree size, rather than
      // special-casing "nothing to select" into zero actions the way
      // clearSelection does below: setSelection([]) / setAnchor(null) are
      // harmless no-ops when that's already the state, and a uniform action
      // count is simpler to reason about — and to test — than a
      // size-dependent one.
      return [
        { kind: "setSelection", ids: selectAll(flat.rows) },
        { kind: "setAnchor", id: null },
      ];

    case "clearSelection":
      // Escape (new this phase). Mirrors listWidget.js:324-332's onEscape,
      // INCLUDING its guard: that method only acts `if
      // (this.list.getSelection().length)`, leaving an Escape with nothing
      // selected as a complete no-op (it never even calls preventDefault in
      // that case, so the key keeps whatever default behavior the browser
      // already gave it). keyToIntent's own comment already flags this exact
      // conditionality as "the CONSUMER's job" — this is that consumer, and
      // this `if` is where the guard actually gets applied, one layer below
      // keyToIntent's unconditional `{ kind: "clearSelection" }`.
      //
      // Also mirrors onEscape's `this.list.setAnchor(undefined)` — clearing
      // the selection clears the pivot too, so a later shift+arrow starts a
      // genuinely fresh range rather than resuming one Escape was just used
      // to abandon.
      //
      // No focus action: onEscape's own `this.view.domNode.focus()` call is a
      // real-DOM-focus nicety with nothing to mirror here — this component's
      // logical focus (aria-activedescendant) was never tied to raw DOM
      // focus leaving the container to begin with (Tree.tsx's own FOCUS
      // MODEL comment: "DOM focus never leaves the container").
      if (selection.length === 0) return [];
      return [
        { kind: "setSelection", ids: [] },
        { kind: "setAnchor", id: null },
      ];
  }
}

// ── mouse clicks (T2) ───────────────────────────────────────────────────────
// The plan's "Mouse" list (docs/design/tree-rewrite-plan.md, §"VS Code UX
// spec"): plain click, cmd/ctrl+click, shift+click. This is the second half
// of this phase's own brief ("the parts that need the DOM and the host —
// mouse modifiers, and multi-aware delete"), and it lives HERE rather than in
// a new `mouse.ts` sibling for the same reason applyIntent above already
// earns its place in this file: same TreeAction currency, same one caller
// (Tree.tsx), same "extract the DECISION so it's testable without a
// component/DOM" motive. Splitting keyboard and mouse into two files would
// immediately need them importing back and forth anyway — applyRowClick's
// shift-click branch reuses the identical `anchor ?? focused` bootstrap
// rangeSelection-based reasoning "extend" above already carries at length; see
// that case's comment for the long version rather than repeating it here. The
// plan's own module table (docs/design/tree-rewrite-plan.md) lists exactly
// ten files under components/tree/ and does not include dispatch.ts either —
// this file was already the one precedent-setting exception (added mid-T2 for
// exactly this testability reason, per the plan's own "What T1 settled"
// forwarding note); growing ITS scope to cover mouse decisions too follows
// that precedent rather than setting a second one.
//
// Traced directly against the vendored base list widget's own mouse
// controller, `ui/node_modules/monaco-editor/esm/vs/base/browser/ui/list/
// listWidget.js`'s `MouseController` class (roughly lines 533-669) — NOT
// abstractTree.js's `TreeNodeListMouseController` override (~1560-1621),
// which layers twistie/expand-on-click handling on TOP of this and is
// deliberately NOT mirrored here; see the note at the bottom of this section
// for why the twistie itself stays out of applyRowClick entirely.

// Which modifier(s) were held for a given click, ALREADY resolved to the
// platform-correct chord by the caller (Tree.tsx) — this module takes a plain
// boolean, exactly like keymap.ts's keyToIntent takes `isMac` as data instead
// of resolving IS_MAC itself (platform.ts's own header explains why: it's
// what keeps a table exercisable as pure data-in-data-out from a single test
// process, both platforms in one file, with no navigator/process sniffing of
// its own — dispatch.test.ts's applyRowClick cases do the same thing keymap.
// test.ts already does, constructing `modKey: true` directly rather than
// mocking a platform). `modKey` names cmd on macOS or ctrl elsewhere —
// listWidget.js:520-521's `isSelectionSingleChangeEvent`: `platform.
// isMacintosh ? e.metaKey : e.ctrlKey` — the SAME platform ternary keymap.
// ts's own cmd/ctrl+A resolution already mirrors (that file's onlyCtrlHeld/
// onlyMetaHeld comments); Tree.tsx's handleRowClick is what actually
// evaluates that ternary against a real MouseEvent and passes the result in
// here as a plain boolean.
export interface ClickMods {
  shiftKey: boolean;
  modKey: boolean;
}

// Every field applyIntent's ctx carries EXCEPT rowsPerPage — a click never
// pages, it names its target row directly, so there is no stride to measure.
// Spelled as an Omit of the existing type rather than a hand-copied second
// interface that could drift from it as ApplyIntentCtx's own fields change.
export type ApplyClickCtx<T> = Omit<ApplyIntentCtx<T>, "rowsPerPage">;

// row/mods -> actions. Precedence — shiftKey checked BEFORE modKey — mirrors
// listWidget.js's own changeSelection (~632-665): `isSelectionRangeChangeEvent`
// (shift) is tested FIRST (:635), `else if isSelectionSingleChangeEvent`
// (cmd/ctrl) SECOND (:653) — so a shift+cmd+click (both held at once) takes
// the RANGE branch; cmd is simply irrelevant once shift is down. This is also
// a LOOSER match than keymap.ts's own onlyShiftHeld/onlyMetaHeld/onlyCtrlHeld
// (which reject e.g. cmd+shift+A outright, requiring an EXACT modifier set
// with nothing else riding along): listWidget.js:520-524's own
// isSelectionSingleChangeEvent/isSelectionRangeChangeEvent are two
// independent raw boolean reads with no exclusivity check of their own — the
// mouse controller never asks "is anything ELSE also held", only "is shift
// down" then "is cmd/ctrl down" — so this function mirrors THAT looser shape
// deliberately, rather than importing the keyboard table's exact-match
// discipline onto a control surface that was never built with it.
export function applyRowClick<T>(
  row: TreeRowModel<T>,
  mods: ClickMods,
  ctx: ApplyClickCtx<T>
): TreeAction[] {
  const { flat, focused, selection, anchor } = ctx;

  if (mods.shiftKey) {
    // shift+click — extend from the anchor (rangeSelection, selection.ts;
    // the SAME function the keyboard "extend" case above already uses, so
    // shift+arrow and shift+click agree by construction on what "range" means
    // — there is exactly one implementation of it in this whole component).
    // FOCUS moves to the clicked row; the ANCHOR does NOT — unless it was
    // never set, in which case it bootstraps. listWidget.js:632-651's
    // changeSelection only calls `this.list.setAnchor(anchor)` INSIDE the
    // `typeof anchor === 'undefined'` bootstrap branch (:636-639); the
    // `this.list.setFocus([focus], e.browserEvent)` that ends the method
    // (:651) sits OUTSIDE that `if`, run unconditionally. `anchor ?? focused`
    // reproduces the identical bootstrap rule the keyboard "extend" case
    // above already documents at length (this file, same describe-by-example
    // reasoning): an unset anchor falls back to wherever focus is LEAVING
    // FROM — listWidget.js:637-638's `currentFocus = this.list.getFocus()[0]`
    // is read BEFORE this click's own setFocus call lands — never the row
    // just clicked. The "anchor and focus both null" degenerate case is
    // identical too: rangeSelection already turns a null anchor into a
    // single-row selection (selection.test.ts), and the very next click or
    // keystroke picks up a real anchor for free once `focused` is non-null
    // from THIS click's own "focus" action having been applied.
    const effectiveAnchor = anchor ?? focused;
    return [
      { kind: "focus", id: row.id },
      { kind: "setAnchor", id: effectiveAnchor },
      { kind: "setSelection", ids: rangeSelection(flat.rows, effectiveAnchor, row.id) },
    ];
  }

  if (mods.modKey) {
    // cmd/ctrl+click — toggle membership (toggleSelection, selection.ts).
    // BOTH focus and anchor move to the clicked row, unconditionally —
    // listWidget.js:656-657 (`this.list.setFocus([focus]);
    // this.list.setAnchor(focus);`, no bootstrap-only special case the way
    // shift's branch above has one). Never opens, never touches expansion —
    // a modified click is PURE selection, per this phase's own brief
    // ("WITHOUT opening it and without collapsing/expanding a folder").
    return [
      { kind: "focus", id: row.id },
      { kind: "setAnchor", id: row.id },
      { kind: "setSelection", ids: toggleSelection(selection, row.id) },
    ];
  }

  // Plain click (T0 behavior, re-expressed as TreeAction[] this phase — see
  // this file's header for why unifying it into the SAME currency as the two
  // modified branches above, rather than leaving it as Tree.tsx's own
  // pre-existing imperative setSelection/setFocused/toggleExpanded calls, is
  // worth doing now). Replaces the selection, focuses, and — NEW this phase —
  // sets the anchor, all landing on the clicked row regardless of leaf/folder:
  // listWidget.js:611-614's onViewPointer does exactly these three
  // (`setFocus`/`setAnchor` unconditionally, `setSelection` unless it was a
  // right click — right-click's own path is Tree.tsx's separate
  // handleContextMenu, untouched by this phase) for EVERY plain click; the
  // base list widget has no notion of "folder" at all, so the expand-vs-open
  // fork below is a TREE-layer concept layered on top by the caller — mirrors
  // abstractTree.js's own Left/Right/twistie handling living outside the base
  // widget too (this file's collapseOrParent/expandOrFirstChild cases above
  // make the identical point for the keyboard side). The `setExpanded`
  // action/`!row.expanded` shape reuses exactly what applyIntent's "open"
  // case above already does for Enter-on-a-folder, not Tree.tsx's older
  // `toggleExpanded(id, currentlyExpanded)` helper (which handleTwistieClick
  // keeps using directly, per this file's TreeAction.setExpanded comment).
  const actions: TreeAction[] = [
    { kind: "setSelection", ids: replaceSelection(row.id) },
    { kind: "focus", id: row.id },
    { kind: "setAnchor", id: row.id },
  ];
  if (row.expandable) {
    actions.push({ kind: "setExpanded", id: row.id, expanded: !row.expanded });
  } else {
    actions.push({ kind: "open", id: row.id });
  }
  return actions;
}

// NOT covered here: a click on the TWISTIE itself. Tree.tsx's
// handleTwistieClick stays exactly as T0 left it — stopPropagation, then the
// existing toggleExpanded(id, currentlyExpanded) helper, unconditionally,
// ignoring modifiers entirely — rather than routing through applyRowClick
// with some third "onTwistie" mode. This was a deliberate call, not an
// oversight: abstractTree.js's OWN tree-layer mouse controller
// (TreeNodeListMouseController, ~1566-1621) shows that real VS Code's twistie
// click is MORE elaborate than "always toggle" — line 1579 (`if (this.
// isSelectionRangeChangeEvent(e) || this.isSelectionSingleChangeEvent(e))
// return super.onViewPointer(e);`) means a shift+click or cmd/ctrl+click
// LANDING ON THE TWISTIE skips the toggle branch ENTIRELY in real VS Code and
// is instead treated as a pure selection operation exactly like clicking
// anywhere else on the row — the opposite of "the twistie always just
// toggles". This phase's own brief states the invariant to preserve as "The
// twistie must keep toggling without changing selection" with no modifier
// carve-out, and the plan's own "Mouse" list has exactly one, unconditional
// twistie row ("click the twistie → toggle without changing selection") with
// no modified variant listed at all — so the simpler, already-specified
// behavior is what ships: every twistie click toggles, full stop, and a
// modified click never reaches it any differently than an unmodified one.
// Recorded here as a found-but-deliberately-not-replicated citation, not a
// silent gap, in case a future phase wants VS Code's fuller nuance.
