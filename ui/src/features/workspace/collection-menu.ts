// The collection tree's right-click menu ITEMS (docs/design/tree-rewrite-plan.md's
// T5: "New Request / New Folder / Rename / Delete / Folder metadata, driven off
// the current selection"). Only the item list lives here; the popup itself is
// components/ui/Menu.tsx and the handlers are CollectionPanel's own.
//
// Its own module, beside delete-confirm.ts and for the identical reason that file
// documents at length: CollectionPanel.tsx transitively imports
// FolderMetadataDialog -> MetadataEditor -> `monaco-editor`, which cannot be
// resolved at all under this repo's Bazel-sandboxed vitest run — so anything in
// this file that is to be unit-tested has to sit outside CollectionPanel's module
// graph. And this genuinely needs testing: "driven off the current selection" is
// the whole substance of the phase's spec line, it is pure data (a selection in,
// a list of labels/flags out), and vitest here has no jsdom to click a real menu
// with. `MenuItem` is imported as a TYPE, so nothing in components/ui/ (or React)
// is pulled in at runtime.
import type { MenuItem } from "@/components/ui/Menu";
import { pruneNestedSelections, type ItemWithPath } from "@/lib/format";
import { deleteConfirmCopy } from "./delete-confirm";

// Every action a menu item can trigger. Each one already exists in
// CollectionPanel as the handler behind either a header button or a row's own
// hover-revealed button — the menu is an ADDITIONAL path to the same handlers,
// exactly as F2 and the row pencil are two paths to one rename. `null` as a
// parent means the collection root.
export interface CollectionMenuActions {
  newRequest(parent: ItemWithPath | null): void;
  newFolder(parent: ItemWithPath | null): void;
  startRename(item: ItemWithPath): void;
  // Takes the whole batch: CollectionPanel's onTreeDelete opens ONE confirm
  // dialog for a list, which is the same flow the keyboard Delete uses.
  requestDelete(nodes: ItemWithPath[]): void;
  editFolderMetadata(folder: ItemWithPath): void;
}

export function collectionMenuItems(
  // The tree's post-right-click selection, verbatim (Tree.tsx's onContextMenu),
  // or EMPTY for a right-click on the panel's empty space below the last row.
  nodes: readonly ItemWithPath[],
  actions: CollectionMenuActions,
  // Whether a request can be created at all — false when no definition source
  // has produced any services yet, mirroring the header's own
  // `disabled={services.length === 0}` on the + button. Disabled rather than
  // omitted so the menu still says the action exists and is merely unavailable.
  opts: { canCreateRequest: boolean }
): MenuItem[] {
  // The DELETE batch, pruned once here: pruneNestedSelections drops any item an
  // ancestor in the same batch already covers, which is what keeps the label's
  // count honest ("Delete folder", not "Delete 4 items", when three of the four
  // rows are inside the fourth) — the same reasoning its own comment gives for
  // the confirm dialog, applied one step earlier so the menu and the dialog it
  // opens cannot disagree. The pruned list is also what gets handed to
  // requestDelete, so there is one list, not a label built from one and an action
  // firing on another.
  const batch = pruneNestedSelections(nodes);
  // Reuses the confirm dialog's own pluralizer rather than writing a second one:
  // its `title` is already exactly a menu-item label ("Delete request", "Delete
  // folder", "Delete 3 items", "Delete 2 folders"). Two pluralizers for one
  // concept is how the menu and the dialog it opens start describing the same
  // batch differently.
  const deleteItem: MenuItem = {
    label: deleteConfirmCopy(batch).title,
    danger: true,
    onSelect: () => actions.requestDelete(batch),
  };

  // Right-click on empty space: the collection ROOT's own creation actions, and
  // nothing else — there is no target to rename, delete, or edit metadata for.
  if (nodes.length === 0) {
    return [
      {
        label: "New request",
        disabled: !opts.canCreateRequest,
        onSelect: () => actions.newRequest(null),
      },
      { label: "New folder", onSelect: () => actions.newFolder(null) },
    ];
  }

  // A MULTI-row selection offers DELETE ONLY, and the other items are OMITTED
  // rather than shown greyed out. Delete is the one action here that is
  // genuinely batch-capable (CollectionPanel's doDelete already loops, and the
  // confirm copy already pluralizes); rename, both creates, and folder metadata
  // are all single-target and would need an arbitrary rule for which of the
  // selected rows they meant. Omitted because a menu whose every row but one is
  // greyed is noise rather than information — the greyed-out affordance earns its
  // place for "New request" above, where the action IS available in principle and
  // is blocked by a fixable condition (add a definition source), which is not
  // true of "rename these four rows".
  //
  // Decided on the RAW selection length, not the pruned batch: the user sees N
  // rows highlighted, so the menu should describe N rows. Pruning is a
  // delete-time honesty measure about redundant operations, not a claim that the
  // other rows are unselected.
  if (nodes.length > 1) return [deleteItem];

  const [item] = nodes;
  const folder = item.item.content.case === "folder";

  // A REQUEST row: the two actions that apply to it. Deliberately no "New
  // request"/"New folder" creating a SIBLING in the request's parent folder, which
  // is what VS Code's explorer offers on a file row — a knowing narrowing of the
  // phase's spec line ("on a folder row, and at the root"), because a create
  // action whose target is the clicked row's invisible parent is the one item in
  // this menu whose destination the user cannot see. The row's parent folder is
  // one right-click away.
  if (!folder) {
    return [{ label: "Rename", onSelect: () => actions.startRename(item) }, deleteItem];
  }

  // A FOLDER row. Grouped the way VS Code's explorer groups a folder's menu —
  // creation first, then the folder's own properties, with rename/delete last so
  // the destructive item is never adjacent to a create action the user was
  // aiming at.
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
    { label: "Rename", separatorBefore: true, onSelect: () => actions.startRename(item) },
    deleteItem,
  ];
}
