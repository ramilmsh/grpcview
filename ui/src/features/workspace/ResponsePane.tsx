import { useMemo } from "react";
import { Copy, DownloadSimple } from "@/components/ui/icons";
import { IconButton } from "@/components/ui/Button";
import { Centered } from "@/components/ui/Centered";
import { Subtab } from "@/components/ui/Subtab";
import { Tag } from "@/components/ui/Tag";
import { useUIStore } from "@/lib/ui-store";
import type { InvokeState } from "@/lib/ui-store";
import {
  codeName,
  latencyLabel,
  timestampLabel,
  prettyBody,
  metadataEntries,
} from "@/lib/format";
import { JsonViewer } from "./JsonViewer";

// ResponsePane renders the unary Invoke result: status bar, body, and combined
// request+response metadata (the backend merges header+trailer, so no separate
// Trailers tab — plan §1.5). No streaming indicators (unary only).
export function ResponsePane({ invoke }: { invoke?: InvokeState }) {
  const subtab = useUIStore((s) => s.responseSubtab);
  const setSubtab = useUIStore((s) => s.setResponseSubtab);
  const response = invoke?.response;

  const pretty = useMemo(
    () => (response ? prettyBody(response.response) : ""),
    [response]
  );

  if (invoke?.loading) {
    return <Centered>Invoking…</Centered>;
  }

  // grpcview-side error (unreachable schema, bad body, no target) — distinct from
  // a gRPC status failure, which arrives as response data below.
  if (invoke?.error) {
    return (
      <div style={{ padding: 16, overflow: "auto" }}>
        <div
          style={{ fontSize: 11, textTransform: "uppercase", fontWeight: 600, color: "var(--err)", marginBottom: 6 }}
        >
          Error
        </div>
        <pre
          className="font-mono"
          style={{
            fontSize: 12,
            color: "var(--err-fg)",
            background: "var(--err-bg)",
            border: "1px solid var(--err-border)",
            borderRadius: 8,
            padding: 12,
            whiteSpace: "pre-wrap",
            wordBreak: "break-word",
          }}
        >
          {invoke.error}
        </pre>
      </div>
    );
  }

  if (!response) {
    return <Centered>No response yet. Click Invoke.</Centered>;
  }

  const code = response.status?.code ?? 0;
  const ok = code === 0;
  const reqMd = metadataEntries(response.requestMetadata as Record<string, unknown> | undefined);
  const resMd = metadataEntries(response.responseMetadata as Record<string, unknown> | undefined);

  const copyBody = () => navigator.clipboard?.writeText(pretty);
  const downloadBody = () => {
    const blob = new Blob([pretty], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "response.json";
    a.click();
    URL.revokeObjectURL(url);
  };

  return (
    <div className="flex flex-col" style={{ flex: 1, minWidth: 0, minHeight: 0 }}>
      {/* status bar */}
      <div
        className="flex items-center gap-[10px]"
        style={{
          flex: "none",
          padding: "8px 14px",
          borderBottom: "1px solid var(--line)",
          background: "var(--color-bg)",
        }}
      >
        <span
          className="tag font-mono"
          style={{
            fontWeight: 600,
            background: ok ? "var(--ok-bg)" : "var(--err-bg)",
            color: ok ? "var(--ok)" : "var(--err)",
          }}
        >
          {code} {codeName(code)}
        </span>
        {response.latency && (
          <span className="font-mono" style={{ fontSize: 12, color: "var(--color-neutral-500)" }}>
            {latencyLabel(response.latency)}
          </span>
        )}
        {response.timestamp && (
          <span className="font-mono" style={{ fontSize: 12, color: "var(--color-neutral-500)" }}>
            {timestampLabel(response.timestamp)}
          </span>
        )}
        <div className="ml-auto flex" style={{ gap: 2 }}>
          <IconButton title="Copy body" onClick={copyBody}>
            <Copy />
          </IconButton>
          <IconButton title="Download body" onClick={downloadBody}>
            <DownloadSimple />
          </IconButton>
        </div>
      </div>

      {/* gRPC status message, if the call failed */}
      {!ok && response.status?.message && (
        <div
          style={{
            padding: "8px 14px",
            fontSize: 13,
            color: "var(--err-fg)",
            background: "var(--err-bg)",
            borderBottom: "1px solid var(--err-border)",
            wordBreak: "break-word",
          }}
        >
          {response.status.message}
        </div>
      )}

      {/* subtabs */}
      <div
        className="flex items-center"
        style={{ flex: "none", padding: "0 6px", borderBottom: "1px solid var(--line)" }}
      >
        <Subtab active={subtab === "messages"} onClick={() => setSubtab("messages")}>
          Messages
        </Subtab>
        <Subtab active={subtab === "metadata"} onClick={() => setSubtab("metadata")}>
          Metadata
          {reqMd.length + resMd.length > 0 && (
            <Tag variant="neutral" className="ml-[2px]">
              {reqMd.length + resMd.length}
            </Tag>
          )}
        </Subtab>
      </div>

      {/* content */}
      {subtab === "messages" ? (
        <div style={{ flex: 1, minHeight: 0 }}>
          {ok ? (
            <JsonViewer value={pretty} />
          ) : (
            <div className="text-muted" style={{ fontSize: 13, padding: 16 }}>
              No response body.
            </div>
          )}
        </div>
      ) : (
        <div style={{ flex: 1, overflow: "auto", padding: "12px 14px" }}>
          <MetaSection title="Request metadata" entries={reqMd} />
          <MetaSection title="Response metadata" entries={resMd} />
        </div>
      )}
    </div>
  );
}

function MetaSection({ title, entries }: { title: string; entries: Array<[string, string]> }) {
  return (
    <div style={{ marginBottom: 16 }}>
      <div
        style={{
          fontSize: 10,
          letterSpacing: ".1em",
          textTransform: "uppercase",
          color: "var(--color-neutral-600)",
          marginBottom: 8,
        }}
      >
        {title}
      </div>
      {entries.length === 0 ? (
        <div className="text-muted" style={{ fontSize: 12 }}>
          None.
        </div>
      ) : (
        <div className="flex flex-col" style={{ gap: 3 }}>
          {entries.map(([k, v]) => (
            <div key={k} className="flex gap-[8px] font-mono" style={{ fontSize: 12 }}>
              <span style={{ color: "var(--color-accent-300)", flex: "none" }}>{k}:</span>
              <span style={{ color: "var(--color-neutral-400)", wordBreak: "break-all" }}>{v}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
