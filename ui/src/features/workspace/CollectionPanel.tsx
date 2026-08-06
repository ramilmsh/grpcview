import { useMemo, useRef, useState, type ReactNode } from "react";
import { MagnifyingGlass, FolderPlus, Plus } from "@/components/ui/icons";
import { ScriptKind, type Service, type Method } from "@grpcview/v1/workspace_pb";
import { IconButton, Button } from "@/components/ui/Button";
import { Dialog } from "@/components/ui/Dialog";
import { Input } from "@/components/ui/Input";
import { Menu } from "@/components/ui/Menu";
import { Tree, isEditableTarget } from "@/components/tree/Tree";
import type { TreeHandle, TreeRowState } from "@/components/tree/types";
import {
  useActiveWorkspace,
  useCollectionItems,
  useCollections,
  useWorkspaceMutations,
} from "@/lib/workspace-query";
import { useUIStore } from "@/lib/ui-store";
import {
  childPathOf,
  itemKey,
  pruneNestedSelections,
  serviceName,
  slugKeyIn,
  type ItemWithPath,
} from "@/lib/format";
import type { RequestRowCallbacks } from "./request-tree";
import {
  panelItems,
  panelNodeId,
  panelTiered,
  renderPanelRow,
  usePanelTreeAdapter,
  type PanelNode,
} from "./panel-tree";
import {
  collectionForNodeId,
  collectionsToQuery,
  countAllRequests,
  filterItemsByCollection,
  panelCanDrop,
  panelDropCollection,
  panelDropParentItem,
} from "./panel-wiring";
import { MethodPickerModal } from "./MethodPickerModal";
import { FolderMetadataDialog } from "./FolderMetadataDialog";
import type { GeneratorDef } from "./generator-libs";
import { deleteConfirmCopy } from "./delete-confirm";
import { collectionMenuItems, type CollectionMenuActions } from "./collection-menu";
import { NewCollectionDialog } from "./NewCollectionDialog";

