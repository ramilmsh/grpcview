import type { KeyboardEvent } from "react";
import { Folder } from "@/components/ui/icons";
import { Button } from "@/components/ui/Button";
import { DraftBadge } from "@/components/ui/Tag";
import { useUIStore } from "@/lib/ui-store";
import { MethodPicker } from "./MethodPicker";
import { draftTitle } from "./new-request-draft";
import { useRequestDraft } from "./useRequestDraft";

const muted = { color: "var(--v-text-2)" };

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
      <div style={{ padding: "14px 16px 10px" }}>
        <div className="flex items-center gap-[10px]" style={{ minWidth: 0 }}>
          <span
            className="truncate"
            style={{
              fontSize: 18,
              fontWeight: 600,
              color: "var(--v-text)",
            }}
          >
            {draftTitle(draft.name)}
          </span>
          <DraftBadge />
        </div>
        <div style={{ fontSize: 12, ...muted }}>
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
            margin: "0 16px 10px",
            padding: "8px 10px",
            borderRadius: "var(--v-r)",
            fontSize: 13,
            background: "var(--v-err-bg)",
            boxShadow: "0 0 0 1px var(--v-err-ring)",
            color: "var(--v-text)",
          }}
        >
          {draft.error}
        </div>
      )}

      <div
        className="flex items-center gap-[8px]"
        style={{
          flex: "none",
          padding: "10px 16px",
          borderTop: "1px solid var(--v-line)",
          fontSize: 13,
        }}
      >
        <Folder size={14} style={{ ...muted, flex: "none" }} />
        <span className="truncate" style={{ color: "var(--v-text-2)" }}>
          In <span style={{ color: "var(--v-text)" }}>{folder}</span>
        </span>
        <span style={muted}>·</span>
        {draft.method ? (
          <span
            className="font-mono truncate"
            style={{ color: "var(--v-text)", fontSize: 12, minWidth: 0 }}
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
