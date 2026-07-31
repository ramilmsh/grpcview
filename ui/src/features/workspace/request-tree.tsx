import { useMemo, type ReactNode } from "react";
import { Folder, Gear, PencilSimple, Plus, Trash } from "@/components/ui/icons";
import type { Service } from "@grpcview/v1/workspace_pb";
import type { TreeAdapter, TreeRowState } from "@/components/tree/types";
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
  // The pencil button's whole job as of T4b: ASK the tree to start renaming this
  // row (CollectionPanel routes it to TreeHandle.startRename). There is no
  // renamingKey/onRenamingChange/onRename trio any more — the tree owns the edit
  // box, the validation and the commit, so a row renderer never renders name-edit
  // UI of its own and never hears about the committed value. Both row kinds get
  // one: folders became renamable at T4a.
  onStartRename: (item: ItemWithPath) => void;
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
          {/* Renamable as of T4a (UpdateFolderRequest.name). Placed immediately
              before the trash, matching the request row's ordering habit below. */}
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
      {/* A plain span, not EditableName: the tree renders the rename box itself
          (components/tree/RenameInput.tsx) and swaps out this whole row content
          while it does, so a row renderer has no edit state left to hold. Same
          ellipsis styling as the folder row's label above. */}
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

// The row's hover-revealed rename affordance, shared by both row kinds (T4b).
// Swallows the click for the same reason RowDeleteButton does: without it the
// click also selects/opens/toggles the row underneath — and for a folder that
// would collapse the very row about to be renamed.
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
