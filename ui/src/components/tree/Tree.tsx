import { useId, useImperativeHandle, useRef, type ReactNode } from "react";
import type { TreeAdapter, TreeHandle, TreeProps, TreeRowModel } from "./types";
import { useTreeState } from "./useTreeState";
import { replaceSelection } from "./selection";
import { TreeRow } from "./TreeRow";
import { IS_MAC } from "./platform";
import { keyToIntent, type KeyStroke } from "./keymap";
import { targetIndex, parentIndex, firstChildIndex } from "./navigate";

// The component itself: keyboard + aria (T1), event wiring
// (docs/design/tree-rewrite-plan.md's module table). T0 covered mouse-only
// interaction (handleRowClick/handleTwistieClick/handleContextMenu below) plus
// the imperative reveal()/invalidate() handle; T1 adds handleKeyDown below,
// wiring platform.ts + keymap.ts + navigate.ts into real focus movement. Knows
// nothing about gRPC (enduring decision 1): only ./types, ./useTreeState,
// ./selection, ./TreeRow, ./platform, ./keymap and ./navigate cross the import
// boundary.
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
// that means a row's inline rename <input> (EditableName.tsx), the only
// in-row focusable text control that exists yet — rather than the row/
// container surface itself. handleKeyDown below bails out on this rather than
// on `renamingId`: EditableName's own onKeyDown calls preventDefault() for
// Enter/Escape but never stopPropagation(), so every OTHER key typed while
// renaming (Space, Delete, arrows, Home/End, F2, a non-mac Enter, …) bubbles
// straight up through TreeRow's div (which has no onKeyDown of its own) to
// this container's listener and gets reinterpreted by keyToIntent — exactly
// the collision handleRowClick's `row.id === renamingId` guard prevents for
// clicks. A target check catches it more robustly than mirroring that same
// renamingId check would: React batches the state updates EditableName's own
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

