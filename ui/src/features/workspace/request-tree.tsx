import { useMemo, type ReactNode } from "react";
import { Folder, Gear, PencilSimple, Plus, Trash } from "@/components/ui/icons";
import type { Service } from "@grpcview/v1/workspace_pb";
import type { TreeAdapter, TreeItemLike, TreeRowState } from "@/components/tree/types";
import { MethodKindTag } from "@/components/ui/Tag";
import { itemKey, methodKind, resolveMethod, type ItemWithPath } from "@/lib/format";

export type RequestTreeKind = "folder" | "request";

function buildParentIndex(roots: ItemWithPath[]): Map<string, ItemWithPath> {
  const parentOf = new Map<string, ItemWithPath>();
  const walk = (items: ItemWithPath[], parent: ItemWithPath | undefined): void => {
    for (const item of items) {
      if (parent) parentOf.set(itemKey(item), parent);
      if (item.children) walk(item.children, item);
    }
  };
  walk(roots, undefined);
  return parentOf;
}

// The PORTABLE description of an item row, shared with the panel tree's item tier
// (panel-tree.tsx) so there is one answer per item, not two that can drift.
export function requestTreeItem(node: ItemWithPath): TreeItemLike {
  const folder = node.item.content.case === "folder";
  const kind: RequestTreeKind = folder ? "folder" : "request";
  return {
    label: node.item.name,
    description: folder ? String(node.children?.length ?? 0) : undefined,
    icon: folder ? "folder" : "file",
    kind,
  };
}

export function requestTreeAdapter(roots: ItemWithPath[]): TreeAdapter<ItemWithPath> {
  const parentOf = buildParentIndex(roots);

  return {
    getId: itemKey,
    getChildren: (node) => (node ? node.children ?? [] : roots),
    getCollapsibleState: (node) => (node.item.content.case === "folder" ? "expanded" : "none"),
    getParent: (node) => parentOf.get(itemKey(node)),
    getTreeItem: requestTreeItem,
    getTypeaheadLabel: (node) => node.item.name,
  };
}

export function useRequestTreeAdapter(roots: ItemWithPath[]): TreeAdapter<ItemWithPath> {
  return useMemo(() => requestTreeAdapter(roots), [roots]);
}

export interface RequestRowCallbacks {
  services: Service[];
  onStartRename: (item: ItemWithPath) => void;
  onNewRequestUnder: (folder: ItemWithPath) => void;
  onDelete: (item: ItemWithPath) => void;
  onEditMetadata: (folder: ItemWithPath) => void;
}

// renderRequestRow is the row CONTENT only; the row shell is the tree component's.
export function renderRequestRow(
  item: ItemWithPath,
  _state: TreeRowState,
  cb: RequestRowCallbacks
): ReactNode {
  if (item.item.content.case === "folder") {
    const count = item.children?.length ?? 0;
    return (
      <>
        <Folder weight="fill" style={{ color: "var(--color-neutral-500)" }} />
        <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
          {item.item.name}
        </span>
        <span className="rowmeta font-mono" style={{ fontSize: 10, color: "var(--color-neutral-600)" }}>
          {count}
        </span>
        <span className="rowbtns">
          <button
            className="rowbtn"
            title="Folder metadata"
            onClick={(e) => {
              e.stopPropagation();
              cb.onEditMetadata(item);
            }}
          >
            <Gear size={13} />
          </button>
          <button
            className="rowbtn"
            title="Add request"
            onClick={(e) => {
              e.stopPropagation();
              cb.onNewRequestUnder(item);
            }}
          >
            <Plus size={13} />
          </button>
          <RowRenameButton title="Rename folder" onStartRename={() => cb.onStartRename(item)} />
          <RowDeleteButton title="Delete folder" onDelete={() => cb.onDelete(item)} />
        </span>
      </>
    );
  }

  const request = item.item.content.case === "request" ? item.item.content.value : undefined;
  const kind = methodKind(resolveMethod(cb.services, request?.service ?? "", request?.method ?? ""));
  return (
    <>
      <MethodKindTag kind={kind} />
      <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
        {item.item.name}
      </span>
      <span className="rowbtns">
        <RowRenameButton title="Rename request" onStartRename={() => cb.onStartRename(item)} />
        <RowDeleteButton title="Delete request" onDelete={() => cb.onDelete(item)} />
      </span>
    </>
  );
}

function RowRenameButton({ title, onStartRename }: { title: string; onStartRename: () => void }) {
  return (
    <button
      className="rowbtn"
      title={title}
      onClick={(e) => {
        e.stopPropagation();
        onStartRename();
      }}
    >
      <PencilSimple size={13} />
    </button>
  );
}

function RowDeleteButton({ title, onDelete }: { title: string; onDelete: () => void }) {
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
