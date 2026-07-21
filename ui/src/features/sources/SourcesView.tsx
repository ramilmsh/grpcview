import { useState } from "react";
import { Plus, PlugsConnected, FileArchive, Warning, Trash } from "@/components/ui/icons";
import { Button, IconButton } from "@/components/ui/Button";
import { Tag } from "@/components/ui/Tag";
import { Dialog } from "@/components/ui/Dialog";
import { useWorkspace, useWorkspaceMutations, hostLabel, WORKSPACE_NAME } from "@/lib/workspace-query";
import type { DescriptorSource } from "@grpcview/v1/workspace_pb";
import { AddSourceModal } from "./AddSourceModal";

// sourceLabel is the human-readable handle shown for a source in the
// remove-confirm dialog.
function sourceLabel(s: DescriptorSource): string {
  const reflection = s.source.case === "reflection" ? s.source.value : null;
  return reflection ? `reflection:${hostLabel(reflection)}` : "descriptor set";
}

// SourcesView is the minimal Phase-1 definition-sources list (plan §1.7): the
// current sources plus an "Add source" action and a per-row remove. Priority/
// freshness/versions/collisions and the full table wait for Phase 2 (needs
// backend work).
export function SourcesView() {
  const { sources } = useWorkspace();
  const { addDescriptorSource, removeDescriptorSource } = useWorkspaceMutations();
  const [modalOpen, setModalOpen] = useState(false);
  // confirm holds the index of the source pending removal, or null.
  const [confirm, setConfirm] = useState<number | null>(null);

  const onAdd = (host: string, port: number, tls: boolean) => {
    addDescriptorSource.mutate(
      {
        workspaceName: WORKSPACE_NAME,
        source: { case: "reflection", value: { host, port, tls: tls ? {} : undefined } },
      },
      { onSuccess: () => setModalOpen(false) }
    );
  };

  const doRemove = () => {
    if (confirm === null) return;
    removeDescriptorSource.mutate(
      { workspaceName: WORKSPACE_NAME, index: confirm },
      { onSuccess: () => setConfirm(null) }
    );
  };

  // Show whichever mutation last errored (add or remove).
  const activeError = addDescriptorSource.isError
    ? addDescriptorSource.error
    : removeDescriptorSource.isError
      ? removeDescriptorSource.error
      : null;

  return (
    <div className="flex flex-col" style={{ flex: 1, minWidth: 0, minHeight: 0 }}>
      <div
        className="flex items-center gap-[12px]"
        style={{ flex: "none", padding: "14px 20px", borderBottom: "1px solid var(--line)" }}
      >
        <div>
          <h4 style={{ margin: 0 }}>Definition sources</h4>
          <span className="text-muted" style={{ fontSize: 12 }}>
            Reflection is the only source type wired in Phase 1.
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
            No definition sources yet. Add a server-reflection source to load its
            services and schemas.
          </div>
        ) : (
          <div className="flex flex-col" style={{ gap: 8, maxWidth: 640 }}>
            {sources.map((s, i) => {
              const reflection = s.source.case === "reflection" ? s.source.value : null;
              return (
                <div
                  key={i}
                  className="flex items-center gap-[11px]"
                  style={{
                    padding: "11px 13px",
                    background: "var(--panel-2)",
                    border: "1px solid var(--line)",
                    borderRadius: 9,
                  }}
                >
                  {reflection ? (
                    <PlugsConnected size={18} style={{ color: "var(--ok)" }} />
                  ) : (
                    <FileArchive size={18} style={{ color: "var(--color-neutral-400)" }} />
                  )}
                  <div style={{ minWidth: 0, flex: 1 }}>
                    <div style={{ fontSize: 13, color: "var(--color-text)" }}>
                      {reflection ? `reflection:${reflection.host}` : "descriptor set"}
                    </div>
                    <div
                      className="font-mono"
                      style={{ fontSize: 11, color: "var(--color-neutral-500)" }}
                    >
                      {reflection ? hostLabel(reflection) : "uploaded bytes"}
                    </div>
                  </div>
                  {reflection ? (
                    <Tag variant="accent">reflection</Tag>
                  ) : (
                    <Tag variant="neutral">descriptor set</Tag>
                  )}
                  <IconButton
                    title="Remove source"
                    onClick={() => setConfirm(i)}
                    disabled={removeDescriptorSource.isPending}
                  >
                    <Trash size={15} />
                  </IconButton>
                </div>
              );
            })}
          </div>
        )}
      </div>

      <AddSourceModal
        open={modalOpen}
        onClose={() => setModalOpen(false)}
        onAddReflection={onAdd}
        pending={addDescriptorSource.isPending}
      />

      {/* remove confirm */}
      <Dialog
        open={confirm !== null}
        onClose={() => setConfirm(null)}
        title="Remove source"
        width={400}
      >
        <p style={{ margin: 0, fontSize: 13, lineHeight: 1.6 }}>
          Remove{" "}
          <strong>{confirm !== null && sources[confirm] ? sourceLabel(sources[confirm]) : "this source"}</strong>?
          Its services are re-resolved from the remaining sources.
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
