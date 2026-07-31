import type { CSSProperties, ReactNode } from "react";
import clsx from "clsx";
import { CaretDown, CaretRight } from "@/components/ui/icons";
import type { TreeAdapter, TreeProps, TreeRowModel, TreeRowState } from "./types";
import { TreeIcon } from "./icon-map";
import { RenameInput } from "./RenameInput";

// The row SHELL: indent guides, the twistie, and (T6b) the drop indicator. Content
// is the caller's choice of tier (plan §"Second consumer", "one provider, two
// renderers") — everything here is tier-agnostic layout, never gRPC-aware
// (enduring decision 1).

// Reserved even on LEAF rows (no caret drawn, but the column still exists) so a
// folder's label and its sibling leaves' labels start at the same x — VS Code
// does this too; without it, leaves would look one indent level to the left of
// their folder siblings.
const TWISTIE_WIDTH = 14;

interface TreeRowProps<T> {
  row: TreeRowModel<T>;
  // T1: a DOM id safe to use as the container's aria-activedescendant target
  // (Tree.tsx's domIdFor — row.id itself is user-authored text that can
  // contain whitespace or worse, unsafe to use as a raw DOM id/IDREF), and the
  // callback ref that lets a keyboard move scrollIntoView the row it just
  // focused. Both are computed and keyed by Tree.tsx (the only place that
  // knows this row's id in the CURRENT flat pass), not here.
  domId: string;
  // T6b: this row's index in the flat array, written out as `data-index`. Drag
  // events are delegated to ONE handler on the container (Tree.tsx), which has to
  // get from the event's target back to a row — and monaco's own list does exactly
  // this, reading a `data-index` attribute while walking up from the target
  // (listView.js's getItemIndexFromEventTarget, :893+). The alternative, reversing
  // `domId` back into a row id, would mean un-doing useId()'s prefix and
  // encodeURIComponent by hand at every dragover.
  dataIndex: number;
  rowRef: (el: HTMLDivElement | null) => void;
  adapter: TreeAdapter<T>;
  renderRow?: TreeProps<T>["renderRow"];
  selected: boolean;
  focused: boolean;
  active: boolean;
  // Driven by Tree.tsx's own internal renaming state (T4b — it was a bridge PROP
  // through T0/T1, and folding it inward is what let the tree take over the edit
  // UI).
  renaming: boolean;
  // T6b. Which side of this row the pointer is currently offering to drop on, and
  // — for the between-rows cases — the DEPTH the dropped rows would land at, which
  // is what the indicator line is indented to. A full-width line cannot say which
  // parent the item lands in; the indent is the whole message. Both come from
  // dnd.ts's resolution, computed once per dragover in Tree.tsx.
  dropTarget: TreeRowState["dropTarget"];
  dropDepth: number;
  // True while this row is part of the set currently being dragged, so it can read
  // as in-flight (reduced opacity, `.treerow.dragging`). Not a TreeRowState field:
  // that object is the ROW CONTENT's contract, and this is shell chrome — the same
  // line drawn for the indent guides and the twistie.
  dragging: boolean;
  // Only meaningful while `renaming`. Computed by Tree.tsx, which is the only
  // place that holds the whole flat pass this row's siblings live in.
  renameSiblings: readonly string[];
  onRenameCommit: (next: string) => void;
  onRenameCancel: () => void;
  indent: number;
  rowHeight: number;
  // T2: Tree.tsx's handleRowClick reads modifier keys off the real event
  // (shiftKey / cmd-or-ctrl) to decide plain-click vs range-extend vs
  // toggle-membership (dispatch.ts's applyRowClick) — so, unlike T0/T1, the
  // event itself has to reach it, not just "a click happened on this row".
  onRowClick: (ev: React.MouseEvent) => void;
  onTwistieClick: (ev: React.MouseEvent) => void;
  onContextMenu: (ev: React.MouseEvent) => void;
  // T6b: only DRAGSTART is per-row — the row is the thing that becomes draggable,
  // and only the row knows which id started the gesture. Every other drag event
  // (dragover/dragleave/drop/dragend) is delegated to the container; see Tree.tsx.
  onDragStart: (ev: React.DragEvent) => void;
}

