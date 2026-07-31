import { useMemo, useRef, useState, type ReactNode } from "react";
import { MagnifyingGlass, FolderPlus, Plus } from "@/components/ui/icons";
import { ScriptKind, type Service, type Method } from "@grpcview/v1/workspace_pb";
import { IconButton, Button } from "@/components/ui/Button";
import { Dialog } from "@/components/ui/Dialog";
import { Input } from "@/components/ui/Input";
import { Menu } from "@/components/ui/Menu";
import { Tree, isEditableTarget } from "@/components/tree/Tree";
import type { TreeHandle, TreeRowState } from "@/components/tree/types";
import { useWorkspace, useRootItems, useWorkspaceMutations, WORKSPACE_NAME } from "@/lib/workspace-query";
import { useUIStore } from "@/lib/ui-store";
import {
  childPathOf,
  findByKey,
  itemKey,
  keyOf,
  pruneNestedSelections,
  serviceName,
  type ItemWithPath,
} from "@/lib/format";
import { renderRequestRow, useRequestTreeAdapter, type RequestRowCallbacks } from "./request-tree";
import { MethodPickerModal } from "./MethodPickerModal";
import { FolderMetadataDialog } from "./FolderMetadataDialog";
import type { GeneratorDef } from "./generator-libs";
import { deleteConfirmCopy } from "./delete-confirm";
import { collectionMenuItems, type CollectionMenuActions } from "./collection-menu";

// Count requests in a subtree (for the header total; a folder row's own count is
// its direct children — request-tree.tsx's getTreeItem — which is a different,
// non-recursive number).
const countRequests = (items: ItemWithPath[]): number =>
  items.reduce(
    (n, it) =>
      it.item.content.case === "folder"
        ? n + countRequests(it.children ?? [])
        : n + 1,
    0
  );

