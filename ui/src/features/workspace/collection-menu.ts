// The collection tree's right-click menu items, driven off the current selection. Its own module
// so it is testable: CollectionPanel pulls in `monaco-editor`, unresolvable under the sandboxed test run.
import type { MenuItem } from "@/components/ui/Menu";
import { pruneNestedSelections, type ItemWithPath } from "@/lib/format";
import { deleteConfirmCopy } from "./delete-confirm";

export interface CollectionMenuActions {
  newRequest(parent: ItemWithPath | null): void;
  newFolder(parent: ItemWithPath | null): void;
  newCollection(): void;
  startRename(item: ItemWithPath): void;
  requestDelete(nodes: ItemWithPath[]): void;
  editFolderMetadata(folder: ItemWithPath): void;
}

export function collectionMenuItems(
  // The tree's post-right-click selection, or empty for a click on the panel's empty space.
  nodes: readonly ItemWithPath[],
  actions: CollectionMenuActions,
  opts: { canCreateRequest: boolean },
): MenuItem[] {
  const batch = pruneNestedSelections(nodes);
  const deleteItem: MenuItem = {
    label: deleteConfirmCopy(batch).title,
    danger: true,
    onSelect: () => actions.requestDelete(batch),
  };

  // Empty space, or a collection row (whose node unwraps to no item): the only menu that can
  // offer a WORKSPACE-level act, since every other one describes rows inside one collection.
  if (nodes.length === 0) {
    return [
      {
        label: "New request",
        disabled: !opts.canCreateRequest,
        onSelect: () => actions.newRequest(null),
      },
      { label: "New folder", onSelect: () => actions.newFolder(null) },
      {
        label: "New collection…",
        separatorBefore: true,
        onSelect: () => actions.newCollection(),
      },
    ];
  }

  // Decided on the RAW selection length, not the pruned batch: the user sees N rows
  // highlighted, so the menu should describe N rows.
  if (nodes.length > 1) return [deleteItem];

  const [item] = nodes;
  const folder = item.item.content.case === "folder";

  if (!folder) {
    return [
      { label: "Rename", onSelect: () => actions.startRename(item) },
      deleteItem,
    ];
  }

  return [
    {
      label: "New request",
      disabled: !opts.canCreateRequest,
      onSelect: () => actions.newRequest(item),
    },
    { label: "New folder", onSelect: () => actions.newFolder(item) },
    {
      label: "Folder metadata",
      separatorBefore: true,
      onSelect: () => actions.editFolderMetadata(item),
    },
    {
      label: "Rename",
      separatorBefore: true,
      onSelect: () => actions.startRename(item),
    },
    deleteItem,
  ];
}
