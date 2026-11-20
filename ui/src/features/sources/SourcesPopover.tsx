import { useEffect, useRef, useState } from "react";
import { ArrowClockwise, Warning } from "@/components/ui/icons";
import { IconButton } from "@/components/ui/Button";
import {
  useActiveWorkspace,
  useWorkspaceMutations,
} from "@/lib/workspace-query";
import { useUIStore } from "@/lib/ui-store";
import type { DescriptorSource } from "@grpcview/v1/workspace_pb";
import {
  COMMITTED_LABEL,
  contribution,
  KIND_ICON,
  KIND_TINT,
  refreshTitle,
  sourceKind,
  sourceLabel,
} from "./source-row";
import {
  errorSummary,
  KIND_LABEL,
  refreshAllIds,
  TARGET_TITLE,
  targetSourceId,
} from "./sources-popover";

// The top bar's definition-sources dropdown: each source's status and a refresh, nothing the
// Sources page doesn't already say. Placement, outside-click and Escape are the trigger's.
export function SourcesPopover({ onClose }: { onClose: () => void }) {
  const { collection, sources } = useActiveWorkspace();
  const { refreshDescriptorSource: refresh } = useWorkspaceMutations();
  const setView = useUIStore((s) => s.setView);
  const [all, setAll] = useState(false);
  // Focus the card on open, so Tab walks its buttons next.
  const cardRef = useRef<HTMLDivElement>(null);
  useEffect(() => cardRef.current?.focus(), []);
  const targetId = targetSourceId(sources);
  // One mutation, so one refresh at a time: its `variables` name the row that asked.
  const pending = refresh.isPending || all;
  const refreshingId = refresh.isPending ? refresh.variables?.id : undefined;

  const refreshOne = (id: string) =>
    refresh.mutate({ collection: collection ?? "", id });
  // Sequential: each refresh rewrites the collection's descriptor state.
  const refreshAll = async () => {
    setAll(true);
    try {
      for (const id of refreshAllIds(sources)) {
        await refresh
          .mutateAsync({ collection: collection ?? "", id })
          .catch(() => {});
      }
    } finally {
      setAll(false);
    }
  };

  return (
    <div
      className="srcpop"
      role="dialog"
      aria-label="Definition sources"
      tabIndex={-1}
      ref={cardRef}
    >
      <div className="flex items-center" style={{ padding: "4px 4px 6px 8px" }}>
        <span className="srcpop-caps">Definition sources</span>
        {sources.length > 0 && (
          <button
            type="button"
            className="srcpop-btn ml-auto"
            onClick={() => void refreshAll()}
            disabled={pending}
            title="Refresh every source, top to bottom"
          >
            <ArrowClockwise size={13} className={all ? "spin" : undefined} />
            All
          </button>
        )}
      </div>

      {refresh.isError && (
        <div className="srcpop-err" style={{ margin: "0 8px 6px" }}>
          <Warning weight="fill" style={{ flex: "none" }} />
          <span className="truncate" title={refresh.error.message}>
            {refresh.error.message}
          </span>
        </div>
      )}

      {sources.length === 0 ? (
        <div style={{ padding: "8px", fontSize: 13, color: "var(--v-text-2)" }}>
          No definition sources yet.
        </div>
      ) : (
        <div className="flex flex-col">
          {sources.map((s) => (
            <Row
              key={s.id}
              source={s}
              target={s.id === targetId}
              refreshing={refreshingId === s.id}
              disabled={pending}
              onRefresh={() => refreshOne(s.id)}
            />
          ))}
        </div>
      )}

      <div className="menusep" role="separator" />
      <button
        type="button"
        className="srcpop-btn"
        style={{ width: "100%", height: 36, padding: "0 8px" }}
        onClick={() => {
          setView("sources");
          onClose();
        }}
      >
        Manage sources…
      </button>
    </div>
  );
}

function Row({
  source: s,
  target,
  refreshing,
  disabled,
  onRefresh,
}: {
  source: DescriptorSource;
  target: boolean;
  refreshing: boolean;
  disabled: boolean;
  onRefresh: () => void;
}) {
  const [open, setOpen] = useState(false);
  const kind = sourceKind(s);
  const label = sourceLabel(s);
  const info = contribution(s);
  const error = s.resolved?.error ?? "";
  const summary = error ? errorSummary(error) : null;
  const Icon = KIND_ICON[kind];

  return (
    <div style={{ padding: "4px 0 4px 8px" }}>
      <div className="flex items-center gap-[10px]">
        <Icon
          size={16}
          style={{
            flex: "none",
            color: error ? "var(--v-err)" : KIND_TINT[kind],
          }}
        />
        <div style={{ minWidth: 0, flex: 1 }}>
          <div className="flex items-baseline gap-[8px]">
            <span
              className="font-mono truncate"
              style={{ fontSize: 13, color: "var(--v-text)", minWidth: 0 }}
              title={label}
            >
              {label}
            </span>
            <span
              style={{
                flex: "none",
                marginLeft: "auto",
                fontSize: 12,
                color:
                  kind === "reference" ? "var(--v-warn)" : "var(--v-text-2)",
              }}
            >
              {KIND_LABEL[kind]}
              {target && <span title={TARGET_TITLE}> · target</span>}
            </span>
          </div>
          {!error && (
            <div
              className="truncate"
              style={{
                fontSize: 12,
                color:
                  info.tone === "ok"
                    ? "var(--v-text-2)"
                    : "var(--v-text-muted)",
              }}
            >
              {info.text}
              {s.commitDescriptors && ` · ${COMMITTED_LABEL}`}
            </div>
          )}
        </div>
        <IconButton
          title={refreshing ? "Refreshing…" : refreshTitle(s)}
          aria-label={refreshing ? `Refreshing ${label}` : `Refresh ${label}`}
          onClick={onRefresh}
          disabled={disabled}
        >
          <ArrowClockwise
            size={14}
            className={refreshing ? "spin" : undefined}
          />
        </IconButton>
      </div>
      {summary && (
        <div style={{ paddingLeft: 26, paddingRight: 8 }}>
          <div className="srcpop-err">
            <Warning weight="fill" style={{ flex: "none" }} />
            <span className="truncate" style={{ minWidth: 0 }} title={error}>
              {summary.line}
            </span>
            {summary.more && (
              <button
                type="button"
                className="srcpop-btn ml-auto"
                style={{ flex: "none" }}
                aria-expanded={open}
                onClick={() => setOpen((o) => !o)}
              >
                {open ? "Hide error" : "Show error"}
              </button>
            )}
          </div>
          {open && <pre className="logview srcpop-log">{error}</pre>}
        </div>
      )}
    </div>
  );
}
