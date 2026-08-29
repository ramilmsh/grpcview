import { useState } from "react";
import { Dialog } from "@/components/ui/Dialog";
import { Button } from "@/components/ui/Button";
import { useWorkspaceMutations } from "@/lib/workspace-query";
import { itemKey, type ItemWithPath } from "@/lib/format";
import { MetadataEditor } from "./MetadataEditor";
import { defaultMetadataModule } from "./metadata-wrapper";

// Authors a folder's metadata script, which descendants pick up via
// require("grpcview:metadata").inherit(). CollectionPanel must render this KEYED by the
// folder so a fresh instance re-seeds `draft` on mount — MetadataEditor reloads its buffer
// in the same render, so a later reseed is too late.
export function FolderMetadataDialog({
  folder,
  onClose,
}: {
  // The folder row being edited, or null when the dialog is closed.
  folder: ItemWithPath | null;
  onClose: () => void;
}) {
  const { updateFolder } = useWorkspaceMutations();
  const key = folder ? itemKey(folder) : "none";
  const savedScript =
    folder?.item.content.case === "folder"
      ? folder.item.content.value.draftMetadataScript
      : "";

  const [draft, setDraft] = useState(
    () => savedScript || defaultMetadataModule(),
  );

  const onSave = () => {
    if (!folder) return;
    updateFolder.mutate(
      {
        // The folder's own collection, which is by construction the active one — the
        // dialog needs no separate route to a collection id.
        collection: folder.collection,
        path: folder.path,
        itemName: folder.item.name,
        draftMetadataScript: draft,
      },
      { onSuccess: onClose },
    );
  };

  return (
    <Dialog
      open={folder !== null}
      onClose={onClose}
      title={
        folder ? `Folder metadata — ${folder.item.name}` : "Folder metadata"
      }
      width={640}
    >
      <p className="dialog-body" style={{ marginBottom: 4 }}>
        Inherited by every request and subfolder beneath this one via{" "}
        <code className="font-mono">
          require("grpcview:metadata").inherit()
        </code>
        . Ancestor scripts are read from the saved workspace, not the live
        buffer here — edits take effect after you Save, not before.
      </p>
      <div
        style={{
          height: 260,
          border: "1px solid var(--line)",
          borderRadius: "var(--radius-md)",
          overflow: "hidden",
        }}
      >
        <MetadataEditor data={draft} onChange={setDraft} currentKey={key} />
      </div>
      <div className="dialog-actions">
        <Button onClick={onClose}>Cancel</Button>
        <Button
          variant="primary"
          onClick={onSave}
          disabled={updateFolder.isPending}
        >
          {updateFolder.isPending ? "Saving…" : "Save"}
        </Button>
      </div>
    </Dialog>
  );
}
