import { useState } from "react";
import { Plus, Warning } from "@/components/ui/icons";
import { Button } from "@/components/ui/Button";
import { Dialog } from "@/components/ui/Dialog";
import { useActiveWorkspace, useWorkspaceMutations } from "@/lib/workspace-query";
import type { DescriptorSource } from "@grpcview/v1/workspace_pb";
import { AddSourceModal } from "./AddSourceModal";
import { removeConsequence, SourceRow, sourceLabel } from "./source-row";

// SourcesView lists the definition sources in priority order — the highest wins.
export function SourcesView() {
  // Scoped to the active collection: each collection has its own source list, and the
  // merged descriptor set it produces is what the collection's requests resolve against.
  const { collection: activeCollection, sources } = useActiveWorkspace();
  // Non-null everywhere this view renders (App gates on the collection listing).
  const collection = activeCollection ?? "";
  const {
    addDescriptorSource,
    removeDescriptorSource,
    refreshDescriptorSource,
    reorderDescriptorSources,
    setDescriptorSourceCommit,
  } = useWorkspaceMutations();
  const [modalOpen, setModalOpen] = useState(false);
  const [confirm, setConfirm] = useState<DescriptorSource | null>(null);

  const onAdd = (address: string, tls: boolean, commitDescriptors: boolean) => {
    addDescriptorSource.mutate(
      {
        collection,
        source: { case: "reflection", value: { address, tls: tls ? {} : undefined } },
        commitDescriptors,
      },
      { onSuccess: () => setModalOpen(false) }
    );
  };

  const onAddDescriptorSet = (
    bytes: Uint8Array,
    fileName: string,
    commitDescriptors: boolean
  ) => {
    addDescriptorSource.mutate(
      { collection, source: { case: "descriptorSet", value: bytes }, fileName, commitDescriptors },
      { onSuccess: () => setModalOpen(false) }
    );
  };

  const doRemove = () => {
    if (!confirm) return;
    removeDescriptorSource.mutate(
      { collection, id: confirm.id },
      { onSuccess: () => setConfirm(null) }
    );
  };

  // The backend requires the full permutation of ids, not a single move.
  const move = (from: number, to: number) => {
    if (to < 0 || to >= sources.length) return;
    const ids = sources.map((s) => s.id);
    const [id] = ids.splice(from, 1);
    ids.splice(to, 0, id);
    reorderDescriptorSources.mutate({ collection, ids });
  };

  const busy =
    addDescriptorSource.isPending ||
    removeDescriptorSource.isPending ||
    refreshDescriptorSource.isPending ||
    reorderDescriptorSources.isPending ||
    setDescriptorSourceCommit.isPending;

  const activeError =
    [
      addDescriptorSource,
      removeDescriptorSource,
      refreshDescriptorSource,
      reorderDescriptorSources,
      // Committing a source that has never resolved is refused, naming refresh as the fix —
      // the banner is where that answer has to land.
      setDescriptorSourceCommit,
    ].find((m) => m.isError)?.error ?? null;

  return (
    <div className="flex flex-col" style={{ flex: 1, minWidth: 0, minHeight: 0 }}>
      <div
        className="flex items-center gap-[12px]"
        style={{ flex: "none", padding: "14px 20px", borderBottom: "1px solid var(--line)" }}
      >
        <div>
          <h4 style={{ margin: 0 }}>Definition sources</h4>
          <span className="text-muted" style={{ fontSize: 12 }}>
            Highest priority first — when sources share protos, the top one wins.
          </span>
        </div>
        <Button
          variant="primary"
          className="ml-auto"
          style={{ padding: "6px 13px", fontSize: 13, gap: 7 }}
          onClick={() => setModalOpen(true)}
        >
          <Plus size={14} />
          Add source
        </Button>
      </div>

      <div style={{ flex: 1, overflow: "auto", padding: "14px 20px" }}>
        {activeError && (
          <div
            className="flex items-center gap-[8px]"
            style={{
              marginBottom: 14,
              padding: "10px 12px",
              borderRadius: 8,
              fontSize: 13,
              background: "var(--err-bg)",
              border: "1px solid var(--err-border)",
              color: "var(--err-fg)",
            }}
          >
            <Warning weight="fill" />
            {activeError instanceof Error ? activeError.message : "Source operation failed"}
          </div>
        )}

        {sources.length === 0 ? (
          <div className="text-muted" style={{ fontSize: 13, padding: "16px 0", lineHeight: 1.6 }}>
            No definition sources yet. Add a server-reflection target or upload a
            descriptor set to load its services and schemas.
          </div>
        ) : (
          <div className="flex flex-col" style={{ gap: 8, maxWidth: 720 }}>
            {sources.map((s, i) => (
              <SourceRow
                key={s.id}
                source={s}
                index={i}
                count={sources.length}
                busy={busy}
                cb={{
                  onMove: move,
                  onRefresh: (src) =>
                    refreshDescriptorSource.mutate({ collection, id: src.id }),
                  onRemove: setConfirm,
                  onSetCommit: (src, commit) =>
                    setDescriptorSourceCommit.mutate({ collection, id: src.id, commit }),
                }}
              />
            ))}
          </div>
        )}
      </div>

      <AddSourceModal
        open={modalOpen}
        onClose={() => setModalOpen(false)}
        onAddReflection={onAdd}
        onAddDescriptorSet={onAddDescriptorSet}
        pending={addDescriptorSource.isPending}
      />

      <Dialog
        open={confirm !== null}
        onClose={() => setConfirm(null)}
        title="Remove source"
        width={400}
      >
        <p style={{ margin: 0, fontSize: 13, lineHeight: 1.6 }}>
          Remove <strong>{confirm ? sourceLabel(confirm) : "this source"}</strong>?{" "}
          {confirm ? removeConsequence(confirm) : ""}
        </p>
        <div className="dialog-actions">
          <Button onClick={() => setConfirm(null)}>Cancel</Button>
          <Button variant="danger" onClick={doRemove} disabled={removeDescriptorSource.isPending}>
            {removeDescriptorSource.isPending ? "Removing…" : "Remove"}
          </Button>
        </div>
      </Dialog>
    </div>
  );
}
