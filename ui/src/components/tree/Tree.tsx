import { useImperativeHandle, type ReactNode } from "react";
import type { TreeAdapter, TreeHandle, TreeProps, TreeRowModel } from "./types";
import { useTreeState } from "./useTreeState";
import { replaceSelection } from "./selection";
import { TreeRow } from "./TreeRow";

// The component itself: roving tabindex (T1), aria, event wiring
// (docs/design/tree-rewrite-plan.md's module table). T0's slice is mouse-only —
// see handleRowClick/handleTwistieClick/handleContextMenu below — plus the
// imperative reveal()/invalidate() handle. Knows nothing about gRPC (enduring
// decision 1): only ./types, ./useTreeState, ./selection and ./TreeRow cross the
// import boundary.

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

export function Tree<T>(props: TreeProps<T>): ReactNode {
  const {
    adapter,
    handle,
    renderRow,
    onOpen,
    onContextMenu,
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

  return (
    <div className="tree" role="tree" aria-label={ariaLabel}>
      {flat.rows.map((row) => (
        <TreeRow
          key={row.id}
          row={row}
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
