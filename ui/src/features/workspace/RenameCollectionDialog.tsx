import { useEffect, useState, type KeyboardEvent } from "react";
import { Dialog } from "@/components/ui/Dialog";
import { Button } from "@/components/ui/Button";
import { Input, Field } from "@/components/ui/Input";
import { useUpdateCollection } from "@/lib/workspace-query";
import { collectionBaseName, normalizeCollectionPath } from "./collection-path";

// Editing the active collection's display name and its directory — which is also its id, so
// changing it MOVES it on disk. Both in one dialog because both live in the same manifest and
// the RPC takes them together.
export function RenameCollectionDialog({
  open,
  onClose,
  collection,
  name,
}: {
  open: boolean;
  onClose: () => void;
  collection: string;
  name: string;
}) {
  const [nextName, setNextName] = useState(name);
  const [dir, setDir] = useState(collection);
  const update = useUpdateCollection();

  // The TopBar keeps this mounted across opens (as it does NewCollectionDialog), so props
  // cannot seed the fields once at mount: they are re-seeded on every open, and whenever the
  // collection being edited changes underneath a closed dialog.
  useEffect(() => {
    if (!open) return;
    setNextName(name);
    setDir(collection);
  }, [open, collection, name]);

  const trimmedName = nextName.trim();
  const nextDir = normalizeCollectionPath(dir);
  // Only what actually changed is sent: the RPC's fields are proto3-optional, and an omitted
  // one means "leave it alone" while an empty `name` means "reset to the base name".
  const nameChanged = trimmedName !== name;
  const dirChanged = nextDir !== collection;
  const unchanged = !nameChanged && !dirChanged;

  const submit = () => {
    if (update.isPending) return;
    if (unchanged) {
      onClose();
      return;
    }
    update.mutate(
      {
        collection,
        ...(nameChanged ? { name: trimmedName } : {}),
        ...(dirChanged ? { newCollection: nextDir } : {}),
      },
      { onSuccess: () => onClose() },
    );
  };

  const onEnter = (e: KeyboardEvent) => {
    if (e.key === "Enter") submit();
  };

  return (
    <Dialog open={open} onClose={onClose} title="Rename collection" width={380}>
      <Field label="Name">
        <Input
          autoFocus
          value={nextName}
          onChange={(e) => setNextName(e.target.value)}
          onKeyDown={onEnter}
          placeholder={`Defaults to “${collectionBaseName(dir)}”`}
        />
      </Field>
      <Field label="Directory">
        <Input
          value={dir}
          onChange={(e) => setDir(e.target.value)}
          onKeyDown={onEnter}
          placeholder="e.g. services/payments"
        />
      </Field>
      <span
        className="text-muted"
        style={{ fontSize: 12, marginTop: -6, lineHeight: 1.5 }}
      >
        Workspace-relative. Changing it{" "}
        <strong>moves the directory on disk</strong>. Neither the current nor
        the new path may be “<code>.</code>”, the workspace root.
      </span>

      {update.isError && (
        <p style={{ margin: 0, fontSize: 12, color: "var(--err-fg)" }}>
          {update.error.message}
        </p>
      )}

      <div className="dialog-actions">
        <Button onClick={onClose}>Cancel</Button>
        <Button
          variant="primary"
          onClick={submit}
          disabled={unchanged || update.isPending}
        >
          {update.isPending ? "Saving…" : "Save"}
        </Button>
      </div>
    </Dialog>
  );
}