export function TreeRow<T>({
  row,
  domId,
  dataIndex,
  rowRef,
  adapter,
  renderRow,
  selected,
  focused,
  active,
  renaming,
  dropTarget,
  dropDepth,
  dragging,
  renameSiblings,
  onRenameCommit,
  onRenameCancel,
  indent,
  rowHeight,
  onRowClick,
  onTwistieClick,
  onContextMenu,
  onDragStart,
}: TreeRowProps<T>): ReactNode {
  // Every field of TreeRowState now genuinely varies — `dropTarget` was the last
  // T0 constant here, and T6b is what started varying it. Passing the real shape
  // from the start (rather than a narrower ad-hoc object) is why no rich renderRow
  // has had to change its signature across any of these phases.
  const state: TreeRowState = {
    focused,
    selected,
    active,
    expanded: row.expanded,
    depth: row.depth,
    renaming,
    dropTarget,
  };

  // getTreeItem() is the PORTABLE tier's data source — called for CONTENT only
  // when there is no renderRow. A rich adapter's getTreeItem still has to exist to
  // satisfy TreeAdapter<T> (it isn't optional), but renderRow overrides it per the
  // contract, so a rich adapter is never required to make it MEAN anything.
  // Calling it unconditionally for content would reach into data such an adapter
  // never promised to supply.
  //
  // ONE NARROW EXCEPTION (T4b): the rename branch below reads `.label` off it for
  // EVERY tier, rich included. That is the deliberate call recorded in plan
  // §"What T4b settled" — a label is the one field of TreeItemLike that cannot be
  // meaningless (getTypeaheadLabel would be the only alternative, and it is
  // documented as a SEARCH key, not a display name), so it is the tree's only
  // adapter-independent answer to "what text is this row's name". request-tree.tsx
  // returns the display name there, which is exactly what a rename edits.
  let content: ReactNode;
  let tooltip: string | undefined;
  if (renaming) {
    // The input replaces the WHOLE row content — both tiers, renderRow included,
    // which is why this branch is checked first. Deliberate, minor deviation from
    // VS Code (plan §"Deliberate deviations from VS Code" #5), which keeps the
    // file icon beside its edit box: our rich request rows show a MethodKindTag
    // (U, S←, B⇄) where VS Code shows a file icon, and swapping that tag for the
    // portable tier's generic "file" icon mid-edit would be a stranger visual
    // substitution than simply yielding the row to the input. The row SHELL —
    // indent guides and the twistie column, below — still renders: that chrome is
    // the tree's own, not the content's, and dropping it would make the edited row
    // jump out of the staircase every other row is aligned to.
    content = (
      <RenameInput
        current={adapter.getTreeItem(row.node).label}
        siblings={renameSiblings}
        onCommit={onRenameCommit}
        onCancel={onRenameCancel}
        ariaLabel="New name"
      />
    );
  } else if (renderRow) {
    content = renderRow(row.node, state);
  } else {
    const item = adapter.getTreeItem(row.node);
    tooltip = item.tooltip;
    content = (
      <>
        {item.icon && <TreeIcon token={item.icon} />}
        <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
          {item.label}
        </span>
        {item.description && (
          <span
            className="rowmeta font-mono"
            style={{ fontSize: 10, color: "var(--color-neutral-600)" }}
          >
            {item.description}
          </span>
        )}
      </>
    );
  }

  return (
    <div
      className={clsx(
        "treerow",
        selected && "sel",
        focused && "foc",
        active && "on",
        dragging && "dragging",
        dropTarget === "into" && "dropinto"
      )}
      // id/ref come right after className (not before) so the rendered markup's
      // FIRST attribute stays `class="..."` — Tree.portable.test.tsx and
      // request-tree.test.tsx both scrape rows via a `<div class="treerow...`
      // prefix match; reordering these would silently break that scrape.
      id={domId}
      data-index={dataIndex}
      ref={rowRef}
      // --tree-indent is set here (not left to app-tokens.css's :root default) so
      // the `indent` PROP, not just the CSS token, actually drives the guides'
      // pitch — see the guide-DOM contract in app-tokens.css's components/tree/
      // block. Redundant with the root default when indent===8, which is fine.
      style={{ height: rowHeight, "--tree-indent": `${indent}px` } as CSSProperties}
      role="treeitem"
      aria-level={row.depth + 1}
      // See TreeRowModel.posInSet/setSize (types.ts) for the full citation of
      // the monaco getPosInSet/getSetSize semantics these mirror exactly.
      aria-posinset={row.posInSet}
      aria-setsize={row.setSize}
      // Present only for expandable rows — a leaf has no expansion state to report,
      // matching how a real accessibility tree omits it for files, not just folders.
      aria-expanded={row.expandable ? row.expanded : undefined}
      // Reflects SELECTION, not focus — the tree's logical focus (aria-
      // activedescendant on the container, T1) and selection are deliberately
      // independent (plan §"Focus ≠ selection"), so this must read `selected`
      // even on a row that also happens to be focused right now.
      aria-selected={selected}
      title={tooltip}
      onClick={onRowClick}
      onContextMenu={onContextMenu}
      // NOT draggable while renaming: the row is hosting a text input, where
      // drag-selecting a substring must work and dragging the ROW must not start.
      // This is also where the plan's "reject dragging a row that is mid-rename"
      // rule is actually enforced — refusing it at the source beats letting the
      // gesture start and then rejecting every drop it could make.
      draggable={!renaming}
      onDragStart={onDragStart}
    >
      {/* The between-rows drop indicator (T6b): a real element rather than a
          ::before/::after, because .treerow.on::before is already taken (the open
          tab's left bar) and a row can be both. Rendered only for the between
          cases — an `into` drop paints the whole row instead (.dropinto). --drop-depth
          is what indents it to the destination's depth; see the .dropline rules in
          app-tokens.css. */}
      {(dropTarget === "before" || dropTarget === "after") && (
        <span
          className={clsx("dropline", dropTarget)}
          style={{ "--drop-depth": dropDepth } as CSSProperties}
        />
      )}
      {/* One guide per ancestor level, 0-indexed — none at depth 0. Purely visual;
          --i is read by the .guide rule in app-tokens.css. */}
      {Array.from({ length: row.depth }, (_, i) => (
        <span key={i} className="guide" style={{ "--i": i } as CSSProperties} />
      ))}
      <span
        className="twistie"
        style={{
          width: TWISTIE_WIDTH,
          flex: "none",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          // Pushes the twistie (and everything after it, since margin participates
          // in flex flow) out to depth * indent — the multiple of --tree-indent the
          // guides themselves are drawn at, so the twistie lands directly under its
          // parent's, continuing the staircase rather than cutting across a label.
          marginLeft: row.depth * indent,
        }}
        // No handler at all for a leaf: there is nothing to toggle, so a click here
        // falls through to the row's own onClick exactly like clicking anywhere
        // else on the row (select + focus + onOpen) — not a dead zone.
        onClick={row.expandable ? onTwistieClick : undefined}
      >
        {row.expandable &&
          (row.expanded ? (
            <CaretDown size={11} style={{ color: "var(--color-neutral-500)" }} />
          ) : (
            <CaretRight size={11} style={{ color: "var(--color-neutral-500)" }} />
          ))}
      </span>
      {content}
    </div>
  );
}