export function Tree<T>(props: TreeProps<T>): ReactNode {
  const {
    adapter,
    handle,
    renderRow,
    onOpen,
    onDelete,
    onContextMenu,
    onRenamingChange,
    activeId = null,
    renamingId = null,
    indent = 8,
    rowHeight = 22,
  } = props;
  const ariaLabel = props["aria-label"];
  // compactFolders is part of TreeProps<T> and therefore already accepted by
  // `props` above — it is T7 polish (folder-chain compression) and deliberately
  // does nothing here; there is no T0 behavior to wire it into yet.

  const { flat, expanded, setExpanded, selection, setSelection, focused, setFocused } =
    useTreeState(props);

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
  }));

  const toggleExpanded = (id: string, currentlyExpanded: boolean): void => {
    const next = new Set(expanded);
    if (currentlyExpanded) next.delete(id);
    else next.add(id);
    setExpanded(next);
  };

  // Moves the roving cursor to `id` and keeps it on-screen. Every keyboard
  // move in handleKeyDown below funnels through this — never a bare
  // setFocused — so a keyboard walk can never leave the cursor scrolled out
  // of view. Mouse-driven focus changes (handleRowClick) don't need this: the
  // user just clicked something that was, by definition, already visible.
  const focusRow = (id: string): void => {
    setFocused(id);
    rowEls.current.get(id)?.scrollIntoView({ block: "nearest" });
  };

  // Plain click. A leaf selects + focuses + opens; a folder selects + focuses +
  // toggles expansion (VS Code's single-click-to-toggle behavior on folder rows).
  // Deliberately ignorant of modifier keys and of double-click: cmd/ctrl+click and
  // shift+click are T2 (not pre-empted here — every click is treated as a plain
  // click, full stop), and double-click is a deliberate deviation from VS Code
  // (plan §"Deliberate deviations" #2) — with no onDoubleClick handler at all, a
  // double-click is just two ordinary clicks in a row, i.e. genuinely a no-op
  // beyond what one click already does.
  const handleRowClick = (row: TreeRowModel<T>): void => {
    // A row reporting itself as mid-rename swallows its own click entirely —
    // mirrors the pre-rewrite TreeView.tsx's per-row `editing ? undefined : ...`
    // guard, which this component has to reproduce explicitly now that rename
    // state isn't a per-row useState the row's own onClick prop could just close
    // over (see renamingId's contract comment in types.ts).
    if (row.id === renamingId) return;
    setSelection(replaceSelection(row.id));
    setFocused(row.id);
    if (row.expandable) {
      toggleExpanded(row.id, row.expanded);
    } else {
      onOpen?.(row.node);
    }
  };

  // The twistie is a separate hit target specifically so it can toggle WITHOUT
  // selecting — stopPropagation is what keeps handleRowClick (which DOES select)
  // from also firing for the same click.
  const handleTwistieClick = (row: TreeRowModel<T>, ev: React.MouseEvent): void => {
    ev.stopPropagation();
    toggleExpanded(row.id, row.expanded);
  };

  // Right-click: select the row first if it isn't already selected, then hand off
  // to the caller — no menu UI at T0 (T5). preventDefault so the browser's native
  // menu doesn't show atop whatever the caller does with the callback; nodes are
  // computed from the POST-click selection (not the stale closure value), since
  // setSelection's effect isn't visible until the next render.
  const handleContextMenu = (row: TreeRowModel<T>, ev: React.MouseEvent): void => {
    // No menu wired up (T5 doesn't exist yet, and CollectionPanel passes no
    // onContextMenu today) means truly do nothing — not even preventDefault —
    // so the browser's native context menu still shows, exactly as it did before
    // this component existed at all.
    if (!onContextMenu) return;
    ev.preventDefault();
    const nextSelection = selection.includes(row.id) ? selection : replaceSelection(row.id);
    if (nextSelection !== selection) setSelection(nextSelection);
    const nodes = nextSelection
      .map((id) => flat.rows[flat.indexById.get(id) ?? -1]?.node)
      .filter((n): n is T => n !== undefined);
    onContextMenu?.(nodes, ev);
  };

  // ONE handler for the whole table (docs/design/tree-rewrite-plan.md's "VS
  // Code UX spec"): a KeyStroke built from the real event, keyToIntent
  // (keymap.ts) resolves platform + modifiers to an intent, and this switch is
  // the only place that knows what each intent DOES to real rows/focus/
  // selection — keymap.ts stays a DOM-free table and navigate.ts stays plain
  // array math, per their own header comments. preventDefault fires only once
  // an intent was actually produced: an unclaimed key (a plain letter — T3
  // typeahead's job, not built yet; any modifier combination this table
  // doesn't bind) falls through completely untouched, so e.g. cmd+C or a
  // browser shortcut still works normally while the tree has focus, and an
  // unhandled arrow/Space still scrolls the panel exactly as if this handler
  // didn't exist.
  const handleKeyDown = (ev: React.KeyboardEvent<HTMLDivElement>): void => {
    // A live text control inside a row (today: EditableName's rename <input>)
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

    switch (intent.kind) {
      case "move": {
        // rowsPerPage is MEASURED, never assumed — a hardcoded page size
        // would either page too far (a short panel) or too little (a
        // maximized window) relative to what's actually on screen. Measured
        // off the real scrollport (findScrollport above), not `.tree`'s own
        // clientHeight — `.tree` has no bounded height of its own, so its
        // clientHeight is its full CONTENT height, which would make a page
        // roughly "the whole tree" instead of one screen. Floored, minimum 1
        // (matching navigate.ts's own pageStride guard) so a page move is
        // never a dead key just because nothing has laid out yet (clientHeight
        // reads 0 before first paint) or the panel is shorter than one row.
        const viewportHeight = containerRef.current
          ? findScrollport(containerRef.current).clientHeight
          : 0;
        const rowsPerPage = Math.max(1, Math.floor(viewportHeight / rowHeight));
        const idx = targetIndex(flat, focused, intent.to, rowsPerPage);
        // Deliberately does NOT touch selection: keyboard focus is the roving
        // cursor, and single-select still follows the MOUSE only at T1
        // (decision 4 — focus ≠ selection — is the whole reason these are
        // separate pieces of state; "open" below is the one intent allowed to
        // select, because it is the one real activation).
        if (idx !== null) focusRow(flat.rows[idx].id);
        break;
      }

      case "collapseOrParent":
        if (focusedRow) {
          if (focusedRow.expandable && focusedRow.expanded) {
            toggleExpanded(focusedRow.id, true);
          } else {
            const idx = parentIndex(flat, focusedRow.id);
            if (idx !== null) focusRow(flat.rows[idx].id);
          }
        }
        break;

      case "expandOrFirstChild":
        if (focusedRow) {
          if (focusedRow.expandable && !focusedRow.expanded) {
            toggleExpanded(focusedRow.id, false);
          } else {
            const idx = firstChildIndex(flat, focusedRow.id);
            if (idx !== null) focusRow(flat.rows[idx].id);
          }
        }
        break;

      case "toggle":
        // No selection change — Space only ever toggles expansion. A leaf has
        // nothing to toggle, so this is a no-op on one (matching how the
        // twistie itself is never rendered/wired for a non-expandable row).
        if (focusedRow?.expandable) toggleExpanded(focusedRow.id, focusedRow.expanded);
        break;

      case "open":
        // The one intent that is a real ACTIVATION rather than pure
        // navigation. Deliberately reads as "press Enter/cmd+↓ on this row"
        // the same way handleRowClick already reads "click this row": select
        // it, then act on what kind of row it is. This is a considered
        // reading, not a literal transcription of the phase brief's "onOpen
        // for the focused row, and select it" — VS Code's OWN Enter-on-a-
        // folder toggles expansion (the same thing Space/a plain click
        // already does to a folder), it does not open some folder-shaped
        // editor tab, so this mirrors handleRowClick's fork rather than
        // unconditionally calling onOpen. Unconditionally calling onOpen
        // would hand CollectionPanel's onOpen (ui-store's openTab) a folder
        // ItemWithPath it has never had to handle before, opening a "tab" for
        // a folder — a real, avoidable regression a straight reading here
        // would have introduced.
        if (focusedRow) {
          setSelection(replaceSelection(focusedRow.id));
          if (focusedRow.expandable) {
            toggleExpanded(focusedRow.id, focusedRow.expanded);
          } else {
            onOpen?.(focusedRow.node);
          }
        }
        break;

      case "rename":
        // The component doesn't own the edit UI yet (T4b) — this just NAMES
        // which row, exactly like the pencil button already does (see
        // onRenamingChange's contract comment in types.ts). Whether a given
        // row can actually be renamed (e.g. CollectionPanel says no for a
        // folder row today) is entirely the HOST's call — Tree.tsx has no
        // notion of "folder" at all, so it always fires the callback and
        // trusts the host to no-op when it must.
        if (focusedRow) onRenamingChange?.(focusedRow.id);
        break;

      case "delete":
        // Single-row at T1 — always exactly the focused node, never the
        // whole selection. T2 makes this selection-wide.
        if (focusedRow) onDelete?.([focusedRow.node]);
        break;
    }
  };

  return (
    <div
      ref={containerRef}
      className="tree"
      role="tree"
      aria-label={ariaLabel}
      // The single focusable element (FOCUS MODEL, above) — every row is
      // inert to Tab on purpose, so one Tab reaches the whole tree and a
      // second Tab leaves it, exactly like any other VS Code list widget.
      tabIndex={0}
      aria-activedescendant={focusedRow ? domIdFor(focusedRow.id) : undefined}
      onKeyDown={handleKeyDown}
    >
      {flat.rows.map((row) => (
        <TreeRow
          key={row.id}
          row={row}
          domId={domIdFor(row.id)}
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
          indent={indent}
          rowHeight={rowHeight}
          onRowClick={() => handleRowClick(row)}
          onTwistieClick={(ev) => handleTwistieClick(row, ev)}
          onContextMenu={(ev) => handleContextMenu(row, ev)}
        />
      ))}
    </div>
  );
}
