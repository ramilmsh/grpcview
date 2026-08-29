import { useState, type KeyboardEvent } from "react";
import { FolderPlus } from "@/components/ui/icons";
import { Button } from "@/components/ui/Button";
import { Input, Field } from "@/components/ui/Input";
import {
  useActiveCollectionId,
  useCreateCollection,
} from "@/lib/workspace-query";
import { collectionBaseName, normalizeCollectionPath } from "./collection-path";

// Replaces the workspace view (and Sources/Scripts, equally meaningless without a
// collection) when the workspace lists none, or when Get comes back not_found for the
// one we address: nothing creates a collection implicitly anymore.
export function NoCollection() {
  const activeCollection = useActiveCollectionId();
  // The directory to offer: the address that just came back not_found, so "create" means
  // "create the collection I asked for". The exceptions are "." and no collection at all
  // — there, offering to scatter grpcview.json across the repo root is a worse guess than
  // suggesting a subdirectory for it.
  const suggestedDir =
    !activeCollection || activeCollection === "."
      ? "requests"
      : activeCollection;

  const [dir, setDir] = useState(suggestedDir);
  const [name, setName] = useState("");
  // The hook activates the new collection and refreshes the listing itself, so success
  // here needs no follow-up (and certainly no reload).
  const createCollection = useCreateCollection();

  const submit = () => {
    createCollection.mutate({
      collection: normalizeCollectionPath(dir),
      name: name.trim(),
    });
  };

  const onEnter = (e: KeyboardEvent) => {
    if (e.key === "Enter") submit();
  };

  return (
    <div
      className="flex flex-col items-center justify-center"
      style={{
        flex: 1,
        minWidth: 0,
        gap: 14,
        padding: 24,
        textAlign: "center",
      }}
    >
      <div
        className="flex items-center justify-center"
        style={{
          width: 48,
          height: 48,
          borderRadius: 13,
          background: "var(--panel-2)",
          border: "1px solid var(--line)",
          color: "var(--color-neutral-500)",
        }}
      >
        <FolderPlus size={24} />
      </div>
      <div style={{ fontSize: 15, color: "var(--color-neutral-200)" }}>
        No collection here
      </div>
      <p
        className="text-muted"
        style={{ fontSize: 13, lineHeight: 1.6, margin: 0, maxWidth: 420 }}
      >
        This workspace has no grpcview collection at this path yet. Creating one
        writes a <code>grpcview.json</code> and a <code>tree/</code> directory
        there.
      </p>

      <div
        className="flex flex-col"
        style={{ gap: 12, width: "100%", maxWidth: 340, textAlign: "left" }}
      >
        <Field label="Directory">
          <Input
            value={dir}
            onChange={(e) => setDir(e.target.value)}
            onKeyDown={onEnter}
            placeholder={suggestedDir}
            autoFocus
          />
        </Field>
        <span className="text-muted" style={{ fontSize: 12, marginTop: -6 }}>
          Workspace-relative. Use “.” for the workspace root.
        </span>
        <Field label="Name (optional)">
          <Input
            value={name}
            onChange={(e) => setName(e.target.value)}
            onKeyDown={onEnter}
            placeholder={`Defaults to “${collectionBaseName(dir)}”`}
          />
        </Field>
      </div>

      {createCollection.isError && (
        <p
          style={{
            margin: 0,
            fontSize: 12,
            color: "var(--err-fg)",
            maxWidth: 340,
          }}
        >
          {createCollection.error.message}
        </p>
      )}

      <Button
        variant="primary"
        onClick={submit}
        disabled={createCollection.isPending}
        style={{ padding: "6px 13px", fontSize: 13, gap: 7 }}
      >
        <FolderPlus size={14} />
        {createCollection.isPending ? "Creating…" : "Create collection"}
      </Button>
    </div>
  );
}
