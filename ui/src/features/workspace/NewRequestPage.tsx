import type { KeyboardEvent } from "react";
import { Folder } from "@/components/ui/icons";
import { Button } from "@/components/ui/Button";
import { Tag } from "@/components/ui/Tag";
import { useUIStore } from "@/lib/ui-store";
import { MethodPicker } from "./MethodPicker";
import { draftTitle } from "./new-request-draft";
import { useRequestDraft } from "./useRequestDraft";

const muted = { color: "var(--color-neutral-500)" };

// The "New request" draft tab's working area. The name is typed in the draft's tree row
// and the folder is wherever that row sits; this page picks the method and creates it.
export function NewRequestPage() {
  const { draft, services, folder, ready, cancel, pick, submit } =
    useRequestDraft();
  const setRequestDraft = useUIStore((s) => s.setRequestDraft);
  if (!draft) return null;

  // Anywhere on the page, as in the row's own input.
  const onKeyDown = (e: KeyboardEvent) => {
    if (e.key === "Escape") {
      e.preventDefault();
      cancel();
    }
  };

  return (
    <div
      role="group"
      aria-label="New request"
      onKeyDown={onKeyDown}
      className="flex flex-col"
      style={{ flex: 1, minHeight: 0 }}
    >
      <div style={{ padding: "18px 24px 14px" }}>
        <div className="flex items-center gap-[10px]" style={{ minWidth: 0 }}>
          <span
            className="truncate"
            style={{
              fontSize: 20,
              fontWeight: 600,
              color: "var(--color-text)",
            }}
          >
            {draftTitle(draft.name)}
          </span>
          <Tag variant="accent">Draft</Tag>
        </div>
        <div style={{ fontSize: 12.5, marginTop: 4, ...muted }}>
          Name it in the tree, then pick the method it calls.
        </div>
      </div>

      <MethodPicker
        services={services}
        selected={draft.method}
        query={draft.query}
        onQueryChange={(query) => setRequestDraft({ query })}
        onPick={pick}
        searchVariant="field"
      />

      {draft.error && (
        <div
          role="alert"
          style={{
            margin: "0 24px 10px",
            padding: "8px 10px",
            borderRadius: 8,
            fontSize: 13,
            background: "var(--err-bg)",
            border: "1px solid var(--err-border)",
            color: "var(--err-fg)",
          }}
        >
          {draft.error}
        </div>
      )}

      <div
        className="flex items-center gap-[8px]"
        style={{
          flex: "none",
          padding: "10px 24px",
          borderTop: "1px solid var(--line)",
          fontSize: 12.5,
        }}
      >
        <Folder size={14} style={{ ...muted, flex: "none" }} />
        <span
          className="truncate"
          style={{ color: "var(--color-neutral-300)" }}
        >
          In {folder}
        </span>
        <span style={muted}>·</span>
        {draft.method ? (
          <span
            className="font-mono truncate"
            style={{ color: "var(--color-neutral-300)", minWidth: 0 }}
          >
            {draft.method.service}/{draft.method.method}
          </span>
        ) : (
          <span style={muted}>No method selected</span>
        )}
        <Button className="ml-auto" onClick={cancel}>
          Cancel
        </Button>
        <Button
          variant="primary"
          onClick={() => void submit()}
          disabled={!ready}
        >
          {draft.pending ? "Creating…" : "Create request"}
        </Button>
      </div>
    </div>
  );
}
