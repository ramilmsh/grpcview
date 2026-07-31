import { useMemo, type ReactNode } from "react";
import { Folder, Gear, PencilSimple, Plus, Trash } from "@/components/ui/icons";
import type { Service } from "@grpcview/v1/workspace_pb";
import type { TreeAdapter, TreeRowState } from "@/components/tree/types";
import { EditableName } from "@/components/ui/EditableName";
import { MethodKindTag } from "@/components/ui/Tag";
import { itemKey, methodKind, resolveMethod, type ItemWithPath } from "@/lib/format";

// The RICH-tier adapter + row renderer over ItemWithPath (tree-rewrite-plan.md
// §"Second consumer": "one provider, two renderers"). Lives beside its only
// caller, CollectionPanel, not in lib/tree-providers/ — that directory is
// reserved for framework-free portable providers, and this one imports React,
// this app's icon set, and gRPC-shaped types, so it is neither portable nor
// reusable by a future VS Code renderer (the request tree stays standalone-only
// per the plan: in plugin mode the collection is a directory of files, so VS
// Code's own file explorer takes over and there is nothing to port).

// Enumerated per the plan's "portability rot" risk (§Risks) — even though this
// provider is never meant to be portable, an ad-hoc string here would still be
// exactly the kind of drift that risk warns about.
export type RequestTreeKind = "folder" | "request";

// One id -> parent lookup built per adapter construction, which is what makes
// getParent (and so Tree's reveal()) work without every node carrying a back
// pointer of its own.
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

export function requestTreeAdapter(roots: ItemWithPath[]): TreeAdapter<ItemWithPath> {
  const parentOf = buildParentIndex(roots);

  return {
    getId: itemKey,
    getChildren: (node) => (node ? node.children ?? [] : roots),
    // Folders default OPEN (matching today's per-row `useState(true)`); requests
    // are leaves. useTreeState's default-expansion seeding is what turns this into
    // actually-expanded state on first render — see its own comment for how a
    // user's later collapse is never second-guessed back open.
    getCollapsibleState: (node) => (node.item.content.case === "folder" ? "expanded" : "none"),
    getParent: (node) => parentOf.get(itemKey(node)),
    getTreeItem: (node) => {
      const folder = node.item.content.case === "folder";
      const kind: RequestTreeKind = folder ? "folder" : "request";
      return {
        label: node.item.name,
        description: folder ? String(node.children?.length ?? 0) : undefined,
        icon: folder ? "folder" : "file",
        kind,
      };
    },
    getTypeaheadLabel: (node) => node.item.name,
  };
}

// Memoized over the (already-filtered) roots CollectionPanel passes in — a fresh
// adapter object every render would force useTreeState's `useMemo(() =>
// flatten(...), [adapter, expanded])` to re-flatten on every keystroke elsewhere
// in the app, not just when the tree's own data actually changed.
export function useRequestTreeAdapter(roots: ItemWithPath[]): TreeAdapter<ItemWithPath> {
  return useMemo(() => requestTreeAdapter(roots), [roots]);
}

// Callbacks renderRequestRow needs that aren't reachable from ItemWithPath alone —
// CollectionPanel owns the mutations/dialogs these trigger; this module only knows
// how to lay out a row and call back into them.
export interface RequestRowCallbacks {
  // Resolves a request row's method-kind tag; folder rows never read this.
  services: Service[];
  // Single "one row renames at a time" key (T4b is full F2/keyboard rename; T0
  // keeps only today's pencil -> EditableName behavior working). Lives in
  // CollectionPanel, not per-row state, since only one row can be mid-rename.
  renamingKey: string | null;
  onRenamingChange: (key: string | null) => void;
  onRename: (item: ItemWithPath, next: string) => void;
  onNewRequestUnder: (folder: ItemWithPath) => void;
  onDelete: (item: ItemWithPath) => void;
  onEditMetadata: (folder: ItemWithPath) => void;
}

// renderRequestRow is the RICH-tier row content (TreeRow.tsx's `content`, not the
// row's own shell — indent guides/twistie/selection styling are the tree
// component's, per plan §"Enduring decisions" #1/#2). `state` carries
// focused/selected/active/etc., but nothing here varies its OWN look by any of
// them (mirrors the pre-rewrite tree, whose row content never did either — only
// the row's shell className did, via `.on`/`.sel`/`.foc`).
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
          <RowDeleteButton title="Delete folder" onDelete={() => cb.onDelete(item)} />
        </span>
      </>
    );
  }

  const key = itemKey(item);
  const editing = cb.renamingKey === key;
  const request = item.item.content.case === "request" ? item.item.content.value : undefined;
  const kind = methodKind(resolveMethod(cb.services, request?.service ?? "", request?.method ?? ""));
  return (
    <>
      <MethodKindTag kind={kind} />
      <EditableName
        value={item.item.name}
        editing={editing}
        onEditingChange={(next) => cb.onRenamingChange(next ? key : null)}
        onCommit={(next) => cb.onRename(item, next)}
        ariaLabel="Request name"
        style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
      />
      <span className="rowbtns">
        <button
          className="rowbtn"
          title="Rename request"
          onClick={(e) => {
            e.stopPropagation();
            cb.onRenamingChange(key);
          }}
        >
          <PencilSimple size={13} />
        </button>
        <RowDeleteButton title="Delete request" onDelete={() => cb.onDelete(item)} />
      </span>
    </>
  );
}

// The row's hover-revealed danger action; swallows the click so selecting the
// row's delete doesn't also select/open/toggle the row itself.
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
