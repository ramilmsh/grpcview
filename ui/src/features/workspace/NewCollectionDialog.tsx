import { useState, type KeyboardEvent } from "react";
import { Dialog } from "@/components/ui/Dialog";
import { Button } from "@/components/ui/Button";
import { Input, Field } from "@/components/ui/Input";
import { useCreateCollection } from "@/lib/workspace-query";
import { collectionBaseName, normalizeCollectionPath } from "./collection-path";

// Creating a collection in a workspace that already has one. <NoCollection> covers the empty
// workspace with the same two fields inside its empty state; this is the same act from the
// TopBar picker and the tree's context menu, where a dialog is the only place it can live.
export function NewCollectionDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  // No suggested directory: unlike <NoCollection>, nothing here came back not_found, so
  // there is no path the user already asked for — and defaulting to "." would scatter a
  // grpcview.json across the repo root on a stray Enter.
  const [dir, setDir] = useState("");
  const [name, setName] = useState("");
  // Activates the new collection and refreshes the listing itself, so success needs no
  // follow-up beyond closing.
  const createCollection = useCreateCollection();

  const submit = () => {
    if (!dir.trim() || createCollection.isPending) return;
    createCollection.mutate(
      { collection: normalizeCollectionPath(dir), name: name.trim() },
      {
        onSuccess: () => {
          setDir("");
          setName("");
          onClose();
        },
      }
    );
  };

  const onEnter = (e: KeyboardEvent) => {
    if (e.key === "Enter") submit();
  };

  return (
    <Dialog open={open} onClose={onClose} title="New collection" width={380}>
      <Field label="Directory">
        <Input
          autoFocus
          value={dir}
          onChange={(e) => setDir(e.target.value)}
          onKeyDown={onEnter}
          placeholder="e.g. services/payments"
        />
      </Field>
      <span className="text-muted" style={{ fontSize: 12, marginTop: -6, lineHeight: 1.5 }}>
        Workspace-relative. Use “.” for the workspace root. Creating one writes a{" "}
        <code>grpcview.json</code> and a <code>tree/</code> directory there.
      </span>
      <Field label="Name (optional)">
        <Input
          value={name}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={onEnter}
          placeholder={`Defaults to “${collectionBaseName(dir)}”`}
        />
      </Field>

      {createCollection.isError && (
        <p style={{ margin: 0, fontSize: 12, color: "var(--err-fg)" }}>
          {createCollection.error.message}
        </p>
      )}

      <div className="dialog-actions">
        <Button onClick={onClose}>Cancel</Button>
        <Button
          variant="primary"
          onClick={submit}
          disabled={!dir.trim() || createCollection.isPending}
        >
          {createCollection.isPending ? "Creating…" : "Create"}
        </Button>
      </div>
    </Dialog>
  );
}
