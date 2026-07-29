import { useState } from "react";
import clsx from "clsx";
import {
  CaretDown,
  CaretRight,
  Folder,
  Gear,
  PencilSimple,
  Plus,
  Trash,
} from "@/components/ui/icons";
import type { Service } from "@grpcview/v1/workspace_pb";
import type { ItemWithPath } from "@/lib/format";
import { itemKey, methodKind, resolveMethod } from "@/lib/format";
import { EditableName } from "@/components/ui/EditableName";
import { MethodKindTag } from "@/components/ui/Tag";

interface TreeViewProps {
  item: ItemWithPath;
  activeKey: string | null;
  // services resolves each request row's method kind for its tag; threaded down
  // the recursion from CollectionPanel (falls back to unary when unresolved).
  services: Service[];
  onSelectRequest: (item: ItemWithPath) => void;
  onNewRequestUnder: (folder: ItemWithPath) => void;
  onRename: (item: ItemWithPath, newName: string) => void;
  onDelete: (item: ItemWithPath) => void;
  // Opens the folder-metadata dialog for this folder row (gv-features-plan.md Feature 1).
  onEditMetadata: (folder: ItemWithPath) => void;
}

// TreeView renders one collection node (folder or request) and, for folders, its
// children. Restyled to .treerow (plan §7). Request rows can be renamed inline via
// the pencil affordance; folder rename is a follow-up (see N2a scope note).
export function TreeView({
  item,
  activeKey,
  services,
  onSelectRequest,
  onNewRequestUnder,
  onRename,
  onDelete,
  onEditMetadata,
}: TreeViewProps) {
  const [open, setOpen] = useState(true);
  const [editing, setEditing] = useState(false);
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
              title="Folder metadata"
              onClick={(e) => {
                e.stopPropagation();
                onEditMetadata(item);
              }}
            >
              <Gear size={13} />
            </button>
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
                services={services}
                onSelectRequest={onSelectRequest}
                onNewRequestUnder={onNewRequestUnder}
                onRename={onRename}
                onDelete={onDelete}
                onEditMetadata={onEditMetadata}
              />
            ))}
          </div>
        )}
      </div>
    );
  }

  // request row
  const active = itemKey(item) === activeKey;
  const request =
    item.item.content.case === "request" ? item.item.content.value : undefined;
  const kind = methodKind(
    resolveMethod(services, request?.service ?? "", request?.method ?? "")
  );
  return (
    <div
      className={clsx("treerow", active && "on")}
      onClick={editing ? undefined : () => onSelectRequest(item)}
    >
      <MethodKindTag kind={kind} />
      <EditableName
        value={item.item.name}
        editing={editing}
        onEditingChange={setEditing}
        onCommit={(next) => onRename(item, next)}
        ariaLabel="Request name"
        style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
      />
      <span className="rowbtns">
        <button
          className="rowbtn"
          title="Rename request"
          onClick={(e) => {
            e.stopPropagation();
            setEditing(true);
          }}
        >
          <PencilSimple size={13} />
        </button>
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