// filterTree keeps requests whose name matches and folders that (recursively)
// contain a match, with children pruned to the matches.
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
  const { workspace, services } = useWorkspace();
  const rootItems = useRootItems(workspace);
  const { createFolder, createRequest, deleteRequest, updateRequest, updateFolder, moveItem } =
    useWorkspaceMutations();
  const openTab = useUIStore((s) => s.openTab);
  const activeKey = useUIStore((s) => s.activeKey);
  const moveSubtree = useUIStore((s) => s.moveSubtree);
  // Tree expansion/selection/focus are CONTROLLED state owned by zustand, not the
  // component (tree-rewrite-plan.md "Enduring decisions" #5) — pulled individually,
  // not as one object selector, matching every other useUIStore read in this file
  // (an object selector would build a fresh object every render and defeat
  // zustand's reference-equality check).
  const treeExpanded = useUIStore((s) => s.treeExpanded);
  const setTreeExpanded = useUIStore((s) => s.setTreeExpanded);
  const treeSelection = useUIStore((s) => s.treeSelection);
  const setTreeSelection = useUIStore((s) => s.setTreeSelection);
  const treeFocused = useUIStore((s) => s.treeFocused);
  const setTreeFocused = useUIStore((s) => s.setTreeFocused);

  const [filter, setFilter] = useState("");
  const [folderName, setFolderName] = useState("");
  // Where a new folder would be created — `undefined` means the dialog is CLOSED,
  // `null` means the collection root, a folder means inside that folder. Exactly
  // the three-state shape `pickerParent` below already uses for new requests, and
  // it replaces T0's `showNewFolder` boolean: submitFolder used to hardcode
  // `path: []`, which is right for the header button and wrong for a folder row's
  // context menu — "New folder" on `Alpha` has to create inside `Alpha` (T5).
  const [newFolderParent, setNewFolderParent] = useState<ItemWithPath | null | undefined>(
    undefined
  );
  const [pickerParent, setPickerParent] = useState<ItemWithPath | null | undefined>(undefined);
  // The item(s) pending delete confirmation — empty means the dialog is
  // closed. Holds the WHOLE (already-pruned — see onTreeDelete below) batch
  // as of T2, not just one item: the tree's own onDelete can now hand over a
  // multi-selection (tree-rewrite-plan.md's T2 line, "Make delete
  // multi-aware"), and a row's own trash button (rowCallbacks.onDelete below)
  // feeds the identical state via a one-element array, so there is exactly
  // ONE confirm flow regardless of which of the two triggered it.
  const [confirm, setConfirm] = useState<ItemWithPath[]>([]);
  // The folder row whose metadata dialog is open (gv-features-plan.md Feature 1); null = closed.
  const [metadataFolder, setMetadataFolder] = useState<ItemWithPath | null>(null);
  // "Which row is mid-rename" is the TREE's own internal state as of T4b (it
  // renders the box, validates the name, and commits), so this file no longer
  // holds it. All that is left here is the imperative handle a row's pencil
  // button needs to START one — TreeHandle.startRename, the home the contract
  // gives to actions rather than values (components/tree/types.ts).
  const treeRef = useRef<TreeHandle<ItemWithPath>>(null);
  // The open context menu (T5): where it was summoned, and the rows it acts on
  // (empty = a right-click on the panel's empty space, i.e. the collection root).
  // null = closed. The TREE does not render the menu — it hands over the nodes
  // plus the event and stops there, because the items are gRPC-shaped and
  // enduring decision 1 forbids the component knowing about them
  // (tree-rewrite-plan.md §Risks, "Scope creep into the row renderer").
  const [menu, setMenu] = useState<{ x: number; y: number; nodes: ItemWithPath[] } | null>(null);

  const filtered = useMemo(() => filterTree(rootItems, filter), [rootItems, filter]);
  const total = useMemo(() => countRequests(rootItems), [rootItems]);
  // Memoized over `filtered` (itself memoized above): a fresh adapter object every
  // render would force useTreeState's flatten() to re-run on every unrelated
  // CollectionPanel render, not just when the tree's own data actually changes.
  const adapter = useRequestTreeAdapter(filtered);

  // The workspace's saved GENERATORS, forwarded to the folder-metadata dialog's MetadataEditor for
  // the same ambient autocomplete the request metadata editor gets (mirrors RequestWorkspace.tsx).
  const generators = useMemo<GeneratorDef[]>(
    () =>
      workspace?.scripts
        .filter((s) => s.kind === ScriptKind.GENERATOR)
        .map((s) => ({ name: s.name, source: s.source })) ?? [],
    [workspace?.scripts]
  );

  // Force a folder open, so a request/folder created inside it is immediately
  // visible instead of landing in a collapsed subtree. Expansion is CONTROLLED
  // state (enduring decision 5), so "open it" means adding its id to
  // treeExpanded; extracted because both create paths need it now (T0 had it
  // inline in onNewRequestUnder alone, T5 adds the new-folder-in-folder case).
  const expandFolder = (folder: ItemWithPath): void =>
    setTreeExpanded(new Set([...treeExpanded, itemKey(folder)]));

  const submitFolder = () => {
    const name = folderName.trim();
    if (name) {
      // childPathOf, not a hardcoded `[]`: the parent is the collection root only
      // when the header's own folder button opened this dialog. See
      // newFolderParent's declaration.
      createFolder.mutate({
        workspaceName: WORKSPACE_NAME,
        path: childPathOf(newFolderParent ?? null),
        itemName: name,
      });
    }
    setFolderName("");
    setNewFolderParent(undefined);
  };

  const onPick = (service: Service, method: Method) => {
    // new request defaults its name to the method name; rename it inline afterward
    createRequest.mutate({
      workspaceName: WORKSPACE_NAME,
      path: childPathOf(pickerParent ?? null),
      itemName: method.name,
      service: serviceName(service),
      method: method.name,
    });
    setPickerParent(undefined);
  };

  // Rename an item — REQUESTS AND FOLDERS BOTH, as of T4a (UpdateFolderRequest
  // carries `name` now; the comment that used to live here explaining why a folder
  // rename was impossible is gone with the limitation). The two differ only in
  // which RPC persists it: UpdateRequest vs UpdateFolder, addressed identically by
  // path + itemName with `name` as the new display name, both rejecting a sibling
  // collision with FailedPrecondition server-side. Neither moves anything on disk
  // — a folder keeps its directory and its descendants keep their files (T4a's
  // slug-identity model) — but the CLIENT's keys are name-derived, so the
  // subtree remap still has to happen.
  //
  // Fire-and-forget .mutate with an onSuccess, like every other mutation in this
  // app (nothing here awaits one — see doDelete's comment for the full reasoning):
  // moveSubtree runs only if the server accepted, so a rejected rename leaves every
  // open tab, draft and response exactly where it was.
  const doRename = (item: ItemWithPath, newName: string) => {
    const next = newName.trim();
    if (!next || next === item.item.name) return;
    const args = {
      workspaceName: WORKSPACE_NAME,
      path: item.path,
      itemName: item.item.name,
      name: next,
    };
    // moveSubtree, not a single-key remap: renaming a FOLDER changes the key of
    // every descendant at once (the plan's "identity hazard"), and its own
    // treeExpanded membership besides.
    const opts = {
      onSuccess: () => moveSubtree(itemKey(item), keyOf(item.path, next), next),
    };
    if (item.item.content.case === "folder") updateFolder.mutate(args, opts);
    else updateRequest.mutate(args, opts);
  };

  // Fires ONE deleteRequest mutation per confirmed item — T2 makes this
  // selection-wide; T1 only ever had exactly one item to fire for. `confirm`
  // is already PRUNED (onTreeDelete below), so by the time this runs, no item
  // in it is a descendant of another item also in it: deleting a folder
  // removes its whole subtree server-side (service/store/fs.go's
  // Collection.Delete: `os.RemoveAll` on the item's own directory), so firing
  // a separate delete for something the batch's own folder already took with
  // it would be pure waste, not a correctness problem in itself — Delete is
  // documented idempotent (deleting an already-gone item is a no-op, not an
  // error) — but pruning is what keeps the CONFIRM COPY's own count honest
  // too (deleteConfirmCopy runs against this same pruned list), so it happens
  // once, up front, rather than being re-derived here.
  //
  // Each `.mutate(...)` call is independent and unawaited, exactly like every
  // OTHER mutation call in this codebase (createFolder/createRequest/
  // updateRequest above all fire-and-forget the same way; grep finds no
  // `.mutateAsync` anywhere in this app) — considered and rejected chaining
  // them via `mutateAsync` to force strict sequencing, since the ACTUAL
  // failure mode that would motivate it (a folder-plus-its-own-descendant
  // batch) is already ruled out by pruning above, and introducing the only
  // awaited-mutation pattern in the app for a batch that pruning already made
  // safe would be new complexity without a concrete bug it fixes. Firing them
  // concurrently is still safe to reason about: Collection.Delete takes its
  // own mutex per call (service/store/fs.go), so the backend serializes the
  // actual filesystem mutations regardless of request arrival order, and
  // every mutation here shares the SAME onSuccess (useSeedGetCache,
  // workspace-query.ts) that reseeds the Get-query cache from whichever
  // response lands — a pre-existing characteristic of that reseed-the-whole-
  // snapshot design for ANY two mutations fired close together, single-item
  // or not, not something this batch delete introduces.
  const doDelete = () => {
    for (const item of confirm) {
      deleteRequest.mutate({
        workspaceName: WORKSPACE_NAME,
        path: item.path,
        itemName: item.item.name,
      });
    }
    setConfirm([]);
  };

  // Tree's own onDelete (keyboard Delete / mac cmd+Backspace on the focused
  // row OR its whole selection, tree-rewrite-plan.md's T1/T2 key table)
  // reaches the SAME confirm dialog as each row's own trash button
  // (RequestRowCallbacks.onDelete below) — just a second entry point into the
  // identical `confirm` state. T1 only ever called this with exactly the
  // focused node; T2 makes Tree.tsx's own dispatch selection-aware
  // (dispatch.ts's resolveDeleteIds), so `nodes` here can genuinely be a
  // multi-row batch now — which is exactly when a folder AND one of its own
  // descendants can both be IN that batch at once (reachable via shift+click
  // across an expanded folder's rows, or ctrl+click picking both
  // individually — see pruneNestedSelections' own comment, lib/format.ts, for
  // the full reasoning). Pruning HERE, once, at the point delete is
  // REQUESTED, is what lets both `confirm`'s own count (deleteConfirmCopy
  // below) and doDelete's mutation loop above trust the list verbatim,
  // instead of each re-deriving "but is any of these actually redundant" on
  // its own.
  const onTreeDelete = (nodes: ItemWithPath[]): void => setConfirm(pruneNestedSelections(nodes));

  // ── drag and drop (T6b) ────────────────────────────────────────────────────

  // The destination folder's REAL children, resolved out of the UNFILTERED
  // `rootItems` rather than read off `to.parent.children`. That distinction is
  // load-bearing, not defensive: the tree's adapter is built over `filtered`, so
  // every ItemWithPath the tree hands back — `to.parent` included — carries
  // children pruned to whatever the filter box left visible (filterTree above
  // rebuilds each folder as `{...it, children: kids}`). Testing a name collision
  // against that pruned list would call a drop legal while the server holds a
  // hidden sibling of the same name and rejects it.
  const destinationChildren = (parent: ItemWithPath | null): ItemWithPath[] =>
    parent ? findByKey(rootItems, itemKey(parent))?.children ?? [] : rootItems;

  const samePath = (a: readonly string[], b: readonly string[]): boolean =>
    a.length === b.length && a.every((segment, i) => segment === b[i]);

  // The host's half of drop validity. The TREE already rejects everything
  // structural — into a leaf, into a dragged node's own subtree, a no-op move
  // (components/tree/dnd.ts) — so this covers only what the tree cannot see: the
  // destination's OWN children, which may be collapsed (not rows at all) or
  // filtered out. `Collection.Move` refuses a reparent onto an existing display
  // name with FailedPrecondition/ErrAlreadyExists — a move never silently renames
  // what it moves — and this is that same rule, enforced early so the pointer shows
  // an invalid drop instead of the drop appearing to work and nothing happening.
  //
  // A REORDER inside the item's own parent is exempt: the colliding name there is
  // the item itself, and the server skips the check entirely for that case (same
  // destDir → the pure-reorder branch). The running `taken` set also catches the
  // one collision the destination's children cannot: two rows in a multi-drag that
  // share a display name and are both being reparented into the same folder.
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

  // One MoveItem per dropped item. `path`/`itemName` address the item, `newPath` is
  // the destination parent (childPathOf already maps a null parent to `[]`, which is
  // exactly new_path's "collection root"), and `before` names the destination
  // sibling to insert ahead of — unset appends. A newPath resolving to the item's
  // CURRENT parent is a pure reorder server-side, which is why "dropped between two
  // rows of the same folder" needs no separate code path here.
  //
  // Pruned first, for the same reason the delete batch is: dragging a folder plus
  // one of its own children must move one thing. The second call would address a
  // path that no longer exists by the time it ran (the child moved WITH its folder),
  // and would therefore fail rather than merely being redundant.
  //
  // The onSuccess is the plan's IDENTITY HAZARD, and it is not optional: a move
  // changes the item's key exactly as a rename does (itemKey is path+name derived),
  // and for a folder it changes every descendant's. moveSubtree is the same
  // prefix remap doRename above uses — openTabs, drafts, invokes, treeSelection,
  // treeFocused, treeExpanded. Skipping it detaches an open tab from its draft and
  // last response silently, which reads as lost work rather than as a bug.
  //
  // Still fire-and-forget — nothing in this app awaits a mutation (doDelete's
  // comment has the full reasoning) — but unlike every other batch here these
  // calls are SEQUENCED, each one fired from the previous one's onSuccess.
  //
  // The reason is specific to move and does not apply to the delete batch this
  // otherwise mirrors. Every call in a multi-row move carries the SAME `before`,
  // so each insertion lands immediately ahead of that one sibling and the order
  // the server PROCESSES them in becomes the resulting sibling order. Fired
  // concurrently, that order is whatever the transport and the store's per-call
  // mutex happen to produce: correct data in a permuted order, and — unlike a
  // stale cache — the permutation is written to disk. Tree.tsx sorts the dragged
  // set into row order precisely so that "visual order in, visual order out"
  // holds; sequencing here is the other half of that promise, without which the
  // sort is necessary but not sufficient.
  //
  // Chaining through onSuccess rather than awaiting mutateAsync is what keeps
  // this from becoming the app's only awaited-mutation path: onSuccess is the
  // same callback every other mutation in this file already uses, and each link
  // does exactly what a single-item move does. A failed call simply stops the
  // chain, which is the better half-applied state to be left in — a prefix of
  // the batch moved, in the right order — rather than an arbitrary subset.
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
          workspaceName: WORKSPACE_NAME,
          path: node.path,
          itemName: node.item.name,
          newPath,
          before: to.before?.item.name,
        },
        {
          onSuccess: () => {
            moveSubtree(itemKey(node), keyOf(newPath, node.item.name), node.item.name);
            fire(i + 1);
          },
        }
      );
    };
    fire(0);
    // Force the destination open, the way every create path does (expandFolder) —
    // otherwise an `into` drop on a collapsed folder makes the dragged rows appear
    // to vanish. Unconditional rather than gated on the drop being `into` (a zone
    // onMove is not told, deliberately — the destination is the whole contract): for
    // a between-rows drop the parent is necessarily expanded already, since its
    // children were the visible rows the pointer was between, so this is a no-op
    // there.
    if (to.parent) expandFolder(to.parent);
  };

  // Callbacks renderRequestRow needs beyond ItemWithPath itself — see
  // request-tree.tsx's RequestRowCallbacks for why these can't be derived from the
  // node alone.
  const rowCallbacks: RequestRowCallbacks = {
    services,
    // A row's pencil is now just a second trigger for the tree's OWN rename, the
    // same way F2/macOS-Enter is (both land in Tree.tsx's renamingId state) —
    // there is no host-side rename UI left for it to open. Folder rows get one
    // too, since T4a made folders renamable.
    onStartRename: (item) => treeRef.current?.startRename(itemKey(item)),
    onNewRequestUnder: (folder) => {
      setPickerParent(folder);
      expandFolder(folder);
    },
    // A row's own trash button always names exactly that ONE row, regardless
    // of whatever the tree's broader multi-selection happens to be right now
    // — a per-row affordance is a targeted single-item action, not "delete
    // whatever's selected" (mirrors how a real file manager's own per-row
    // delete icon acts on that row alone). Wrapped in a one-element array
    // rather than passed as `setConfirm` directly (T0/T1's shape, since
    // `confirm` used to hold a bare `ItemWithPath | null`): `confirm` is a
    // list as of this phase, so RequestRowCallbacks.onDelete's own
    // single-item signature (request-tree.tsx) needs an explicit adapter now.
    // pruneNestedSelections is a no-op on a one-element array (nothing else
    // in the array to be an ancestor of), so there is no need to route this
    // through it too.
    onDelete: (item) => setConfirm([item]),
    onEditMetadata: setMetadataFolder,
  };
  const renderRow = (item: ItemWithPath, state: TreeRowState): ReactNode =>
    renderRequestRow(item, state, rowCallbacks);

  // What the context menu's items DO — every one of them an existing handler,
  // reached by a second path (the menu) exactly as F2 and the row pencil are two
  // paths to one rename. Nothing new is created here; the only novelty is that
  // `newFolder` can now name a parent at all (see submitFolder).
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
    // Already pruned by collectionMenuItems (its own comment explains why it has
    // to be, for the label's count to match the dialog's), and onTreeDelete
    // prunes again — idempotent, and cheaper to leave than to add a second
    // "already pruned?" entry point to the one confirm flow.
    requestDelete: onTreeDelete,
    editFolderMetadata: setMetadataFolder,
  };

  // Not memoized: deleteConfirmCopy is a handful of string-length/composition
  // checks over `confirm`, which itself never holds more than a small
  // multi-selection (this app's whole scale, per the plan: "dozens to low
  // hundreds") — nowhere near costly enough to need useMemo the way
  // `filtered`/`total` above are (those re-walk the ENTIRE collection tree on
  // every keystroke in the filter box; this recomputes only when `confirm`
  // itself changes, i.e. essentially never while the dialog is closed).
  const deleteCopy = deleteConfirmCopy(confirm);

  return (
    <div
      className="bg-panel flex flex-col"
      style={{ width: 278, flex: "none", borderRight: "1px solid var(--line)", minHeight: 0 }}
    >
      {/* header: filter + actions */}
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

      {/* tree */}
      <div
        style={{ flex: 1, overflow: "auto", padding: "8px" }}
        // Right-click on the panel's EMPTY SPACE (below the last row, or the
        // "Collection / N reqs" strip) offers the collection ROOT's creation
        // actions. It has to live here rather than in the tree: the tree only
        // sees right-clicks that land on one of its rows, and this scroll
        // container is the surface the rest of the panel actually is.
        //
        // defaultPrevented is the guard against double-firing. A right-click ON a
        // row is handled first by Tree.tsx's own handleContextMenu, which
        // preventDefault()s it — and React's synthetic event bubbles up here
        // carrying that flag, so seeing it means "a row already claimed this
        // gesture". Without the guard, every row right-click would open the row's
        // menu and then immediately overwrite it with the root's.
        // isEditableTarget mirrors the same escape hatch the tree makes for a
        // mid-rename row: that path deliberately does NOT preventDefault (the
        // native menu is the only way to paste into the box), so this handler has
        // to make the identical exception or it would swallow the gesture the
        // tree just declined.
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
            // The tree has already selected the row if it wasn't selected, moved
            // focus to it, and preventDefault()ed the native menu; `nodes` is its
            // post-click selection. All that is left is to put a menu at the
            // pointer. clientX/clientY, not pageX/pageY: .menu is
            // position: fixed (app-tokens.css), so it is placed in viewport
            // coordinates.
            onContextMenu={(nodes, ev) =>
              setMenu({ x: ev.clientX, y: ev.clientY, nodes })
            }
            aria-label="Collection"
          />
        )}
      </div>

      {/* new folder dialog. The title NAMES the destination when it isn't the
          root — "New folder" is unambiguous from the header button, but a menu
          item invoked on some folder three levels down needs to say where the
          folder is about to land. */}
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

      {/* context menu (T5). Mounted only while open — Menu wraps Backdrop, whose
          Escape listener and opener-focus save/restore live for exactly the
          backdrop's lifetime (see its own header). Keyed on the summon point plus
          the row set so a second right-click somewhere else remounts it rather
          than repositioning a menu that still holds the previous row's
          highlight. */}
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

      {/* new request: method picker */}
      <MethodPickerModal
        open={pickerParent !== undefined}
        services={services}
        onClose={() => setPickerParent(undefined)}
        onSelect={onPick}
      />

      {/* folder metadata editor. Keyed by the open folder's identity (or "none" while closed) so
          a fresh instance mounts per open — see FolderMetadataDialog's own seeding comment. */}
      <FolderMetadataDialog
        key={metadataFolder ? itemKey(metadataFolder) : "none"}
        folder={metadataFolder}
        onClose={() => setMetadataFolder(null)}
        generators={generators}
      />

      {/* delete confirm — copy is entirely deleteConfirmCopy's call
          (./delete-confirm.ts); this JSX only ever lays out its three
          fields, unchanged in shape whether `confirm` holds 1 item or N. */}
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
