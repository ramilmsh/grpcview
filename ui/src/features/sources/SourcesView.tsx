import { useState } from "react";
import {
  Plus,
  PlugsConnected,
  FileArchive,
  Warning,
  Trash,
  ArrowClockwise,
  CaretUp,
  CaretDown,
} from "@/components/ui/icons";
import { Button, IconButton } from "@/components/ui/Button";
import { Tag } from "@/components/ui/Tag";
import { Dialog } from "@/components/ui/Dialog";
import { useWorkspace, useWorkspaceMutations, hostLabel, WORKSPACE_NAME } from "@/lib/workspace-query";
import type { DescriptorSource } from "@grpcview/v1/workspace_pb";
import { AddSourceModal } from "./AddSourceModal";

// sourceLabel is the human-readable handle for a source: its dial target, or the
// name of the file it was uploaded from.
function sourceLabel(s: DescriptorSource): string {
  if (s.source.case === "reflection") return hostLabel(s.source.value);
  if (s.source.case === "upload") return s.source.value.fileName;
  return s.id;
}

// contribution describes, in one line, what a source actually provides once the
// priority merge has run. A source can define services and still win none of them
// (a higher-priority source describes the same protos) — saying so explicitly is
// the point of the list: it's what makes "which source am I using" answerable
// instead of guesswork.
function contribution(s: DescriptorSource): { text: string; tone: "ok" | "muted" | "warn" } {
  const r = s.resolved;
  if (r?.error) return { text: r.error, tone: "warn" };
  if (!r) return { text: "not resolved yet", tone: "muted" };
  const defined = r.serviceNames.length;
  const won = r.wonServiceNames.length;
  const files = `${r.fileCount} file${r.fileCount === 1 ? "" : "s"}`;
  if (defined === 0) return { text: `${files}, no services`, tone: "muted" };
  if (won === 0) {
    // "all N" reads wrong for a single service, and a fully shadowed one-service
    // source is the common case when an upload and its live server overlap.
    const shadowed = defined === 1 ? "its 1 service" : `all ${defined} services`;
    return { text: `${files}, ${shadowed} shadowed`, tone: "muted" };
  }
  if (won < defined) {
    return { text: `${files}, ${won} of ${defined} services`, tone: "ok" };
  }
  return { text: `${files}, ${won} service${won === 1 ? "" : "s"}`, tone: "ok" };
}

