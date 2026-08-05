import { useMemo, useRef, useState, type ReactNode } from "react";
import { MagnifyingGlass, FolderPlus, Plus } from "@/components/ui/icons";
import { ScriptKind, type Service, type Method } from "@grpcview/v1/workspace_pb";
import { IconButton, Button } from "@/components/ui/Button";
import { Dialog } from "@/components/ui/Dialog";
import { Input } from "@/components/ui/Input";
import { Menu } from "@/components/ui/Menu";
import { Tree, isEditableTarget } from "@/components/tree/Tree";
import type { TreeHandle, TreeRowState } from "@/components/tree/types";
import { useActiveWorkspace, useRootItems, useWorkspaceMutations } from "@/lib/workspace-query";
import { useUIStore } from "@/lib/ui-store";
import {
  childPathOf,
  findByKey,
  itemKey,
  pruneNestedSelections,
  serviceName,
  slugKeyIn,
  type ItemWithPath,
} from "@/lib/format";
import { renderRequestRow, useRequestTreeAdapter, type RequestRowCallbacks } from "./request-tree";
import { MethodPickerModal } from "./MethodPickerModal";
import { FolderMetadataDialog } from "./FolderMetadataDialog";
import type { GeneratorDef } from "./generator-libs";
import { deleteConfirmCopy } from "./delete-confirm";
import { collectionMenuItems, type CollectionMenuActions } from "./collection-menu";

const countRequests = (items: ItemWithPath[]): number =>
  items.reduce(
    (n, it) =>
      it.item.content.case === "folder"
        ? n + countRequests(it.children ?? [])
        : n + 1,
    0
  );

const filterTree = (items: ItemWithPath[], q: string): ItemWithPath[] => {
  if (!q) return items;
  const lower = q.toLowerCase();
  const walk = (list: ItemWithPath[]): ItemWithPath[] => {
    const out: ItemWithPath[] = [];
    for (const it of list) {
      if (it.item.content.case === "folder") {
        const kids = walk(it.children ?? []);
        if (kids.length || it.item.name.toLowerCase().includes(lower)) {
          out.push({ ...it, children: kids });
        }
      } else if (it.item.name.toLowerCase().includes(lower)) {
        out.push(it);
      }
    }
    return out;
  };
  return walk(items);
};

