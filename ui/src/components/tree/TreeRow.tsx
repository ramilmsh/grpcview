import type { CSSProperties, ReactNode } from "react";
import clsx from "clsx";
import { CaretDown, CaretRight } from "@/components/ui/icons";
import type { TreeAdapter, TreeProps, TreeRowModel, TreeRowState } from "./types";
import { TreeIcon } from "./icon-map";

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
  adapter: TreeAdapter<T>;
  renderRow?: TreeProps<T>["renderRow"];
  selected: boolean;
  focused: boolean;
  active: boolean;
  // Driven by Tree's renamingId prop (types.ts) — real from T0, since it doubles
  // as the click-guard for the pencil rename that already exists, not a future
  // T4b-only concern. dropTarget below is the one still-honest T0 constant.
  renaming: boolean;
  indent: number;
  rowHeight: number;
  onRowClick: () => void;
  onTwistieClick: (ev: React.MouseEvent) => void;
  onContextMenu: (ev: React.MouseEvent) => void;
}

export function TreeRow<T>({
  row,
  adapter,
  renderRow,
  selected,
  focused,
  active,
  renaming,
  indent,
  rowHeight,
  onRowClick,
  onTwistieClick,
  onContextMenu,
}: TreeRowProps<T>): ReactNode {
  // dropTarget is the one honest T0 constant left here — T6b is what starts
  // varying it. Passing the real TreeRowState shape now (rather than a narrower
  // ad-hoc object) means a rich renderRow never has to change its signature when
  // that phase lands — only the value it receives starts changing.
  const state: TreeRowState = {
    focused,
    selected,
    active,
    expanded: row.expanded,
    depth: row.depth,
    renaming,
    dropTarget: null,
  };

  // getTreeItem() is the PORTABLE tier's data source — called only when there is no
  // renderRow. A rich adapter's getTreeItem still has to exist to satisfy
  // TreeAdapter<T> (it isn't optional), but renderRow overrides it per the
  // contract, so a rich adapter is never required to make it MEAN anything.
  // Calling it unconditionally here would reach into data such an adapter never
  // promised to supply.
  let content: ReactNode;
  let tooltip: string | undefined;
  if (renderRow) {
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
      className={clsx("treerow", selected && "sel", focused && "foc", active && "on")}
      // --tree-indent is set here (not left to app-tokens.css's :root default) so
      // the `indent` PROP, not just the CSS token, actually drives the guides'
      // pitch — see the guide-DOM contract in app-tokens.css's components/tree/
      // block. Redundant with the root default when indent===8, which is fine.
      style={{ height: rowHeight, "--tree-indent": `${indent}px` } as CSSProperties}
      role="treeitem"
      aria-level={row.depth + 1}
      // Present only for expandable rows — a leaf has no expansion state to report,
      // matching how a real accessibility tree omits it for files, not just folders.
      aria-expanded={row.expandable ? row.expanded : undefined}
      title={tooltip}
      onClick={onRowClick}
      onContextMenu={onContextMenu}
    >
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
