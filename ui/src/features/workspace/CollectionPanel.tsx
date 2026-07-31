import { useMemo, useState, type ReactNode } from "react";
import { MagnifyingGlass, FolderPlus, Plus } from "@/components/ui/icons";
import { ScriptKind, type Service, type Method } from "@grpcview/v1/workspace_pb";
import { IconButton, Button } from "@/components/ui/Button";
import { Dialog } from "@/components/ui/Dialog";
import { Input } from "@/components/ui/Input";
import { Tree } from "@/components/tree/Tree";
import type { TreeRowState } from "@/components/tree/types";
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
  const { createFolder, createRequest, deleteRequest, updateRequest } = useWorkspaceMutations();
  const openTab = useUIStore((s) => s.openTab);
  const activeKey = useUIStore((s) => s.activeKey);
  const renameItem = useUIStore((s) => s.renameItem);
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
  const [showNewFolder, setShowNewFolder] = useState(false);
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
  // Which row (if any) is mid-rename. One row at a time, so this lives here rather
  // than per-row (today's pencil -> EditableName behavior; F2/keyboard rename is
  // T4b, out of scope here) — request-tree.tsx's renderRequestRow reads it back via
  // RequestRowCallbacks.
  const [renamingKey, setRenamingKey] = useState<string | null>(null);

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

  const submitFolder = () => {
    const name = folderName.trim();
    if (name) {
      createFolder.mutate({ workspaceName: WORKSPACE_NAME, path: [], itemName: name });
    }
    setFolderName("");
    setShowNewFolder(false);
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

  // Rename a request (folders are not renamable yet — N2a scope). Persists via
  // UpdateRequest, then remaps client-side keyed state old key -> new key so the
  // open tab/draft/response follow the rename (itemKey is name-derived).
  const doRename = (item: ItemWithPath, newName: string) => {
    const next = newName.trim();
    if (!next || next === item.item.name || item.item.content.case !== "request") return;
    updateRequest.mutate(
      { workspaceName: WORKSPACE_NAME, path: item.path, itemName: item.item.name, name: next },
      { onSuccess: () => renameItem(itemKey(item), keyOf(item.path, next), next) }
    );
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

  // Tree's own onRenamingChange (F2, or Enter on macOS, on the focused row) is
  // a second way to set the SAME renamingKey the pencil button already drives
  // (RequestRowCallbacks.onRenamingChange below) — one piece of state, two
  // triggers. A rename intent on a FOLDER row is a deliberate no-op: per
  // proto/grpcview/v1/service.proto, UpdateFolderRequest carries only
  // workspace_name/path/item_name/draft_metadata_script — no `name` field,
  // unlike UpdateRequestRequest, which has one — so there is nowhere on the
  // wire for a folder rename to go until T4a adds it. Letting renamingKey
  // become a folder's key anyway would silently start swallowing THAT row's
  // own clicks forever (Tree.tsx's handleRowClick guard is keyed on
  // renamingId alone, with no folder/request distinction of its own — the
  // tree doesn't know what a "folder" is), which is a worse, more confusing
  // failure than simply not entering rename mode.
  const onTreeRenamingChange = (id: string | null): void => {
    if (id === null) {
      setRenamingKey(null);
      return;
    }
    const target = findByKey(filtered, id);
    if (target?.item.content.case === "folder") return;
    setRenamingKey(id);
  };

  // Callbacks renderRequestRow needs beyond ItemWithPath itself — see
  // request-tree.tsx's RequestRowCallbacks for why these can't be derived from the
  // node alone.
  const rowCallbacks: RequestRowCallbacks = {
    services,
    renamingKey,
    onRenamingChange: setRenamingKey,
    onRename: doRename,
    onNewRequestUnder: (folder) => {
      setPickerParent(folder);
      // Mirrors today's setOpen(true) on the folder a new request is added into —
      // expansion is controlled state now, so "open it" means adding its id to
      // treeExpanded instead of flipping local component state.
      setTreeExpanded(new Set([...treeExpanded, itemKey(folder)]));
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
        <IconButton title="New folder" onClick={() => setShowNewFolder(true)}>
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
      <div style={{ flex: 1, overflow: "auto", padding: "8px" }}>
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
            renderRow={renderRow}
            expanded={treeExpanded}
            onExpandedChange={setTreeExpanded}
            selection={treeSelection}
            onSelectionChange={setTreeSelection}
            focused={treeFocused}
            onFocusedChange={setTreeFocused}
            activeId={activeKey}
            renamingId={renamingKey}
            onRenamingChange={onTreeRenamingChange}
            onOpen={openTab}
            onDelete={onTreeDelete}
            aria-label="Collection"
          />
        )}
      </div>

      {/* new folder dialog */}
      <Dialog
        open={showNewFolder}
        onClose={() => setShowNewFolder(false)}
        title="New folder"
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
          <Button onClick={() => setShowNewFolder(false)}>Cancel</Button>
          <Button variant="primary" onClick={submitFolder} disabled={!folderName.trim()}>
            Create
          </Button>
        </div>
      </Dialog>

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