export function CollectionPanel() {
  const { collection: activeCollection, workspace, services } = useActiveWorkspace();
  // Non-null everywhere this panel renders: App gates all three views behind the
  // collection listing, so "" is the unreachable pre-gate value rather than a default.
  const collection = activeCollection ?? "";
  const { collections } = useCollections();
  const { createFolder, createRequest, deleteRequest, updateRequest, updateFolder, moveItem } =
    useWorkspaceMutations();
  const openTab = useUIStore((s) => s.openTab);
  const setActiveCollection = useUIStore((s) => s.setActiveCollection);
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
  const [newCollection, setNewCollection] = useState(false);
  const treeRef = useRef<TreeHandle<PanelNode>>(null);
  // Empty `nodes` = a right-click on the panel's empty space, i.e. the collection root.
  const [menu, setMenu] = useState<{ x: number; y: number; nodes: PanelNode[] } | null>(null);

  // One Get per collection the tree needs items from — the active one plus every expanded
  // collection row. Absent from the map = not landed yet, which is a "Loading…" row.
  const queryIds = useMemo(
    () => collectionsToQuery(activeCollection, treeExpanded, collections),
    [activeCollection, treeExpanded, collections]
  );
  const loaded = useCollectionItems(queryIds);
  const itemsByCollection = useMemo(
    () => filterItemsByCollection(loaded, filter),
    [loaded, filter]
  );

  const tiered = panelTiered(collections);
  const adapter = usePanelTreeAdapter({ collections, itemsByCollection, activeCollection });
  // Untiered, the sole collection IS the active one (resolveActiveCollection), and these are
  // the tree's roots. Unfiltered, because the empty state asks what the collection HOLDS.
  const soloItems = tiered ? [] : loaded.get(collections[0]?.id ?? "") ?? [];
  // Tiered, the strip counts the whole workspace: no row carries a per-collection count, so
  // an active-collection count next to several collection rows would name none of them.
  const total = useMemo(() => countAllRequests(loaded), [loaded]);

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

  // Addressed at the ITEM's own collection, not the active one: with the tier on screen, the
  // rows of a collection that is not active are renameable and deletable too.
  const doRename = (item: ItemWithPath, newName: string) => {
    const next = newName.trim();
    if (!next || next === item.item.name) return;
    const args = {
      collection: item.collection,
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
        collection: item.collection,
        path: item.path,
        itemName: item.item.name,
      });
    }
    setConfirm([]);
  };

  // The tiers the item callbacks cannot speak are dropped: a collection row is neither
  // renameable nor deletable here (deleting one is `grpcview` on the command line or the
  // filesystem; creating one is the context menu's "New collection…", the TopBar picker and
  // the empty state), and a status row is not a thing at all.
  const onTreeDelete = (nodes: PanelNode[]): void =>
    setConfirm(pruneNestedSelections(panelItems(nodes)));

  // Sequenced through onSuccess, not fired concurrently: every call carries the same
  // `before`, so the order the server processes them in becomes the sibling order on
  // disk.
  const onTreeMove = (
    nodes: PanelNode[],
    to: { parent: PanelNode | null; before?: PanelNode }
  ): void => {
    const batch = pruneNestedSelections(panelItems(nodes));
    // The DESTINATION's collection, never the panel's active one. panelDropAllowed has
    // already guaranteed every dragged item is in it, so the two agree today — writing the
    // destination is what keeps this correct if that rule ever loosens.
    const destination = panelDropCollection(batch, to.parent);
    if (destination === null) return;
    const parentItem = panelDropParentItem(to.parent);
    const newPath = childPathOf(parentItem);
    // A `before` can only be a sibling item; the tiers have no name to position against.
    const before = to.before?.kind === "item" ? to.before.item.item.name : undefined;
    const fire = (i: number): void => {
      const node = batch[i];
      if (node === undefined) return;
      moveItem.mutate(
        {
          collection: destination,
          path: node.path,
          itemName: node.item.name,
          newPath,
          before,
        },
        {
          // The new key comes from the response, never from names: Move allocates a
          // fresh slug when the destination already has one by that name.
          onSuccess: (res) => {
            const newKey = slugKeyIn(destination, res.collection?.item, newPath, node.item.name);
            if (newKey) moveSubtree(itemKey(node), newKey);
            fire(i + 1);
          },
        }
      );
    };
    fire(0);
    if (parentItem) expandFolder(parentItem);
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
  const renderRow = (node: PanelNode, state: TreeRowState): ReactNode =>
    renderPanelRow(node, state, rowCallbacks);

  const menuActions: CollectionMenuActions = {
    newRequest: (parent) => {
      setPickerParent(parent);
      if (parent) expandFolder(parent);
    },
    newFolder: (parent) => {
      setNewFolderParent(parent);
      if (parent) expandFolder(parent);
    },
    newCollection: () => setNewCollection(true),
    startRename: (item) => treeRef.current?.startRename(itemKey(item)),
    requestDelete: (items) => setConfirm(pruneNestedSelections(items)),
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
            {tiered ? "Collections" : "Collection"}
          </span>
          <span
            className="font-mono"
            style={{ fontSize: 11, color: "var(--color-neutral-600)" }}
          >
            {/* Tiered, this counts COLLECTIONS, not requests: a request total would only
                cover the collections whose rows are open, so collapsing one would change
                a number that describes the workspace. Untiered it is today's count. */}
            {tiered
              ? `${collections.length} collections`
              : `${total} ${total === 1 ? "req" : "reqs"}`}
          </span>
        </div>

        {/* Untiered only: with the tier on, panel-tree already puts a status row under each
            collection, so a panel-wide empty state would speak for collections it cannot see. */}
        {!tiered && soloItems.length === 0 ? (
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
            // Whatever row the tree focuses decides which collection is active, which is
            // what scopes Sources, Scripts and the pickers. onOpen is not enough: a single
            // click on an expandable row only toggles it, so a collection row is never
            // "opened" — and this also makes arrowing into another collection scope to it,
            // the way VS Code lets the selected thing decide the context.
            onFocusedChange={(id) => {
              setTreeFocused(id);
              const owner = id === null ? null : collectionForNodeId(id, collections);
              if (owner !== null) setActiveCollection(owner);
            }}
            activeId={activeKey}
            // A collection row opens nothing; focusing it already made it active. A status
            // row does nothing.
            onOpen={(node) => {
              if (node.kind === "item") openTab(node.item);
            }}
            onRenameCommit={(node, next) => {
              if (node.kind === "item") doRename(node.item, next);
            }}
            onDelete={onTreeDelete}
            onMove={onTreeMove}
            // The UNFILTERED map: the collision check must see siblings the filter hid.
            canDrop={(dragged, to) => panelCanDrop(dragged, to, { tiered, itemsByCollection: loaded })}
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

      <NewCollectionDialog open={newCollection} onClose={() => setNewCollection(false)} />

      {/* Keyed on the summon point plus the row set so a second right-click remounts.
          panelNodeId, not itemKey: an empty-space right-click and a collection right-click
          both unwrap to no items, and would otherwise share one key. */}
      {menu ? (
        <Menu
          key={`${menu.x},${menu.y},${menu.nodes.map(panelNodeId).join("|")}`}
          x={menu.x}
          y={menu.y}
          items={collectionMenuItems(panelItems(menu.nodes), menuActions, {
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
