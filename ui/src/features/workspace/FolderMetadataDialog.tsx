import { useState } from "react";
import { Dialog } from "@/components/ui/Dialog";
import { Button } from "@/components/ui/Button";
import { useWorkspaceMutations, WORKSPACE_NAME } from "@/lib/workspace-query";
import { itemKey, type ItemWithPath } from "@/lib/format";
import { MetadataEditor } from "./MetadataEditor";
import { defaultMetadataModule } from "./metadata-wrapper";
import type { GeneratorDef } from "./generator-libs";

// FolderMetadataDialog (gv-features-plan.md Feature 1 §"Frontend changes", third bullet) hosts
// the existing request <MetadataEditor> to author a FOLDER's metadata script — the same
// canonical `export default (): Metadata => ({ ... })` TS module a request authors, but folded
// into every descendant's `gv.metadata.inherit()` (service/workspace/invoke.go's ancestor-chain
// fold). Opened from a gear button on a folder row (TreeView.tsx).
//
// CollectionPanel renders this component KEYED by the open folder's itemKey (or "none" while
// closed) so a BRAND NEW instance mounts every time a different folder opens, and `draft`'s
// useState initializer re-seeds fresh on that very first render. This sidesteps a stale-seed race
// a same-instance reseed (an effect calling setState after the fact) would hit: MetadataEditor
// only reloads its buffer when ITS OWN `currentKey` prop changes, and that prop changes in the
// SAME render this component mounts in — one render later is already too late. The dialog can
// only be reopened after being closed (its Backdrop blocks clicks on the tree behind it), so a
// fresh mount per open is always correct, never redundant.
export function FolderMetadataDialog({
  folder,
  onClose,
  generators = [],
}: {
  // The folder row being edited, or null when the dialog is closed.
  folder: ItemWithPath | null;
  onClose: () => void;
  // Workspace generators, forwarded to MetadataEditor for the same ambient autocomplete the
  // request metadata editor gets (folder scripts run through the same eval path — D4: uniform
  // capabilities). Optional; defaults to [].
  generators?: GeneratorDef[];
}) {
  const { updateFolder } = useWorkspaceMutations();
  const key = folder ? itemKey(folder) : "none";
  const savedScript =
    folder?.item.content.case === "folder" ? folder.item.content.value.draftMetadataScript : "";

  // The editor's live buffer, seeded from the folder's persisted script — falling back to the
  // transparent-by-default `{ ...gv.metadata.inherit() }` module so an as-yet-unedited folder is
  // transparent-by-default, same as a new request.
  const [draft, setDraft] = useState(() => savedScript || defaultMetadataModule());

  const onSave = () => {
    if (!folder) return;
    updateFolder.mutate(
      {
        workspaceName: WORKSPACE_NAME,
        path: folder.path,
        itemName: folder.item.name,
        draftMetadataScript: draft,
      },
      { onSuccess: onClose }
    );
  };

  return (
    <Dialog
      open={folder !== null}
      onClose={onClose}
      title={folder ? `Folder metadata — ${folder.item.name}` : "Folder metadata"}
      width={640}
    >
      <p className="dialog-body" style={{ marginBottom: 4 }}>
        Inherited by every request and subfolder beneath this one via{" "}
        <code className="font-mono">gv.metadata.inherit()</code>. Ancestor scripts are read from
        the saved workspace, not the live buffer here — edits take effect after you Save, not
        before.
      </p>
      <div
        style={{
          height: 260,
          border: "1px solid var(--line)",
          borderRadius: "var(--radius-md)",
          overflow: "hidden",
        }}
      >
        <MetadataEditor data={draft} onChange={setDraft} currentKey={key} generators={generators} />
      </div>
      <div className="dialog-actions">
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" onClick={onSave} disabled={updateFolder.isPending}>
          {updateFolder.isPending ? "Saving…" : "Save"}
        </Button>
      </div>
    </Dialog>
  );
}
