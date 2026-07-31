import { useId, useImperativeHandle, useRef, useState, type ReactNode } from "react";
import type { TreeAdapter, TreeHandle, TreeProps, TreeRowModel } from "./types";
import { useTreeState } from "./useTreeState";
import { replaceSelection } from "./selection";
import { TreeRow } from "./TreeRow";
import { IS_MAC } from "./platform";
import { keyToIntent, type KeyStroke } from "./keymap";
import {
  applyIntent,
  applyRowClick,
  applyTwistieClick,
  type ClickMods,
  type TreeAction,
} from "./dispatch";
import {
  autoScrollDelta,
  dropTargetAt,
  zoneForOffset,
  type DropResolution,
  type DropZone,
} from "./dnd";

// The component itself: keyboard + aria (T1), multi-select (T2), event wiring
// (docs/design/tree-rewrite-plan.md's module table). T0 covered mouse-only
// interaction (handleRowClick/handleTwistieClick/handleContextMenu below) plus
// the imperative reveal()/invalidate() handle; T1 added handleKeyDown, wiring
// platform.ts + keymap.ts + navigate.ts directly into real focus movement. T2
// pulls the intent -> action DECISION back out of this file into dispatch.ts's
// applyIntent (this file becomes a thin interpreter over its returned
// TreeAction[] — see handleKeyDown's own comment below for why), which is why
// navigate.ts no longer appears in the import list here: dispatch.ts imports
// it now, one layer removed from this component. T2 ALSO does the same split
// for mouse clicks — dispatch.ts's applyRowClick decides what a click (plain,
// cmd/ctrl, or shift) means, in the identical TreeAction currency applyIntent
// already produces, and handleRowClick below becomes as thin an interpreter
// over it as handleKeyDown already is over applyIntent; applyActions (defined
// once, near nodeFor below) is the ONE place that knows how a TreeAction gets
// applied, shared by both. Knows nothing about gRPC (enduring decision 1):
// only ./types, ./useTreeState, ./selection, ./TreeRow, ./platform, ./keymap
// and ./dispatch cross the import boundary.
//
// FOCUS MODEL — a deliberate READING of the plan's T1 line, not a literal
// transcription of it. The plan's phase table says "roving tabindex", which
// names the classic pattern: tabIndex=0 on whichever ROW currently holds the
// cursor, -1 on every other row, real DOM focus physically moving row to row
// on every arrow press. This component does NOT do that — DOM focus never
// leaves the container. There is exactly one focusable element (the `.tree`
// div below, tabIndex={0}), and `aria-activedescendant` names the logically
// focused row by id, with `.treerow.foc` (app-tokens.css) painting that as the
// visible cursor. This isn't a new call made here: app-tokens.css's own
// "roving-tabindex container (T1)" comment, written back at T0, already says
// outright "the TREE takes DOM focus, never a row" and suppresses the native
// ring on that basis — moving real focus between rows instead would
// contradict that already-committed CSS and reintroduce the scroll jank a
// real per-row `.focus()` call causes on every keystroke. The plan's INTENT —
// tab in once, drive the whole tree by keyboard from there, focus visibly
// distinct from selection — is fully met by aria-activedescendant; only the
// literal "tabindex moves between elements" mechanics differ from the phrase
// VS Code's own docs use for this pattern.

// Sane cap on the ancestor walk in reveal() below, guarding a misbehaving or
// cyclic adapter.getParent — see the comment at its call site.
const MAX_REVEAL_DEPTH = 1000;

// Handed to every row that is NOT renaming, so those rows get a stable array
// identity instead of a fresh `[]` per render (TreeRow is a plain function
// component today, but handing out throwaway arrays is exactly what makes it
// unmemoizable later). Never mutated — siblingLabelsFor builds a new array.
const NO_SIBLINGS: readonly string[] = [];

// Structural thenable check, duplicated from flatten.ts rather than imported: that
// file exports no such helper (it's a private implementation detail there), and
// this task's scope is Tree.tsx/TreeRow.tsx only — not editing an already-shipped,
// already-tested module just to share three lines.
function isThenable(value: unknown): value is PromiseLike<unknown> {
  return typeof (value as { then?: unknown }).then === "function";
}

// reveal(id) is handed only a STRING id (TreeHandle<T>'s contract), but
// adapter.getParent takes a NODE — so the node has to be found first. Unlike
// flatten(), which only ever walks expansion-gated (visible) nodes, this walks the
// WHOLE adapter tree unconditionally: reveal's entire purpose is to make visible a
// node that may currently be hidden behind a collapsed ancestor, so restricting the
// search to what's already visible would defeat it. A full walk is fine at this
// app's scale (plan: "dozens to low hundreds", the same reasoning that rules out
// virtualization).
function findNode<T>(adapter: TreeAdapter<T>, id: string): T | undefined {
  const search = (parent: T | undefined): T | undefined => {
    const children = adapter.getChildren(parent);
    if (isThenable(children)) {
      throw new Error(
        "Tree: reveal() walked into an adapter.getChildren() that returned a " +
          "thenable. Like flatten(), this component implements only the " +
          'synchronous TreeDataProvider path (T8, "Async children", is not built ' +
          "yet); silently skipping the branch would make reveal() quietly fail to " +
          "find a real node instead of failing loudly."
      );
    }
    for (const node of children) {
      if (adapter.getId(node) === id) return node;
      const found = search(node);
      if (found !== undefined) return found;
    }
    return undefined;
  };
  return search(undefined);
}

