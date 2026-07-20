import { useMemo, useState } from "react";
import { MagnifyingGlass, FolderPlus, Plus } from "@/components/ui/icons";
import type { Service, Method } from "@grpcview/v1/workspace_pb";
import { IconButton, Button } from "@/components/ui/Button";
import { Dialog } from "@/components/ui/Dialog";
import { Input } from "@/components/ui/Input";
import { useWorkspace, useRootItems, useWorkspaceMutations, WORKSPACE_NAME } from "@/lib/workspace-query";
import { useUIStore } from "@/lib/ui-store";
import { childPathOf, itemKey, serviceName, type ItemWithPath } from "@/lib/format";
import { TreeView } from "./TreeView";
import { MethodPickerModal } from "./MethodPickerModal";

// Count requests in a subtree (for the header + folder counts already handled in
// TreeView).
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
  const { createFolder, createRequest, deleteRequest } = useWorkspaceMutations();
  const openTab = useUIStore((s) => s.openTab);
  const activeKey = useUIStore((s) => s.activeKey);

  const [filter, setFilter] = useState("");
  const [folderName, setFolderName] = useState("");
  const [showNewFolder, setShowNewFolder] = useState(false);
  const [pickerParent, setPickerParent] = useState<ItemWithPath | null | undefined>(undefined);
  const [confirm, setConfirm] = useState<ItemWithPath | null>(null);

  const filtered = useMemo(() => filterTree(rootItems, filter), [rootItems, filter]);
  const total = useMemo(() => countRequests(rootItems), [rootItems]);

  const submitFolder = () => {
    const name = folderName.trim();
    if (name) {
      createFolder.mutate({ workspaceName: WORKSPACE_NAME, path: [], itemName: name });
    }
    setFolderName("");
    setShowNewFolder(false);
  };

  const onPick = (service: Service, method: Method) => {
    // request name = method name (rename is unsupported in Phase 1 — plan §11)
    createRequest.mutate({
      workspaceName: WORKSPACE_NAME,
      path: childPathOf(pickerParent ?? null),
      itemName: method.name,
      service: serviceName(service),
      method: method.name,
    });
    setPickerParent(undefined);
  };

  const doDelete = () => {
    if (confirm) {
      deleteRequest.mutate({
        workspaceName: WORKSPACE_NAME,
        path: confirm.path,
        itemName: confirm.item.name,
      });
    }
    setConfirm(null);
  };

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
          filtered.map((item) => (
            <TreeView
              key={itemKey(item)}
              item={item}
              activeKey={activeKey}
              onSelectRequest={openTab}
              onNewRequestUnder={(folder) => setPickerParent(folder)}
              onDelete={setConfirm}
            />
          ))
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

      {/* delete confirm */}
      <Dialog
        open={confirm !== null}
        onClose={() => setConfirm(null)}
        title={confirm?.item.content.case === "folder" ? "Delete folder" : "Delete request"}
        width={380}
      >
        <p className="dialog-body">
          Delete <strong>{confirm?.item.name}</strong>
          {confirm?.item.content.case === "folder" ? " and everything inside it?" : "?"}
        </p>
        <div className="dialog-actions">
          <Button onClick={() => setConfirm(null)}>Cancel</Button>
          <Button variant="danger" onClick={doDelete}>
            Delete
          </Button>
        </div>
      </Dialog>
    </div>
  );
}
