import {
  useId,
  useImperativeHandle,
  useRef,
  useState,
  type ReactNode,
} from "react";
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

// DOM focus never leaves the container; `aria-activedescendant` names the focused row.

// Guards a cyclic adapter.getParent in reveal().
const MAX_REVEAL_DEPTH = 1000;

const NO_SIBLINGS: readonly string[] = [];

function isThenable(value: unknown): value is PromiseLike<unknown> {
  return typeof (value as { then?: unknown }).then === "function";
}

// Walks the whole adapter tree, not just visible rows.
function findNode<T>(adapter: TreeAdapter<T>, id: string): T | undefined {
  const search = (parent: T | undefined): T | undefined => {
    const children = adapter.getChildren(parent);
    if (isThenable(children)) {
      throw new Error(
        "Tree: reveal() walked into an adapter.getChildren() that returned a " +
          "thenable. Like flatten(), this component implements only the " +
          'synchronous TreeDataProvider path (T8, "Async children", is not built ' +
          "yet); silently skipping the branch would make reveal() quietly fail to " +
          "find a real node instead of failing loudly.",
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

// Whether a keydown's target is a live text control, whose keys must not be
// reinterpreted as tree intents.
// eslint-disable-next-line react-refresh/only-export-components
export function isEditableTarget(target: {
  tagName?: string;
  isContentEditable?: boolean;
}): boolean {
  return (
    target.isContentEditable === true ||
    target.tagName === "INPUT" ||
    target.tagName === "TEXTAREA"
  );
}

// On macOS ctrl+click is a right-click gesture, and Firefox delivers it as a
// `click` with button 0 and ctrlKey true.
// eslint-disable-next-line react-refresh/only-export-components
export function isRightClickGesture(
  ev: { button: number; ctrlKey: boolean },
  isMac: boolean,
): boolean {
  return ev.button === 2 || (isMac && ev.ctrlKey);
}

// `.tree` has no height or overflow of its own, so its clientHeight is the full
// content height rather than the visible window.
function findScrollport(el: HTMLElement): HTMLElement {
  for (let node = el.parentElement; node !== null; node = node.parentElement) {
    const overflowY = getComputedStyle(node).overflowY;
    if (overflowY === "auto" || overflowY === "scroll") return node;
  }
  return el;
}

// Carried from dragover to drop, so the drop uses the destination the indicator
// promised even if `flat` has moved on since.
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

  const [renamingId, setRenamingId] = useState<string | null>(null);

  const containerRef = useRef<HTMLDivElement>(null);
  const rowEls = useRef<Map<string, HTMLDivElement>>(new Map());

  // Drag state is state (what paints) mirrored in a ref (what a handler reads):
  // several drag events can fire before React commits.
  const [dragIds, setDragIdsState] = useState<readonly string[] | null>(null);
  const dragIdsRef = useRef<readonly string[] | null>(null);
  const setDragIds = (next: readonly string[] | null): void => {
    dragIdsRef.current = next;
    setDragIdsState(next);
  };
  const [dropState, setDropStateValue] = useState<DropState | null>(null);
  const dropStateRef = useRef<DropState | null>(null);
  // `dragover` fires on every pointer move and, per the HTML drag-and-drop model,
  // at least every 350ms even for a stationary pointer.
  const setDropState = (next: DropState | null): void => {
    const current = dropStateRef.current;
    if (current === null && next === null) return;
    if (
      current !== null &&
      next !== null &&
      current.rowId === next.rowId &&
      current.zone === next.zone
    ) {
      return;
    }
    dropStateRef.current = next;
    setDropStateValue(next);
  };

  // row.id is user-authored text, and an ARIA IDREF may not contain whitespace.
  const treeId = useId();
  const domIdFor = (id: string): string => `${treeId}${encodeURIComponent(id)}`;

  const focusedRow: TreeRowModel<T> | null =
    focused === null
      ? null
      : (flat.rows[flat.indexById.get(focused) ?? -1] ?? null);

  useImperativeHandle(handle, (): TreeHandle<T> => ({
    reveal(id, opts) {
      const target = findNode(adapter, id);
      if (target === undefined) return;

      const toExpand = new Set<string>();
      const getParent = adapter.getParent;
      if (getParent) {
        const seen = new Set<string>([id]);
        let current = target;
        for (let hops = 0; hops < MAX_REVEAL_DEPTH; hops++) {
          const parent = getParent(current);
          if (parent === undefined) break;
          const parentId = adapter.getId(parent);
          if (seen.has(parentId)) break;
          seen.add(parentId);
          toExpand.add(parentId);
          current = parent;
        }
      }
      // opts.expand opens the revealed node itself, not just its ancestors.
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
      // No async children cache to invalidate yet.
    },
    startRename(id) {
      if (!flat.indexById.has(id)) return;
      setRenamingId(id);
    },
  }));

  // The scroll is conditional because scrollIntoView({block: "nearest"}) is not a
  // no-op for a partially clipped row: on a click it yanks the list under the pointer.
  const focusRow = (id: string, scroll: boolean): void => {
    setFocused(id);
    if (scroll) rowEls.current.get(id)?.scrollIntoView({ block: "nearest" });
  };

  const nodeFor = (id: string): T | undefined =>
    flat.rows[flat.indexById.get(id) ?? -1]?.node;

  const siblingLabelsFor = (row: TreeRowModel<T>): string[] =>
    flat.rows
      .filter((r) => r.parentId === row.parentId && r.id !== row.id)
      .map((r) => adapter.getTreeItem(r.node).label);

  const applyActions = (actions: readonly TreeAction[]): void => {
    // Expansion is folded and written once: two setExpanded calls would each derive
    // `new Set(expanded)` from this render's closure, losing the first.
    let nextExpanded: Set<string> | null = null;

    for (const action of actions) {
      switch (action.kind) {
        case "focus":
          focusRow(action.id, action.scroll);
          break;
        case "setExpanded": {
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
          setRenamingId(action.id);
          break;
        case "delete": {
          const nodes = action.ids
            .map(nodeFor)
            .filter((n): n is T => n !== undefined);
          onDelete?.(nodes);
          break;
        }
      }
    }

    // `null` is "nothing touched expansion", not "expand nothing".
    if (nextExpanded !== null) setExpanded(nextExpanded);
  };

  const handleRowClick = (row: TreeRowModel<T>, ev: React.MouseEvent): void => {
    // A row mid-rename swallows its own click, modified or not.
    if (row.id === renamingId) return;

    const mods: ClickMods = {
      shiftKey: ev.shiftKey,
      modKey: IS_MAC ? ev.metaKey : ev.ctrlKey,
      rightButton: isRightClickGesture(ev, IS_MAC),
    };
    applyActions(
      applyRowClick(row, mods, { flat, focused, selection, anchor }),
    );
  };

  const handleTwistieClick = (
    row: TreeRowModel<T>,
    ev: React.MouseEvent,
  ): void => {
    // stopPropagation is what keeps handleRowClick, which selects, from also firing.
    ev.stopPropagation();
    applyActions(applyTwistieClick(row, { flat, focused, selection, anchor }));
  };

  const handleContextMenu = (
    row: TreeRowModel<T>,
    ev: React.MouseEvent,
  ): void => {
    // No host menu: do nothing at all, so the browser's own menu still shows.
    if (!onContextMenu) return;
    // Mid-rename, the native menu is the only way to paste into the input.
    if (isEditableTarget(ev.target as HTMLElement)) return;
    ev.preventDefault();
    const nextSelection = selection.includes(row.id)
      ? selection
      : replaceSelection(row.id);
    if (nextSelection !== selection) {
      setSelection(nextSelection);
      setAnchor(row.id);
    }
    focusRow(row.id, false);
    const nodes = nextSelection
      .map(nodeFor)
      .filter((n): n is T => n !== undefined);
    onContextMenu(nodes, ev);
  };

  // Native HTML5 drag and drop. Every event except `dragstart` is delegated to the
  // container: with per-row handlers, `dragleave` on the old row fires before
  // `dragover` on the new one, blanking the indicator between every pair of rows.

  // In row order: a multi-row move fires one MoveItem per node with the same
  // `before`, so firing order is the resulting sibling order.
  const draggedNodes = (ids: readonly string[]): T[] =>
    [...ids]
      .filter((id) => flat.indexById.has(id))
      .sort(
        (a, b) => (flat.indexById.get(a) ?? 0) - (flat.indexById.get(b) ?? 0),
      )
      .map((id) => flat.rows[flat.indexById.get(id) ?? -1].node);

  const destinationFor = (
    res: DropResolution,
  ): { parent: T | null; before?: T } | null => {
    let parent: T | null = null;
    if (res.parentId !== null) {
      const node = nodeFor(res.parentId);
      if (node === undefined) return null;
      parent = node;
    }
    if (res.beforeId === null) return { parent };
    const before = nodeFor(res.beforeId);
    // A vanished `before` degrades to append, as MoveItemRequest does server-side.
    return before === undefined ? { parent } : { parent, before };
  };

  const endDrag = (): void => {
    setDragIds(null);
    setDropState(null);
  };

  const handleDragStart = (row: TreeRowModel<T>, ev: React.DragEvent): void => {
    // The whole selection travels if the gesture started on a selected row,
    // otherwise just this row, which then replaces the selection.
    let ids: readonly string[];
    if (selection.includes(row.id)) {
      ids = selection.filter((id) => flat.indexById.has(id));
    } else {
      ids = replaceSelection(row.id);
      setSelection(ids);
      setAnchor(row.id);
    }
    setDragIds(ids);

    // Written only because a drag with an empty dataTransfer is cancelled in some
    // browsers, and never read back: getData is unreadable during `dragover`, which
    // is exactly when a drop has to be judged. The payload lives in dragIdsRef.
    ev.dataTransfer.effectAllowed = "move";
    ev.dataTransfer.setData(
      "text/plain",
      draggedNodes(ids)
        .map((node) => adapter.getTreeItem(node).label)
        .join("\n"),
    );
  };

  // The event target is whatever descendant the pointer is over, so walk up to the
  // nearest `data-index` carrier.
  const rowElementFor = (
    ev: React.DragEvent,
  ): { index: number; el: HTMLElement } | null => {
    const container = containerRef.current;
    const target = ev.target;
    if (container === null || !(target instanceof HTMLElement)) return null;
    const el = target.closest<HTMLElement>("[data-index]");
    if (el === null || !container.contains(el)) return null;
    const index = Number(el.dataset.index);
    if (!Number.isInteger(index) || index < 0 || index >= flat.rows.length)
      return null;
    return { index, el };
  };

  const autoScroll = (pointerY: number): void => {
    const container = containerRef.current;
    if (container === null) return;
    const port = findScrollport(container);
    const rect = port.getBoundingClientRect();
    const delta = autoScrollDelta(pointerY, rect.top, rect.bottom);
    if (delta !== 0) port.scrollTop += delta;
  };

  const handleDragOver = (ev: React.DragEvent<HTMLDivElement>): void => {
    // A drag that did not start in this tree gets no preventDefault, so the browser
    // shows its own no-drop cursor.
    const ids = dragIdsRef.current;
    if (ids === null) return;

    // Before the target check, so autoscroll works over a gap or past the last row.
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
      // The measured height: the geometry must be relative to the real box.
      rowHeight: rect.height,
      expandable: row.expandable,
    });
    const res = dropTargetAt(flat, hit.index, zone, ids);
    if (res === null) {
      setDropState(null);
      return;
    }
    // The host's veto, for what the tree cannot see (children collapsed or filtered
    // out of `flat`).
    const to = destinationFor(res);
    if (to === null || (canDrop && !canDrop(draggedNodes(ids), to))) {
      setDropState(null);
      return;
    }

    // preventDefault is what makes a drop possible at all, and only happens here on
    // the accepted path.
    ev.preventDefault();
    ev.dataTransfer.dropEffect = "move";
    setDropState({ rowId: row.id, zone, res });
  };

  // `relatedTarget` distinguishes leaving the tree from crossing our own descendants.
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

  // Fires for every drag that ends, dropped or cancelled — the one guaranteed teardown.
  const handleDragEnd = (): void => endDrag();

  const handleKeyDown = (ev: React.KeyboardEvent<HTMLDivElement>): void => {
    // A target check, not a `renamingId` one: React has not yet committed the state
    // updates RenameInput's own handler queued.
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
    // Only for a claimed key, so an unbound one keeps its browser default.
    ev.preventDefault();

    const viewportHeight = containerRef.current
      ? findScrollport(containerRef.current).clientHeight
      : 0;
    const rowsPerPage = Math.max(1, Math.floor(viewportHeight / rowHeight));

    const actions = applyIntent(intent, {
      flat,
      focused,
      selection,
      anchor,
      rowsPerPage,
    });
    applyActions(actions);
  };

  return (
    <div
      ref={containerRef}
      className="tree"
      role="tree"
      aria-label={ariaLabel}
      // Without this, ARIA defines a `tree` as single-select.
      aria-multiselectable="true"
      tabIndex={0}
      aria-activedescendant={focusedRow ? domIdFor(focusedRow.id) : undefined}
      onKeyDown={handleKeyDown}
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
          renameSiblings={
            row.id === renamingId ? siblingLabelsFor(row) : NO_SIBLINGS
          }
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
