import { useState } from "react";
import clsx from "clsx";
import { CaretDown, CaretRight, Folder, Plus, Trash } from "@/components/ui/icons";
import type { ItemWithPath } from "@/lib/format";
import { itemKey } from "@/lib/format";
import { MethodKindTag } from "@/components/ui/Tag";

interface TreeViewProps {
  item: ItemWithPath;
  activeKey: string | null;
  onSelectRequest: (item: ItemWithPath) => void;
  onNewRequestUnder: (folder: ItemWithPath) => void;
  onDelete: (item: ItemWithPath) => void;
}

// TreeView renders one collection node (folder or request) and, for folders, its
// children. Restyled to .treerow (plan §7); rename is omitted (no backend).
export function TreeView({
  item,
  activeKey,
  onSelectRequest,
  onNewRequestUnder,
  onDelete,
}: TreeViewProps) {
  const [open, setOpen] = useState(true);
  const isFolder = item.item.content.case === "folder";
  const children = item.children ?? [];

  if (isFolder) {
    return (
      <div>
        <div className="treerow" onClick={() => setOpen((v) => !v)}>
          {open ? (
            <CaretDown size={11} style={{ color: "var(--color-neutral-500)" }} />
          ) : (
            <CaretRight size={11} style={{ color: "var(--color-neutral-500)" }} />
          )}
          <Folder weight="fill" style={{ color: "var(--color-neutral-500)" }} />
          <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
            {item.item.name}
          </span>
          <span
            className="rowmeta font-mono"
            style={{ fontSize: 10, color: "var(--color-neutral-600)" }}
          >
            {children.length}
          </span>
          <span className="rowbtns">
            <button
              className="rowbtn"
              title="Add request"
              onClick={(e) => {
                e.stopPropagation();
                setOpen(true);
                onNewRequestUnder(item);
              }}
            >
              <Plus size={13} />
            </button>
            <DeleteButton title="Delete folder" onDelete={() => onDelete(item)} />
          </span>
        </div>

        {open && children.length > 0 && (
          <div style={{ marginLeft: 16, borderLeft: "1px solid var(--line)", paddingLeft: 6 }}>
            {children.map((child) => (
              <TreeView
                key={itemKey(child)}
                item={child}
                activeKey={activeKey}
                onSelectRequest={onSelectRequest}
                onNewRequestUnder={onNewRequestUnder}
                onDelete={onDelete}
              />
            ))}
          </div>
        )}
      </div>
    );
  }

  // request row
  const active = itemKey(item) === activeKey;
  return (
    <div
      className={clsx("treerow", active && "on")}
      onClick={() => onSelectRequest(item)}
    >
      <MethodKindTag kind="u" />
      <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
        {item.item.name}
      </span>
      <span className="rowbtns">
        <DeleteButton title="Delete request" onDelete={() => onDelete(item)} />
      </span>
    </div>
  );
}

// DeleteButton is the row's hover-revealed danger action; it swallows the click
// so selecting the row's delete doesn't also open/select the row.
function DeleteButton({ title, onDelete }: { title: string; onDelete: () => void }) {
  return (
    <button
      className="rowbtn danger"
      title={title}
      onClick={(e) => {
        e.stopPropagation();
        onDelete();
      }}
    >
      <Trash size={13} />
    </button>
  );
}