// Whether a keydown's real DOM target is a live text-editing control — today
// that means a row's inline rename <input> (RenameInput.tsx), the only
// in-row focusable text control that exists yet — rather than the row/
// container surface itself. handleKeyDown below bails out on this rather than
// on `renamingId`: RenameInput's own onKeyDown calls preventDefault() for
// Enter/Escape but never stopPropagation(), so every OTHER key typed while
// renaming (Space, Delete, arrows, Home/End, F2, a non-mac Enter, …) bubbles
// straight up through TreeRow's div (which has no onKeyDown of its own) to
// this container's listener and gets reinterpreted by keyToIntent — exactly
// the collision handleRowClick's `row.id === renamingId` guard prevents for
// clicks. A target check catches it more robustly than mirroring that same
// renamingId check would: React batches the state updates RenameInput's own
// handler queues (committing the rename, clearing renaming state) until after
// this whole synchronous dispatch finishes, so `renamingId` here would often
// still read as the OLD value anyway — but relying on that timing to be right
// is fragile, and doesn't generalize to any future in-row control. Checking
// the event's actual origin instead is correct on first principles and covers
// any future text control the same way. Matches monaco's own listWidget.js
// guard (`isInputElement`), which filters the identical keydown stream
// feeding Enter/Arrow/PageUp/PageDown/etc. in the widget this app already
// vendors. Duck-typed over a minimal shape (not HTMLElement/EventTarget) so
// this predicate is unit-testable with a plain object and no DOM.
export function isEditableTarget(target: { tagName?: string; isContentEditable?: boolean }): boolean {
  return (
    target.isContentEditable === true || target.tagName === "INPUT" || target.tagName === "TEXTAREA"
  );
}

// Whether a mouse event that arrived on a CLICK handler was actually produced by
// a right-click gesture — the guard the plan's §"The review T2 was owed"
// deferred to T5 ("macOS ctrl+click falls into the plain-click branch and opens
// the row ... VS Code's MouseController does guard it (isMouseRightClick → skip
// setSelection) — add that guard at T5, when there is a context menu for it to
// interact with"). Consumed as ClickMods.rightButton (dispatch.ts), which is
// where the resulting behavior is documented.
//
// What the vendored widget actually checks: `isMouseRightClick(event)` is
// `isMouseEvent(event) && event.button === 2` (listWidget.js:526-528), tested in
// `onViewPointer` at :613-615. This predicate is a deliberate SUPERSET of that,
// by one term. Two findings from reading it:
//
//   - The `button === 2` half is close to dead code in a browser. `click` is a
//     primary-button event by spec (right/middle buttons produce `auxclick`), so
//     a plain right-click never reaches a click handler with button 2 at all.
//     Mirrored anyway: it costs one comparison, it is what the widget says, and
//     the widget's own stream (onMouseClick/onMouseMiddleClick/onTap) is not
//     exactly ours.
//   - The `isMac && ctrlKey` half is the term that actually fires, and VS Code
//     does NOT have it. On macOS ctrl+click IS a right-click gesture at the OS
//     level, and Firefox delivers it as a `click` with button 0 and ctrlKey
//     true — so VS Code's check would miss it too. It never manifests there
//     because Electron is Chromium-only, and Chromium fires no `click` for
//     ctrl+click; we ship in whatever browser the user has, so the extra term is
//     the difference between "faithful" and "correct". Gated on isMac because
//     ctrl is the multi-select modKey everywhere ELSE (see handleRowClick's
//     platform ternary) — an ungated term would break cmd/ctrl+click on
//     Windows/Linux outright.
//
// isMac is a parameter, not a read of platform.ts's IS_MAC, for the same reason
// keyToIntent takes it as data: both platforms stay exercisable from one test
// process with no navigator sniffing. Duck-typed over the two fields it reads so
// it is callable with a plain object (no DOM), like isEditableTarget above.
export function isRightClickGesture(
  ev: { button: number; ctrlKey: boolean },
  isMac: boolean
): boolean {
  return ev.button === 2 || (isMac && ev.ctrlKey);
}

// PageUp/PageDown's rowsPerPage (in the "move" case below) needs the height of
// the actual SCROLLING viewport onto the tree, not `.tree`'s own box.
// app-tokens.css gives `.tree` no height or overflow of its own (only
// `outline: none`, for the roving-focus ring — see the FOCUS MODEL comment
// above), so as a plain block element it lays out to the summed height of
// every row it renders — its full CONTENT height, not the visible window a
// caller scrolls it inside. CollectionPanel wraps <Tree> in its own
// `overflow:auto` div for exactly that scrolling; walking up to the nearest
// ancestor whose COMPUTED overflow-y is auto/scroll finds that real viewport
// regardless of how many wrapper elements sit between it and `.tree` — a
// hardcoded "my parent is the scrollport" assumption would silently break the
// moment a caller adds one more wrapping div (e.g. a future descriptor-
// explorer consumer). Falls back to `.tree` itself if no scrollable ancestor
// exists at all (a standalone embedding with no scrolling wrapper) — the
// pre-existing, merely-imprecise behavior for that case, not a new one.
function findScrollport(el: HTMLElement): HTMLElement {
  for (let node = el.parentElement; node !== null; node = node.parentElement) {
    const overflowY = getComputedStyle(node).overflowY;
    if (overflowY === "auto" || overflowY === "scroll") return node;
  }
  return el;
}

// Where the pointer is currently offering to drop (T6b): the row it is over, the
// side of that row (dnd.ts's zone geometry), and the destination that resolves to.
// The resolution is carried alongside rather than recomputed at drop time so the
// drop uses exactly the destination the INDICATOR promised — the row's `data-index`
// and the flat array can both have moved on between the last dragover and the drop
// (a mutation elsewhere reseeding the workspace cache mid-drag), and honouring the
// stale-but-shown answer is the behavior the user consented to. A `before` naming a
// child that no longer exists appends rather than failing, server-side
// (MoveItemRequest's own documentation), so the worst case of a stale resolution is
// a position one off — not an error.
interface DropState {
  rowId: string;
  zone: DropZone;
  res: DropResolution;
}