// SourcesView lists the workspace's definition sources IN PRIORITY ORDER and lets
// you add, refresh, reorder, and remove them.
//
// The order is the product feature, not decoration: when two sources describe the
// same protos — a buf-built descriptor set and the live server that serves them —
// the higher one's definitions win, so moving a source up is how you switch which
// one the editor and method picker resolve against. That matters because gRPC
// reflection strips proto doc comments while a buf image keeps them.
export function SourcesView() {
  const { sources } = useWorkspace();
  const {
    addDescriptorSource,
    removeDescriptorSource,
    refreshDescriptorSource,
    reorderDescriptorSources,
  } = useWorkspaceMutations();
  const [modalOpen, setModalOpen] = useState(false);
  // confirm holds the source pending removal, or null.
  const [confirm, setConfirm] = useState<DescriptorSource | null>(null);

  const onAdd = (address: string, tls: boolean) => {
    addDescriptorSource.mutate(
      {
        workspaceName: WORKSPACE_NAME,
        source: { case: "reflection", value: { address, tls: tls ? {} : undefined } },
      },
      { onSuccess: () => setModalOpen(false) }
    );
  };

  // fileName rides along as the upload's identity, so re-uploading a rebuilt image
  // refreshes that source in place instead of adding an indistinguishable row.
  const onAddDescriptorSet = (bytes: Uint8Array, fileName: string) => {
    addDescriptorSource.mutate(
      { workspaceName: WORKSPACE_NAME, source: { case: "descriptorSet", value: bytes }, fileName },
      { onSuccess: () => setModalOpen(false) }
    );
  };

  const doRemove = () => {
    if (!confirm) return;
    removeDescriptorSource.mutate(
      { workspaceName: WORKSPACE_NAME, id: confirm.id },
      { onSuccess: () => setConfirm(null) }
    );
  };

  // Moving a source sends the whole reordered id list — the backend requires a full
  // permutation, so a stale client can't silently drop a source.
  const move = (from: number, to: number) => {
    if (to < 0 || to >= sources.length) return;
    const ids = sources.map((s) => s.id);
    const [id] = ids.splice(from, 1);
    ids.splice(to, 0, id);
    reorderDescriptorSources.mutate({ workspaceName: WORKSPACE_NAME, ids });
  };

  const busy =
    addDescriptorSource.isPending ||
    removeDescriptorSource.isPending ||
    refreshDescriptorSource.isPending ||
    reorderDescriptorSources.isPending;

  // Show whichever mutation last errored.
  const activeError =
    [addDescriptorSource, removeDescriptorSource, refreshDescriptorSource, reorderDescriptorSources]
      .find((m) => m.isError)?.error ?? null;

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
            {sources.map((s, i) => {
              const reflection = s.source.case === "reflection" ? s.source.value : null;
              const info = contribution(s);
              const toneColor =
                info.tone === "warn"
                  ? "var(--err-fg)"
                  : info.tone === "ok"
                    ? "var(--color-neutral-500)"
                    : "var(--color-neutral-600)";
              return (
                <div
                  key={s.id}
                  className="flex items-center gap-[11px]"
                  style={{
                    padding: "11px 13px",
                    background: "var(--panel-2)",
                    border: "1px solid var(--line)",
                    borderRadius: 9,
                  }}
                >
                  {/* Priority position — the number the reorder controls change. */}
                  <span
                    className="font-mono"
                    style={{ fontSize: 11, color: "var(--color-neutral-600)", width: "2ch" }}
                  >
                    {i + 1}
                  </span>
                  {/* A source that failed to resolve must not wear the green
                      "connected" plug — the icon is the first thing scanned, so it
                      says the same thing as the reason line under it. */}
                  {reflection ? (
                    <PlugsConnected
                      size={18}
                      style={{ color: s.resolved?.error ? "var(--err-fg)" : "var(--ok)" }}
                    />
                  ) : (
                    <FileArchive
                      size={18}
                      style={{
                        color: s.resolved?.error ? "var(--err-fg)" : "var(--color-neutral-400)",
                      }}
                    />
                  )}
                  <div style={{ minWidth: 0, flex: 1 }}>
                    <div
                      className="font-mono"
                      style={{ fontSize: 13, color: "var(--color-text)" }}
                    >
                      {sourceLabel(s)}
                    </div>
                    <div style={{ fontSize: 11, color: toneColor }}>{info.text}</div>
                  </div>
                  {reflection ? (
                    <Tag variant="accent">reflection</Tag>
                  ) : (
                    <Tag variant="neutral">descriptor set</Tag>
                  )}
                  <div className="flex items-center">
                    <IconButton
                      title="Raise priority"
                      aria-label={`Raise priority of ${sourceLabel(s)}`}
                      onClick={() => move(i, i - 1)}
                      disabled={busy || i === 0}
                    >
                      <CaretUp size={14} />
                    </IconButton>
                    <IconButton
                      title="Lower priority"
                      aria-label={`Lower priority of ${sourceLabel(s)}`}
                      onClick={() => move(i, i + 1)}
                      disabled={busy || i === sources.length - 1}
                    >
                      <CaretDown size={14} />
                    </IconButton>
                  </div>
                  <IconButton
                    title={
                      reflection
                        ? "Re-reflect this target"
                        : "Re-link this descriptor set"
                    }
                    aria-label={`Refresh ${sourceLabel(s)}`}
                    onClick={() =>
                      refreshDescriptorSource.mutate({ workspaceName: WORKSPACE_NAME, id: s.id })
                    }
                    disabled={busy}
                  >
                    <ArrowClockwise size={15} />
                  </IconButton>
                  <IconButton
                    title="Remove source"
                    aria-label={`Remove ${sourceLabel(s)}`}
                    onClick={() => setConfirm(s)}
                    disabled={busy}
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
        onAddDescriptorSet={onAddDescriptorSet}
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
          Remove <strong>{confirm ? sourceLabel(confirm) : "this source"}</strong>? The
          workspace's definitions are re-derived from the sources that remain.
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