export function CollectionPanel() {
  const { collection: activeCollection, workspace, services } = useActiveWorkspace();
  // Non-null everywhere this panel renders: App gates all three views behind the
  // collection listing, so "" is the unreachable pre-gate value rather than a default.
  const collection = activeCollection ?? "";
  const rootItems = useRootItems(workspace);
  const { createFolder, createRequest, deleteRequest, updateRequest, updateFolder, moveItem } =
    useWorkspaceMutations();
  const openTab = useUIStore((s) => s.openTab);
  const activeKey = useUIStore((s) => s.activeKey);
  const moveSubtree = useUIStore((s) => s.moveSubtree);
  const treeExpanded = useUIStore((s) => s.treeExpanded);
  const setTreeExpanded = useUIStore((s) => s.setTreeExpanded);
  const treeSelection = useUIStore((s) => s.treeSelection);
  const setTreeSelection = useUIStore((s) => s.setTreeSelection);
  const treeFocused = useUIStore((s) => s.treeFocused);
  const setTreeFocused = useUIStore((s) => s.setTreeFocused);

  const [filter, setFilter] = useState("");
  const [folderName, setFolderName] = useState("");
  // undefined = dialog closed, null = collection root, a folder = inside it.
  const [newFolderParent, setNewFolderParent] = useState<ItemWithPath | null | undefined>(
    undefined
  );
  const [pickerParent, setPickerParent] = useState<ItemWithPath | null | undefined>(undefined);
  const [confirm, setConfirm] = useState<ItemWithPath[]>([]);
  const [metadataFolder, setMetadataFolder] = useState<ItemWithPath | null>(null);
  const treeRef = useRef<TreeHandle<ItemWithPath>>(null);
  // Empty `nodes` = a right-click on the panel's empty space, i.e. the collection root.
  const [menu, setMenu] = useState<{ x: number; y: number; nodes: ItemWithPath[] } | null>(null);

  const filtered = useMemo(() => filterTree(rootItems, filter), [rootItems, filter]);
  const total = useMemo(() => countRequests(rootItems), [rootItems]);
  const adapter = useRequestTreeAdapter(filtered);

  const generators = useMemo<GeneratorDef[]>(
    () =>
      workspace?.scripts
        .filter((s) => s.kind === ScriptKind.GENERATOR)
        .map((s) => ({ name: s.name, source: s.source })) ?? [],
    [workspace?.scripts]
  );

  const expandFolder = (folder: ItemWithPath): void =>
    setTreeExpanded(new Set([...treeExpanded, itemKey(folder)]));

  const submitFolder = () => {
    const name = folderName.trim();
    if (name) {
      createFolder.mutate({
        collection,
        path: childPathOf(newFolderParent ?? null),
        itemName: name,
      });
    }
    setFolderName("");
    setNewFolderParent(undefined);
  };

  const onPick = (service: Service, method: Method) => {
    createRequest.mutate({
      collection,
      path: childPathOf(pickerParent ?? null),
      itemName: method.name,
      service: serviceName(service),
      method: method.name,
    });
    setPickerParent(undefined);
  };

  const doRename = (item: ItemWithPath, newName: string) => {
    const next = newName.trim();
    if (!next || next === item.item.name) return;
    const args = {
      collection,
      path: item.path,
      itemName: item.item.name,
      name: next,
    };
    // No key remap: keys are slug-based, and a rename leaves every slug alone.
    if (item.item.content.case === "folder") updateFolder.mutate(args);
    else updateRequest.mutate(args);
  };

  const doDelete = () => {
    for (const item of confirm) {
      deleteRequest.mutate({
        collection,
        path: item.path,
        itemName: item.item.name,
      });
    }
    setConfirm([]);
  };

  const onTreeDelete = (nodes: ItemWithPath[]): void => setConfirm(pruneNestedSelections(nodes));

  // Resolved out of the UNFILTERED rootItems: `parent.children` is pruned to whatever
  // the filter box left visible, so a collision test against it would miss a hidden
  // sibling the server will reject.
  const destinationChildren = (parent: ItemWithPath | null): ItemWithPath[] =>
    parent ? findByKey(rootItems, itemKey(parent))?.children ?? [] : rootItems;

  const samePath = (a: readonly string[], b: readonly string[]): boolean =>
    a.length === b.length && a.every((segment, i) => segment === b[i]);

  // Covers only what the tree cannot see (collapsed or filtered-out siblings); the
  // tree rejects everything structural itself. Collection.Move refuses a reparent onto
  // an existing display name, so this mirrors that rule early.
  const canDropHere = (
    nodes: ItemWithPath[],
    to: { parent: ItemWithPath | null; before?: ItemWithPath }
  ): boolean => {
    const newPath = childPathOf(to.parent);
    const taken = new Set(destinationChildren(to.parent).map((child) => child.item.name));
    for (const node of pruneNestedSelections(nodes)) {
      if (samePath(node.path, newPath)) continue; // pure reorder in its own parent
      if (taken.has(node.item.name)) return false;
      taken.add(node.item.name);
    }
    return true;
  };

  // Sequenced through onSuccess, not fired concurrently: every call carries the same
  // `before`, so the order the server processes them in becomes the sibling order on
  // disk.
  const onTreeMove = (
    nodes: ItemWithPath[],
    to: { parent: ItemWithPath | null; before?: ItemWithPath }
  ): void => {
    const newPath = childPathOf(to.parent);
    const batch = pruneNestedSelections(nodes);
    const fire = (i: number): void => {
      const node = batch[i];
      if (node === undefined) return;
      moveItem.mutate(
        {
          collection,
          path: node.path,
          itemName: node.item.name,
          newPath,
          before: to.before?.item.name,
        },
        {
          // The new key comes from the response, never from names: Move allocates a
          // fresh slug when the destination already has one by that name.
          onSuccess: (res) => {
            const newKey = slugKeyIn(collection, res.collection?.item, newPath, node.item.name);
            if (newKey) moveSubtree(itemKey(node), newKey);
            fire(i + 1);
          },
        }
      );
    };
    fire(0);
    if (to.parent) expandFolder(to.parent);
  };

  const rowCallbacks: RequestRowCallbacks = {
    services,
    onStartRename: (item) => treeRef.current?.startRename(itemKey(item)),
    onNewRequestUnder: (folder) => {
      setPickerParent(folder);
      expandFolder(folder);
    },
    // A row's own trash button names exactly that one row, not the selection.
    onDelete: (item) => setConfirm([item]),
    onEditMetadata: setMetadataFolder,
  };
  const renderRow = (item: ItemWithPath, state: TreeRowState): ReactNode =>
    renderRequestRow(item, state, rowCallbacks);

  const menuActions: CollectionMenuActions = {
    newRequest: (parent) => {
      setPickerParent(parent);
      if (parent) expandFolder(parent);
    },
    newFolder: (parent) => {
      setNewFolderParent(parent);
      if (parent) expandFolder(parent);
    },
    startRename: (item) => treeRef.current?.startRename(itemKey(item)),
    requestDelete: onTreeDelete,
    editFolderMetadata: setMetadataFolder,
  };

  const deleteCopy = deleteConfirmCopy(confirm);

  return (
    <div
      className="bg-panel flex flex-col"
      style={{ width: 278, flex: "none", borderRight: "1px solid var(--line)", minHeight: 0 }}
    >
      <div
        className="flex items-center gap-[8px]"
        style={{ height: 40, flex: "none", padding: "0 12px", borderBottom: "1px solid var(--line)" }}
      >
        <MagnifyingGlass size={14} style={{ color: "var(--color-neutral-500)" }} />
        <input
          className="bare"
          style={{ fontSize: 13 }}
          placeholder="Filter requests…"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
        <IconButton title="New folder" onClick={() => setNewFolderParent(null)}>
          <FolderPlus />
        </IconButton>
        <IconButton
          title="New request"
          onClick={() => setPickerParent(null)}
          disabled={services.length === 0}
        >
          <Plus />
        </IconButton>
      </div>

      <div
        style={{ flex: 1, overflow: "auto", padding: "8px" }}
        // defaultPrevented means a row already claimed the gesture in Tree.tsx;
        // isEditableTarget is the same exception the tree makes for a mid-rename box.
        onContextMenu={(ev) => {
          if (ev.defaultPrevented || isEditableTarget(ev.target as HTMLElement)) return;
          ev.preventDefault();
          setMenu({ x: ev.clientX, y: ev.clientY, nodes: [] });
        }}
      >
        <div
          className="flex items-center justify-between"
          style={{ padding: "2px 6px 6px" }}
        >
          <span
            style={{
              fontSize: 10,
              letterSpacing: ".1em",
              textTransform: "uppercase",
              color: "var(--color-neutral-600)",
            }}
          >
            Collection
          </span>
          <span
            className="font-mono"
            style={{ fontSize: 11, color: "var(--color-neutral-600)" }}
          >
            {total} {total === 1 ? "req" : "reqs"}
          </span>
        </div>

        {rootItems.length === 0 ? (
          <div
            className="text-muted"
            style={{ fontSize: 12, padding: "16px 6px", lineHeight: 1.6 }}
          >
            {services.length === 0
              ? "No requests yet. Add a definition source, then create a request."
              : "No requests yet. Use + to create one."}
          </div>
        ) : (
          <Tree
            adapter={adapter}
            handle={treeRef}
            renderRow={renderRow}
            expanded={treeExpanded}
            onExpandedChange={setTreeExpanded}
            selection={treeSelection}
            onSelectionChange={setTreeSelection}
            focused={treeFocused}
            onFocusedChange={setTreeFocused}
            activeId={activeKey}
            onOpen={openTab}
            onRenameCommit={doRename}
            onDelete={onTreeDelete}
            onMove={onTreeMove}
            canDrop={canDropHere}
            // clientX/clientY, not pageX/pageY: .menu is position: fixed.
            onContextMenu={(nodes, ev) =>
              setMenu({ x: ev.clientX, y: ev.clientY, nodes })
            }
            aria-label="Collection"
          />
        )}
      </div>

      <Dialog
        open={newFolderParent !== undefined}
        onClose={() => setNewFolderParent(undefined)}
        title={newFolderParent ? `New folder in ${newFolderParent.item.name}` : "New folder"}
        width={380}
      >
        <Input
          autoFocus
          placeholder="Folder name"
          value={folderName}
          onChange={(e) => setFolderName(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") submitFolder();
          }}
        />
        <div className="dialog-actions">
          <Button onClick={() => setNewFolderParent(undefined)}>Cancel</Button>
          <Button variant="primary" onClick={submitFolder} disabled={!folderName.trim()}>
            Create
          </Button>
        </div>
      </Dialog>

      {/* Keyed on the summon point plus the row set so a second right-click remounts. */}
      {menu ? (
        <Menu
          key={`${menu.x},${menu.y},${menu.nodes.map(itemKey).join("|")}`}
          x={menu.x}
          y={menu.y}
          items={collectionMenuItems(menu.nodes, menuActions, {
            canCreateRequest: services.length > 0,
          })}
          onClose={() => setMenu(null)}
        />
      ) : null}

      <MethodPickerModal
        open={pickerParent !== undefined}
        services={services}
        onClose={() => setPickerParent(undefined)}
        onSelect={onPick}
      />

      {/* Keyed by the open folder's identity so a fresh instance mounts per open. */}
      <FolderMetadataDialog
        key={metadataFolder ? itemKey(metadataFolder) : "none"}
        folder={metadataFolder}
        onClose={() => setMetadataFolder(null)}
        generators={generators}
      />

      <Dialog
        open={confirm.length > 0}
        onClose={() => setConfirm([])}
        title={deleteCopy.title}
        width={380}
      >
        <p className="dialog-body">
          Delete <strong>{deleteCopy.emphasis}</strong>
          {deleteCopy.suffix}
        </p>
        <div className="dialog-actions">
          <Button onClick={() => setConfirm([])}>Cancel</Button>
          <Button variant="danger" onClick={doDelete}>
            Delete
          </Button>
        </div>
      </Dialog>
    </div>
  );
}