export function Tree<T>(props: TreeProps<T>): ReactNode {
  const {
    adapter,
    handle,
    renderRow,
    onOpen,
    onDelete,
    onContextMenu,
    onRenameCommit,
    onMove,
    canDrop,
    activeId = null,
    indent = 8,
    rowHeight = 22,
  } = props;
  const ariaLabel = props["aria-label"];
  // compactFolders is part of TreeProps<T> and therefore already accepted by
  // `props` above — it is T7 polish (folder-chain compression) and deliberately
  // does nothing here; there is no T0 behavior to wire it into yet.

  const {
    flat,
    expanded,
    setExpanded,
    selection,
    setSelection,
    focused,
    setFocused,
    anchor,
    setAnchor,
  } = useTreeState(props);

  // Which row, if any, is mid-rename — INTERNAL state as of T4b. Through T0/T1
  // this was a pair of bridge props (`renamingId` / `onRenamingChange`, both
  // deleted from TreeProps), because the HOST rendered the edit box and so had to
  // own "which row". The component renders it now (TreeRow -> RenameInput), so
  // there is nothing left for a host to track: it hears about a rename exactly
  // once, as onRenameCommit. Deliberately NOT routed through useTreeState's
  // controlled/uncontrolled seam like expanded/selection/focused — those three
  // are controlled because grpcview persists them in zustand across renders and
  // the plan requires it (enduring decision 5); a half-finished rename is
  // ephemeral by nature, and no consumer has asked to drive or read it (plan
  // §Risks, "Over-fitting": name the consumer that wants it). The host's own
  // pencil reaches it through TreeHandle.startRename instead.
  const [renamingId, setRenamingId] = useState<string | null>(null);

  // Container ref: the starting point for finding the real scrollport that
  // measures PageUp/PageDown's rowsPerPage (no hardcoded viewport assumption —
  // see findScrollport and handleKeyDown's "move" case above/below) and is
  // where keydown itself is wired (FOCUS MODEL, above). rowEls is a plain
  // (non-state) ref — writing to it must never itself trigger a render — that
  // exists purely so a keyboard move can scrollIntoView the row it just
  // focused, reusing the DOM node TreeRow already mounted for that id rather
  // than querying for it.
  const containerRef = useRef<HTMLDivElement>(null);
  const rowEls = useRef<Map<string, HTMLDivElement>>(new Map());

  // DRAG STATE (T6b), two values, each held as STATE *and* mirrored in a REF. The
  // doubling is not redundancy: the state copy is what paints (the dragged rows'
  // in-flight opacity, the drop indicator), and the ref copy is what a drag handler
  // READS, because the handlers run in a stream of events that can fire several
  // times before React commits anything — a `dragover` reading the state value from
  // its closure would be reading whatever the last committed render held, and
  // `handleDrop` in particular has to see the resolution the very last `dragover`
  // computed, not the one from the render before it. Both are written together
  // through the two setters below so they cannot diverge.
  //
  // `dragIds` is ALSO why the drag payload never round-trips through `dataTransfer`
  // (see handleDragStart): the ids live here for the drag's whole life.
  const [dragIds, setDragIdsState] = useState<readonly string[] | null>(null);
  const dragIdsRef = useRef<readonly string[] | null>(null);
  const setDragIds = (next: readonly string[] | null): void => {
    dragIdsRef.current = next;
    setDragIdsState(next);
  };
  const [dropState, setDropStateValue] = useState<DropState | null>(null);
  const dropStateRef = useRef<DropState | null>(null);
  // Deduplicated on (row, zone): `dragover` fires continuously — on every pointer
  // move and, per the HTML drag-and-drop processing model, at least every 350ms even
  // for a stationary pointer — and the indicator only changes when one of those two
  // changes. Without this the whole tree re-renders several times a second for the
  // duration of every drag.
  const setDropState = (next: DropState | null): void => {
    const current = dropStateRef.current;
    if (current === null && next === null) return;
    if (current !== null && next !== null && current.rowId === next.rowId && current.zone === next.zone) {
      return;
    }
    dropStateRef.current = next;
    setDropStateValue(next);
  };

  // aria-activedescendant needs a DOM id, but a row's OWN id (adapter.getId —
  // e.g. request-tree.tsx's path+name itemKey) is user-authored text that can
  // contain spaces, slashes, or be empty — none of which are safe to use as a
  // DOM id verbatim. In particular an ARIA IDREF may not contain whitespace,
  // and itemKey's path+name join already produces one with a "/" for any
  // nested row ("Calls/My Request" has a literal space the moment a request
  // is named with one). useId()'s value is this component INSTANCE's own
  // unique, always-non-empty, whitespace-free prefix; encodeURIComponent does
  // the equivalent for the row id's own text (escaping whitespace, slashes,
  // quotes, anything). Concatenating the two is always a valid, collision-free
  // DOM id — including collision-free across two Tree instances on one page
  // (this app's request tree today, a future descriptor-explorer tree
  // tomorrow) that might otherwise mint the identical row id.
  const treeId = useId();
  const domIdFor = (id: string): string => `${treeId}${encodeURIComponent(id)}`;

  // The row the roving cursor is currently on, resolved once per render for
  // both the container's aria-activedescendant and handleKeyDown below. `??
  // null` also quietly covers a STALE `focused` — an id no longer present in
  // this pass (e.g. hidden by CollectionPanel's own filter box) — the same way
  // navigate.ts's targetIndex already treats an unknown fromId as "nothing":
  // flat.rows[-1] is `undefined` in JS, same as a genuinely absent id would be.
  const focusedRow: TreeRowModel<T> | null =
    focused === null ? null : flat.rows[flat.indexById.get(focused) ?? -1] ?? null;

  useImperativeHandle(handle, (): TreeHandle<T> => ({
    reveal(id, opts) {
      const target = findNode(adapter, id);
      if (target === undefined) return; // nothing by that id to reveal

      const toExpand = new Set<string>();
      // Guard against a missing getParent (optional in the contract): with no way
      // to walk upward, reveal() can't force any ancestor open. It still degrades
      // usefully rather than doing nothing at all — select/focus below still apply,
      // taking effect if `id` already happens to be visible.
      const getParent = adapter.getParent;
      if (getParent) {
        const seen = new Set<string>([id]);
        let current = target;
        for (let hops = 0; hops < MAX_REVEAL_DEPTH; hops++) {
          const parent = getParent(current);
          if (parent === undefined) break; // reached a root
          const parentId = adapter.getId(parent);
          if (seen.has(parentId)) break; // cycle guard: a repeated id stops the walk
          seen.add(parentId);
          toExpand.add(parentId);
          current = parent;
        }
      }
      // opts.expand additionally opens the revealed node ITSELF, not just its
      // ancestors — revealing a folder without this makes the folder visible but
      // leaves it closed, a real and useful distinction from also showing what's
      // inside it.
      if (opts?.expand && adapter.getCollapsibleState(target) !== "none") {
        toExpand.add(id);
      }
      if (toExpand.size > 0) {
        setExpanded(new Set([...expanded, ...toExpand]));
      }
      if (opts?.select) setSelection(replaceSelection(id));
      if (opts?.focus) setFocused(id);
    },
    invalidate() {
      // Documented no-op: there is no async children cache yet to invalidate (T8
      // adds one, for the promise-returning getChildren path). A real, callable
      // method now means call sites don't need to change when T8 wires it up.
    },
    startRename(id) {
      // Gated on `flat`, not on reveal()'s findNode walk: the requirement is "an
      // id that names a CURRENT ROW", and flat.indexById is literally that.
      // reveal() has to walk the whole adapter instead because its whole job is
      // reaching a node hidden behind a collapsed ancestor — the opposite of this
      // method's, which has nowhere to render an input unless a row already
      // exists. Refusing here is what keeps a stale id (e.g. one the filter box
      // has since hidden) from leaving the tree in a rename with no visible box
      // to commit or escape from.
      if (!flat.indexById.has(id)) return;
      setRenamingId(id);
    },
  }));

  // Moves the roving cursor to `id`, scrolling it into view only when the
  // action that asked for the move says to. Every focus change in this
  // component — keyboard or mouse — funnels through this ONE helper via
  // applyActions below; `scroll` is carried as data on the "focus" TreeAction
  // (dispatch.ts) rather than by having two focus code paths.
  //
  // Why it isn't unconditional (it was, briefly, and that was a real bug):
  // `scrollIntoView({block: "nearest"})` is only a no-op for a target ENTIRELY
  // inside its scrolling ancestor. A row clipped at the viewport edge is not —
  // "nearest" is precisely the mode that scrolls just enough to un-clip it —
  // so routing MOUSE clicks through an unconditional scroll made clicking a
  // partially visible row's sliver yank the list under the cursor (measured:
  // scrollTop 111 -> 99 on a 12px-clipped row, which is enough to land the
  // pointer on a different row than the one that was clicked). Keyboard moves
  // want exactly the opposite — an arrow/page/Home/End move routinely names a
  // row that is off-screen, and a cursor you can't see is useless — so the two
  // producers differ, not the two helpers.
  const focusRow = (id: string, scroll: boolean): void => {
    setFocused(id);
    if (scroll) rowEls.current.get(id)?.scrollIntoView({ block: "nearest" });
  };

  // Resolves a row id back to its real node, via the same flat.indexById
  // lookup used everywhere in this file that only has an id to work from:
  // reveal() above (indirectly, through setSelection/setFocused), applyActions'
  // "open"/"delete" TreeAction cases below (shared by the keyboard and mouse
  // interpreters), and handleContextMenu further below. Pulled out once here
  // rather than left as near-identical inline expressions at each call site.
  // `undefined` covers an id that no longer names a CURRENT row — e.g.
  // `selection` still holding an id the filter box has since hidden from
  // `flat` — which every call site already has to filter out; that defensive
  // filter is unchanged by this extraction, only de-duplicated.
  const nodeFor = (id: string): T | undefined => flat.rows[flat.indexById.get(id) ?? -1]?.node;

  // The collision set the rename box validates against (T4b): the labels of this
  // row's other VISIBLE siblings — the same "set" aria-posinset/aria-setsize
  // already mean (TreeRowModel, types.ts), i.e. the rows sharing its parentId in
  // THIS flat pass, never every child getChildren() could return. Read through
  // adapter.getTreeItem(...).label for both tiers; see TreeRow.tsx's own comment
  // for why reading only `.label` off a rich adapter is a deliberate exception.
  // Computed only for the row actually renaming (below) — one filter+map over the
  // flat array per keystroke of the whole tree would be waste, and the box needs
  // this list exactly once, when it mounts.
  const siblingLabelsFor = (row: TreeRowModel<T>): string[] =>
    flat.rows
      .filter((r) => r.parentId === row.parentId && r.id !== row.id)
      .map((r) => adapter.getTreeItem(r.node).label);

  // Applies a list of TreeAction (dispatch.ts's shared currency for BOTH
  // applyIntent, the keyboard decision, and applyRowClick, the mouse decision
  // added this phase) by performing exactly one state-setter or callback call
  // per action, in order. This is the ONE place in the whole component that
  // knows HOW an action takes effect — handleKeyDown and handleRowClick below
  // each only have to decide WHICH actions apply (by calling into dispatch.ts)
  // and hand the result here, rather than each maintaining its own copy of
  // this switch. Before this phase, this loop lived inline at the end of
  // handleKeyDown alone (T1/T2's own extraction history); pulling it out is
  // what lets handleRowClick and handleTwistieClick reuse it verbatim for the
  // mouse paths this phase adds, instead of duplicating every case a second
  // time.
  const applyActions = (actions: readonly TreeAction[]): void => {
    // Expansion changes are FOLDED across the whole list and written once, at
    // the end, rather than one setExpanded call per action. Each call would
    // otherwise derive `new Set(expanded)` from this render's closure, so two
    // setExpanded actions in one list would silently lose the first — the
    // second would be built from the same stale `expanded` and overwrite it.
    // No producer emits two today, but this is the shared sink for every
    // producer there is (and T5's context menu / T6's drag-drop are the
    // obvious future emitters of "collapse the source, expand the target" in
    // one list), so the sink should not have a one-action-per-kind ceiling
    // hiding in it. Folding, rather than a functional setState update, because
    // setExpanded is useTreeState's controlled/uncontrolled seam and takes a
    // VALUE, not an updater — and folding keeps a controlled host seeing one
    // onExpandedChange per interaction either way.
    let nextExpanded: Set<string> | null = null;

    for (const action of actions) {
      switch (action.kind) {
        case "focus":
          focusRow(action.id, action.scroll);
          break;
        case "setExpanded": {
          // A DESIRED final state, not a toggle — see TreeAction's own
          // comment (dispatch.ts). Applied against the running fold, so a
          // later action in the same list sees the earlier one's effect.
          nextExpanded = new Set(nextExpanded ?? expanded);
          if (action.expanded) nextExpanded.add(action.id);
          else nextExpanded.delete(action.id);
          break;
        }
        case "setSelection":
          setSelection(action.ids);
          break;
        case "setAnchor":
          setAnchor(action.id);
          break;
        case "open": {
          const node = nodeFor(action.id);
          if (node !== undefined) onOpen?.(node);
          break;
        }
        case "requestRename":
          // T4b: lands in this component's own state instead of being forwarded
          // to a host callback. dispatch.ts already guarantees the id names a row
          // in the CURRENT flat pass (applyIntent re-validates ctx.focused
          // against `flat` before emitting), so there is no equivalent of
          // startRename's own membership check to repeat here.
          setRenamingId(action.id);
          break;
        case "delete": {
          const nodes = action.ids.map(nodeFor).filter((n): n is T => n !== undefined);
          onDelete?.(nodes);
          break;
        }
      }
    }

    // The single write the fold above earned. `null` means no action in this
    // list touched expansion at all, which is not the same as "expand nothing"
    // — writing an unchanged set here would still hand a controlled host a new
    // Set identity on every keystroke.
    if (nextExpanded !== null) setExpanded(nextExpanded);
  };

  // Click, now MODIFIER-AWARE (T2 — docs/design/tree-rewrite-plan.md's "Mouse"
  // list; previously this comment said the opposite: "Deliberately ignorant of
  // modifier keys ... cmd/ctrl+click and shift+click are T2"). Deciding what a
  // click MEANS — a plain click's select+focus+anchor+open-or-toggle, a
  // cmd/ctrl+click's pure toggle, a shift+click's range-extend — is
  // dispatch.ts's applyRowClick now, not this function's: exactly the same
  // "this file is a thin INTERPRETER" split T1/T2 already made for
  // handleKeyDown/applyIntent (see that function's own comment below), now
  // covering mouse too. applyActions (above) is what actually performs
  // whatever applyRowClick decided.
  //
  // Still deliberately ignorant of double-click: a deliberate deviation from
  // VS Code (plan §"Deliberate deviations" #2) — with no onDoubleClick handler
  // at all, a double-click is just two ordinary clicks in a row, i.e.
  // genuinely a no-op beyond what one click already does.
  const handleRowClick = (row: TreeRowModel<T>, ev: React.MouseEvent): void => {
    // A row reporting itself as mid-rename swallows its own click entirely —
    // modified or not: there is no reading of e.g. "cmd+click a row that's
    // mid-rename" that should toggle it into a multi-selection rather than
    // simply being swallowed, since the row isn't an ordinary selectable item
    // again until the rename resolves. Mirrors the pre-rewrite TreeView.tsx's
    // per-row `editing ? undefined : ...` guard, which this component has to
    // reproduce explicitly because rename state is not a per-row useState the
    // row's own onClick prop could close over. Strictly MORE reliable as of T4b
    // than it was through T0/T1: `renamingId` is this component's own state now,
    // not a value round-tripped through a host that could hand back a stale one.
    // Checked BEFORE building an intent or calling into
    // dispatch.ts at all — the same "bail before doing anything else" shape
    // handleKeyDown's own isEditableTarget guard uses, and a hard constraint
    // of this phase (preserve T1's guards) that this one predates but must
    // keep holding regardless.
    if (row.id === renamingId) return;

    // modKey resolves the SAME platform ternary keymap.ts's cmd/ctrl+A
    // already does (that file's onlyCtrlHeld/onlyMetaHeld comments;
    // listWidget.js:520-521's isSelectionSingleChangeEvent) — evaluated HERE,
    // against the real MouseEvent, because dispatch.ts's applyRowClick takes
    // it as a plain boolean rather than resolving IS_MAC itself (ClickMods'
    // own comment, dispatch.ts — the same split keyToIntent already uses for
    // `isMac`, for the same "keep the decision layer pure data-in-data-out"
    // reason).
    const mods: ClickMods = {
      shiftKey: ev.shiftKey,
      modKey: IS_MAC ? ev.metaKey : ev.ctrlKey,
      rightButton: isRightClickGesture(ev, IS_MAC),
    };
    applyActions(applyRowClick(row, mods, { flat, focused, selection, anchor }));
  };

  // The twistie is a separate hit target specifically so it can toggle WITHOUT
  // selecting — stopPropagation is what keeps handleRowClick (which DOES
  // select) from also firing for the same click. Still ignorant of modifiers
  // (the plan's "Mouse" list has exactly one, unconditional twistie row; see
  // dispatch.ts's twistie section for the richer VS Code behavior deliberately
  // not replicated), but no longer a bare toggle: a COLLAPSE can hide the very
  // rows focus and selection point at, so what happens to them is a decision,
  // and every decision in this component belongs in dispatch.ts. Interpreting
  // its actions goes through the same applyActions as the keyboard and the row
  // click — this handler stays as thin as the other two.
  const handleTwistieClick = (row: TreeRowModel<T>, ev: React.MouseEvent): void => {
    ev.stopPropagation();
    applyActions(applyTwistieClick(row, { flat, focused, selection, anchor }));
  };

  // Right-click: select the row if it isn't already selected, move focus to it,
  // then hand off to the caller. The tree still does NOT render a menu — that is
  // the host's job (see components/ui/Menu.tsx's header, and the plan's §Risks
  // "Scope creep into the row renderer"), because the items are entirely
  // gRPC-shaped and enduring decision 1 forbids this component knowing about
  // them. preventDefault so the browser's native menu doesn't show atop whatever
  // the caller does with the callback; nodes are computed from the POST-click
  // selection (not the stale closure value), since setSelection's effect isn't
  // visible until the next render.
  //
  // FOCUS MOVES, as of T5 — the two things T2 recorded as deliberately not done
  // here, both resolved this phase:
  //
  // (1) The base widget's own dedicated onContextMenu (listWidget.js:583-589)
  //     was re-read rather than taken on trust, and it does exactly one thing:
  //     `this.list.setFocus(focus, e.browserEvent)`, with no setSelection and no
  //     setAnchor anywhere in the method. (It also computes `focus` as `[]` when
  //     `e.index` is undefined, i.e. a right-click on empty space CLEARS focus —
  //     not reproducible from here, since this handler only ever fires for a
  //     row; CollectionPanel's own panel-level handler covers empty space and
  //     leaves tree state alone.) So moving focus is the faithful behavior, and
  //     it is also the useful one: it is what makes the very next keystroke after
  //     a right-click (Escape, an arrow, F2) act on the row the user pointed at.
  //     scroll: false, like every mouse-driven focus in this file — the pointer
  //     is physically on the row already, and scrolling a partially clipped row
  //     into view would yank the list out from under the cursor (plan §"The
  //     review T2 was owed" #3).
  //
  // (2) The stale ANCHOR is fixed, but only in the branch that has one: when
  //     this right-click REPLACES the selection, the anchor is left pointing at a
  //     row that is no longer selected at all, so a following shift+click would
  //     extend from nowhere the user can see. Setting it to the clicked row
  //     matches listWidget.js:611-612 (`setFocus` + `setAnchor` for any pointer
  //     landing on a row) and costs nothing. When the row is ALREADY selected the
  //     anchor is left untouched: the existing selection and its anchor are
  //     coherent, and silently re-anchoring a multi-row selection onto whichever
  //     of its rows happened to be right-clicked would change what a later
  //     shift+click extends — a real behavior change, and the widget's own
  //     onContextMenu does not make it.
  //
  // A right-click on a row that is mid-rename is handed to the browser instead.
  // The native menu is the only way to paste into that input, and monaco's
  // controller guards the identical case the identical way (listWidget.js:584's
  // `isInputElement(e.browserEvent.target)` early return).
  const handleContextMenu = (row: TreeRowModel<T>, ev: React.MouseEvent): void => {
    // No menu wired up by the host means truly do nothing — not even
    // preventDefault — so the browser's native context menu still shows, exactly
    // as it did before this component existed at all.
    if (!onContextMenu) return;
    if (isEditableTarget(ev.target as HTMLElement)) return;
    ev.preventDefault();
    const nextSelection = selection.includes(row.id) ? selection : replaceSelection(row.id);
    if (nextSelection !== selection) {
      setSelection(nextSelection);
      setAnchor(row.id);
    }
    focusRow(row.id, false);
    const nodes = nextSelection.map(nodeFor).filter((n): n is T => n !== undefined);
    onContextMenu(nodes, ev);
  };

  // ── drag and drop (T6b) ──────────────────────────────────────────────────────
  // NATIVE HTML5 drag and drop, no library (the plan's T6b line: "Native HTML5
  // first; react-dnd@16 only if it falls short" — it did not fall short; see
  // §"What T6b settled"). The DECISIONS all live in dnd.ts, pure and unit-tested
  // (zone geometry, zone -> destination, validity, the autoscroll step); what is
  // left here is the DOM half no pure module could do: reading a pointer, measuring
  // a row's box, scrolling the scrollport, and turning ids back into nodes for the
  // host's two callbacks. Exactly the same split as keyboard/mouse (dispatch.ts).
  //
  // ONE delegated set of handlers on the CONTAINER, not per row — only `dragstart`
  // is per-row (the row is what becomes draggable). This is monaco's own structure
  // (listView.js binds the dnd stream to its one dom node and recovers the row via
  // `data-index`, :893+) and it removes an entire class of flicker: with per-row
  // handlers, moving the pointer from one row to the next fires `dragleave` on the
  // old row BEFORE `dragover` on the new one, so a naive per-row clear blanks the
  // indicator between every pair of rows. Delegated, `dragleave` fires only when the
  // pointer leaves the tree entirely.

  // The dragged rows, in ROW ORDER regardless of the order `selection` happens to
  // hold them in — a multi-row move fires one MoveItem per node with the same
  // `before`, so each insertion lands ahead of that sibling and the firing order IS
  // the resulting sibling order. Visual order in, visual order out.
  const draggedNodes = (ids: readonly string[]): T[] =>
    [...ids]
      .filter((id) => flat.indexById.has(id))
      .sort((a, b) => (flat.indexById.get(a) ?? 0) - (flat.indexById.get(b) ?? 0))
      .map((id) => flat.rows[flat.indexById.get(id) ?? -1].node);

  // dnd.ts speaks ids; TreeProps.onMove/canDrop speak nodes. This is the one
  // boundary between them. `null` means the resolution named a row that has since
  // vanished, which is not a drop this component can describe to a host.
  const destinationFor = (res: DropResolution): { parent: T | null; before?: T } | null => {
    let parent: T | null = null;
    if (res.parentId !== null) {
      const node = nodeFor(res.parentId);
      if (node === undefined) return null;
      parent = node;
    }
    if (res.beforeId === null) return { parent };
    const before = nodeFor(res.beforeId);
    // A missing `before` degrades to append rather than rejecting the drop —
    // MoveItemRequest.before does the same thing server-side for a name that no
    // longer identifies a child, on the same reading (a stale sibling is a UI race,
    // not a corrupt request).
    return before === undefined ? { parent } : { parent, before };
  };

  const endDrag = (): void => {
    setDragIds(null);
    setDropState(null);
  };

  const handleDragStart = (row: TreeRowModel<T>, ev: React.DragEvent): void => {
    // WHICH rows travel: the whole selection if the gesture started on a selected
    // row, otherwise just this one — and in that second case the selection is
    // REPLACED by it first, which is what VS Code's explorer does and what keeps the
    // highlight from disagreeing with what is visibly in flight. The anchor moves
    // with it for the same reason handleContextMenu moves it when it starts a new
    // selection (listWidget.js:611-612): leaving it on a row that is no longer
    // selected makes the next shift+click extend from nowhere. Focus is deliberately
    // NOT moved — a drag fires no `click`, the widget's own drag path sets no focus,
    // and moving the roving cursor on a gesture the user may abort mid-air would be
    // a change they never asked for.
    let ids: readonly string[];
    if (selection.includes(row.id)) {
      ids = selection.filter((id) => flat.indexById.has(id));
    } else {
      ids = replaceSelection(row.id);
      setSelection(ids);
      setAnchor(row.id);
    }
    setDragIds(ids);

    // dataTransfer is written ONLY to make the browser treat this as a real drag —
    // a drag with an empty dataTransfer is cancelled outright in some browsers — and
    // is never read back. It CANNOT be the drag payload: `getData` is deliberately
    // unreadable during `dragover` in every browser (the drag data store is in
    // protected mode until drop), which is exactly when the tree needs to know what
    // is being dragged in order to decide whether a drop is legal. So node identity
    // lives in `dragIdsRef` for the drag's whole life and this line is inert. Do not
    // "clean it up" into a JSON payload and read it in handleDrop: that reintroduces
    // the dragover blindness the ref exists to avoid.
    //
    // The text is the dragged rows' LABELS, so dropping onto some unrelated text
    // target outside the app pastes something a human recognises rather than
    // path-derived internal ids. Reading `.label` off a rich adapter is the same
    // narrow exception TreeRow.tsx's rename branch already makes and documents — it
    // is the only adapter-independent answer to "what is this row called".
    ev.dataTransfer.effectAllowed = "move";
    ev.dataTransfer.setData(
      "text/plain",
      draggedNodes(ids)
        .map((node) => adapter.getTreeItem(node).label)
        .join("\n")
    );
  };

  // The row (and its element) under a drag event's target. Walks up from the real
  // target — which is whatever descendant of the row the pointer happens to be over,
  // a label span or a hover button — to the nearest `data-index` carrier, exactly as
  // listView.js's getItemIndexFromEventTarget does. Bounded to THIS tree's container
  // so a nested tree's rows can never be mistaken for ours.
  const rowElementFor = (ev: React.DragEvent): { index: number; el: HTMLElement } | null => {
    const container = containerRef.current;
    const target = ev.target;
    if (container === null || !(target instanceof HTMLElement)) return null;
    const el = target.closest<HTMLElement>("[data-index]");
    if (el === null || !container.contains(el)) return null;
    const index = Number(el.dataset.index);
    if (!Number.isInteger(index) || index < 0 || index >= flat.rows.length) return null;
    return { index, el };
  };

  // Scrolls the real scrollport (findScrollport — `.tree` has no bounded height of
  // its own) when the pointer is within dnd.ts's edge band. Driven off the dragover
  // events themselves rather than an rAF loop or an interval: see autoScrollDelta's
  // own comment for why (no timer to leak, and rAF does not fire in the automated
  // browser harness at all).
  const autoScroll = (pointerY: number): void => {
    const container = containerRef.current;
    if (container === null) return;
    const port = findScrollport(container);
    const rect = port.getBoundingClientRect();
    const delta = autoScrollDelta(pointerY, rect.top, rect.bottom);
    if (delta !== 0) port.scrollTop += delta;
  };

  const handleDragOver = (ev: React.DragEvent<HTMLDivElement>): void => {
    // A drag that did not start in this tree — a file from the desktop, a selection
    // from another panel — is left entirely alone: no indicator, and crucially no
    // preventDefault, so the browser shows its own no-drop cursor and whatever
    // outer handler wants it still gets it.
    const ids = dragIdsRef.current;
    if (ids === null) return;

    // Before the target check, so autoscroll keeps working while the pointer is over
    // a gap or past the last row — those are precisely the positions a user reaches
    // for when they want the list to move.
    autoScroll(ev.clientY);

    const hit = rowElementFor(ev);
    if (hit === null) {
      setDropState(null);
      return;
    }
    const row = flat.rows[hit.index];
    const rect = hit.el.getBoundingClientRect();
    const zone = zoneForOffset({
      offsetY: ev.clientY - rect.top,
      // The MEASURED height, not the `rowHeight` prop: the two agree today, but the
      // geometry has to be relative to the box the pointer is actually inside.
      rowHeight: rect.height,
      expandable: row.expandable,
    });
    const res = dropTargetAt(flat, hit.index, zone, ids);
    if (res === null) {
      setDropState(null);
      return;
    }
    // The host's veto, consulted only for drops the tree already considers legal —
    // it exists for what the tree CANNOT know (a destination that already holds an
    // item with this display name, whose children may be collapsed or filtered out
    // of `flat` entirely), never as a second copy of the structural rules.
    const to = destinationFor(res);
    if (to === null || (canDrop && !canDrop(draggedNodes(ids), to))) {
      setDropState(null);
      return;
    }

    // preventDefault is what makes a drop possible AT ALL, and it is called only on
    // this accepted path — which is how an invalid target gets the native no-drop
    // cursor for free, with no cursor management of our own.
    ev.preventDefault();
    ev.dataTransfer.dropEffect = "move";
    setDropState({ rowId: row.id, zone, res });
  };

  // Fires only when the pointer leaves the whole tree (handlers are delegated), and
  // `relatedTarget` — the element being entered — is what distinguishes that from
  // crossing between two of our own descendants. A null relatedTarget (leaving the
  // window entirely) correctly falls through to the clear.
  const handleDragLeave = (ev: React.DragEvent<HTMLDivElement>): void => {
    const entering = ev.relatedTarget;
    if (entering instanceof Node && ev.currentTarget.contains(entering)) return;
    setDropState(null);
  };

  const handleDrop = (ev: React.DragEvent<HTMLDivElement>): void => {
    const ids = dragIdsRef.current;
    const drop = dropStateRef.current;
    endDrag();
    if (ids === null || drop === null) return;
    ev.preventDefault();
    const to = destinationFor(drop.res);
    const nodes = draggedNodes(ids);
    if (to !== null && nodes.length > 0) onMove?.(nodes, to);
  };

  // Fires on the source row for every drag that ends, dropped or cancelled, and
  // bubbles to this container — so this is the one guaranteed teardown. There is no
  // timer or animation frame to cancel here by design (see autoScroll above), which
  // is why nothing needs an unmount cleanup.
  const handleDragEnd = (): void => endDrag();

  // The keyboard INTERPRETER (docs/design/tree-rewrite-plan.md's "VS Code UX
  // spec"). Building a KeyStroke from the real event and resolving it to an
  // intent is still done here (keyToIntent stays a DOM-free table, per its
  // own header comment) — but deciding what an intent MEANS, which rows get
  // focused/selected/expanded, whether the anchor moves, is dispatch.ts's job
  // now (applyIntent), not this function's. That split is this phase's own
  // refactor, per the plan's "What T1 settled" note ("T2 has to edit that same
  // switch ... extract it into a pure applyIntent(intent, ctx) -> actions"):
  // the ~100-line intent switch that used to live directly in this function
  // was the one piece of the keyboard path with no unit-test coverage (every
  // OTHER piece — keyToIntent, the navigate.ts lookups, isEditableTarget — is
  // covered in isolation), because it needed real component state and this
  // suite has no DOM to drive one from. dispatch.test.ts is what that
  // coverage looks like now that the decision is a pure function.
  //
  // preventDefault fires only once an intent was actually produced: an
  // unclaimed key (a plain letter — T3 typeahead's job, not built yet; any
  // modifier combination this table doesn't bind) falls through completely
  // untouched, so e.g. cmd+C or a browser shortcut still works normally while
  // the tree has focus, and an unhandled arrow/Space still scrolls the panel
  // exactly as if this handler didn't exist.
  //
  // For WHY a given intent produces the particular actions it does — the
  // anchor-reset rule on a plain move, the open-vs-toggle fork on a folder,
  // the focused-vs-selection resolution for delete, each with its own
  // monaco file:line citation — see dispatch.ts's applyIntent, not here.
  // Duplicating that reasoning in two files would only let them drift out of
  // sync; HOW each returned TreeAction gets APPLIED, a genuinely different
  // (and much smaller) concern, is applyActions above — shared verbatim with
  // handleRowClick's own applyRowClick-driven actions below, so there is
  // exactly one switch in this file translating a TreeAction into a real
  // state change, not one per input modality.
  const handleKeyDown = (ev: React.KeyboardEvent<HTMLDivElement>): void => {
    // A live text control inside a row (today: RenameInput's <input>)
    // must handle its own keystrokes untouched — see isEditableTarget's
    // comment above for why this, not a renamingId check, is the guard.
    if (isEditableTarget(ev.target as HTMLElement)) return;

    const stroke: KeyStroke = {
      key: ev.key,
      shiftKey: ev.shiftKey,
      metaKey: ev.metaKey,
      ctrlKey: ev.ctrlKey,
      altKey: ev.altKey,
    };
    const intent = keyToIntent(stroke, IS_MAC);
    if (intent === null) return;
    // Claimed: arrows/Space/PageUp.../Home/End must not ALSO scroll the panel
    // (the browser's native behavior for these keys inside a scrollable
    // container) now that this handler is the one moving the cursor.
    ev.preventDefault();

    // rowsPerPage is MEASURED, never assumed — a hardcoded page size would
    // either page too far (a short panel) or too little (a maximized window)
    // relative to what's actually on screen. Measured off the real scrollport
    // (findScrollport above), not `.tree`'s own clientHeight — `.tree` has no
    // bounded height of its own, so its clientHeight is its full CONTENT
    // height, which would make a page roughly "the whole tree" instead of one
    // screen. Floored, minimum 1 (matching navigate.ts's own pageStride
    // guard) so a page move is never a dead key just because nothing has laid
    // out yet (clientHeight reads 0 before first paint) or the panel is
    // shorter than one row. Computed unconditionally for every claimed key,
    // not only for "move"/"extend" (the only two intents that actually
    // consume it): applyIntent's ctx always takes a rowsPerPage, and a
    // getComputedStyle walk up a handful of ancestors is cheap enough that
    // gating it per-intent-kind would only complicate this function for no
    // measurable benefit.
    const viewportHeight = containerRef.current
      ? findScrollport(containerRef.current).clientHeight
      : 0;
    const rowsPerPage = Math.max(1, Math.floor(viewportHeight / rowHeight));

    const actions = applyIntent(intent, { flat, focused, selection, anchor, rowsPerPage });
    // Applied in order, one at a time, via the shared interpreter above —
    // ordering matters only in the sense that it matches the order dispatch.ts
    // chose to list actions in (e.g. "open"'s setSelection before its
    // setExpanded/open — see that case's own comment for why selecting first
    // is the reading that matches handleRowClick's mouse equivalent).
    applyActions(actions);
  };

  return (
    <div
      ref={containerRef}
      className="tree"
      role="tree"
      aria-label={ariaLabel}
      // Without this, ARIA defines a `tree` as SINGLE-select, so assistive
      // tech announces each newly selected row as having REPLACED whatever was
      // selected before — silently misreporting the whole multi-select feature
      // (T2) to exactly the users who have no other way to perceive it. Hard-
      // coded true rather than derived from a prop: there is no single-select
      // mode in this component (shift+click, shift+arrow and cmd/ctrl+A are
      // unconditional), so a prop would only be able to lie.
      aria-multiselectable="true"
      // The single focusable element (FOCUS MODEL, above) — every row is
      // inert to Tab on purpose, so one Tab reaches the whole tree and a
      // second Tab leaves it, exactly like any other VS Code list widget.
      tabIndex={0}
      aria-activedescendant={focusedRow ? domIdFor(focusedRow.id) : undefined}
      onKeyDown={handleKeyDown}
      // Every drag event EXCEPT dragstart is delegated here rather than bound per
      // row — see the drag-and-drop section above for why (row-to-row flicker, and
      // monaco's own structure).
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
      onDragEnd={handleDragEnd}
    >
      {flat.rows.map((row, index) => (
        <TreeRow
          key={row.id}
          row={row}
          domId={domIdFor(row.id)}
          dataIndex={index}
          rowRef={(el) => {
            if (el) rowEls.current.set(row.id, el);
            else rowEls.current.delete(row.id);
          }}
          adapter={adapter}
          renderRow={renderRow}
          selected={selection.includes(row.id)}
          focused={focused === row.id}
          active={row.id === activeId}
          renaming={row.id === renamingId}
          dropTarget={dropState?.rowId === row.id ? dropState.zone : null}
          dropDepth={dropState?.rowId === row.id ? dropState.res.depth : 0}
          dragging={dragIds?.includes(row.id) ?? false}
          renameSiblings={row.id === renamingId ? siblingLabelsFor(row) : NO_SIBLINGS}
          // Leaving rename mode is THIS component's job in both directions —
          // RenameInput never assumes its caller did (see its own comment) — so
          // both callbacks clear the state and only the commit path continues on
          // to the host. Clearing FIRST also means a host that synchronously
          // re-renders us from inside onRenameCommit (a mutation's optimistic
          // cache write, say) can't find the row still in rename mode.
          onRenameCommit={(next) => {
            setRenamingId(null);
            onRenameCommit?.(row.node, next);
          }}
          onRenameCancel={() => setRenamingId(null)}
          indent={indent}
          rowHeight={rowHeight}
          onRowClick={(ev) => handleRowClick(row, ev)}
          onTwistieClick={(ev) => handleTwistieClick(row, ev)}
          onContextMenu={(ev) => handleContextMenu(row, ev)}
          onDragStart={(ev) => handleDragStart(row, ev)}
        />
      ))}
    </div>
  );
}
