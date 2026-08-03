import type { CSSProperties, ReactNode } from "react";
import clsx from "clsx";
import { CaretDown, CaretRight } from "@/components/ui/icons";
import type { TreeAdapter, TreeProps, TreeRowModel, TreeRowState } from "./types";
import { TreeIcon } from "./icon-map";
import { RenameInput } from "./RenameInput";

// The row shell: indent guides, twistie, drop indicator.

// Reserved on leaf rows too, so folder and leaf labels start at the same x.
const TWISTIE_WIDTH = 14;

interface TreeRowProps<T> {
  row: TreeRowModel<T>;
  // Safe as the container's aria-activedescendant target, unlike row.id itself.
  domId: string;
  // Read back off the DOM by the container's delegated drag handlers.
  dataIndex: number;
  rowRef: (el: HTMLDivElement | null) => void;
  adapter: TreeAdapter<T>;
  renderRow?: TreeProps<T>["renderRow"];
  selected: boolean;
  focused: boolean;
  active: boolean;
  renaming: boolean;
  dropTarget: TreeRowState["dropTarget"];
  // The depth the between-rows indicator line is indented to.
  dropDepth: number;
  dragging: boolean;
  renameSiblings: readonly string[];
  onRenameCommit: (next: string) => void;
  onRenameCancel: () => void;
  indent: number;
  rowHeight: number;
  onRowClick: (ev: React.MouseEvent) => void;
  onTwistieClick: (ev: React.MouseEvent) => void;
  onContextMenu: (ev: React.MouseEvent) => void;
  // Only dragstart is per-row; the rest are delegated to the container.
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
  const state: TreeRowState = {
    focused,
    selected,
    active,
    expanded: row.expanded,
    depth: row.depth,
    renaming,
    dropTarget,
  };

  let content: ReactNode;
  let tooltip: string | undefined;
  if (renaming) {
    // The input replaces the whole row content, renderRow included — hence first.
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
      // className must stay the FIRST rendered attribute: Tree.portable.test.tsx
      // and request-tree.test.tsx scrape rows by a `<div class="treerow...` prefix.
      id={domId}
      data-index={dataIndex}
      ref={rowRef}
      style={{ height: rowHeight, "--tree-indent": `${indent}px` } as CSSProperties}
      role="treeitem"
      aria-level={row.depth + 1}
      aria-posinset={row.posInSet}
      aria-setsize={row.setSize}
      aria-expanded={row.expandable ? row.expanded : undefined}
      // Reflects SELECTION, not focus — the two are independent here.
      aria-selected={selected}
      title={tooltip}
      onClick={onRowClick}
      onContextMenu={onContextMenu}
      // Not draggable while renaming: drag-selecting inside the input must work.
      draggable={!renaming}
      onDragStart={onDragStart}
    >
      {/* A real element, not a ::before: .treerow.on::before is already taken and a
          row can be both. --drop-depth is read by the .dropline rules. */}
      {(dropTarget === "before" || dropTarget === "after") && (
        <span
          className={clsx("dropline", dropTarget)}
          style={{ "--drop-depth": dropDepth } as CSSProperties}
        />
      )}
      {/* --i is read by the .guide rule in app-tokens.css. */}
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
          marginLeft: row.depth * indent,
        }}
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
