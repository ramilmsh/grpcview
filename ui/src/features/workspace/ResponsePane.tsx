import { useMemo } from "react";
import { Copy, DownloadSimple, Stack, Stop } from "@/components/ui/icons";
import { IconButton, Button } from "@/components/ui/Button";
import { Centered } from "@/components/ui/Centered";
import { Subtab } from "@/components/ui/Subtab";
import { Tag, type MethodKind } from "@/components/ui/Tag";
import { useUIStore } from "@/lib/ui-store";
import type { InvokeState, StreamMessage } from "@/lib/ui-store";
import {
  codeName,
  latencyLabel,
  timestampLabel,
  prettyBody,
  metadataEntries,
} from "@/lib/format";
import { JsonViewer } from "./JsonViewer";

// ResponsePane renders the Invoke result. Unary calls show a single status bar +
// body (unchanged). Streaming calls (server/client/bidi) show the same status bar
// augmented with a live streaming indicator, message count, and Stop, plus a loop
// of response-message cards. The backend merges header+trailer metadata, so there
// is no separate Trailers tab (plan §1.5). A gRPC-status failure arrives as the
// terminal result's status; only grpcview-internal failures surface via `error`.
export function ResponsePane({
  invoke,
  kind = "u",
  onStop,
}: {
  invoke?: InvokeState;
  kind?: MethodKind;
  onStop?: () => void;
}) {
  const subtab = useUIStore((s) => s.responseSubtab);
  const setSubtab = useUIStore((s) => s.setResponseSubtab);

  const response = invoke?.response; // terminal result (undefined while streaming)
  // A stream having started is the discriminator: `messages` is set (even empty)
  // for the streaming path and undefined for the unary path.
  const streamMode = invoke?.messages !== undefined;
  const streaming = !!invoke?.streaming;
  const streamMsgs = invoke?.messages ?? [];

  const pretty = useMemo(
    () => (response ? prettyBody(response.response) : ""),
    [response]
  );

  // Unary in-flight (streaming never sets `loading`).
  if (invoke?.loading) {
    return <Centered>Invoking…</Centered>;
  }

  // grpcview-side error (unreachable schema, bad body, no target) — distinct from
  // a gRPC status failure, which arrives as response data below. For streams this
  // only fires on an open failure (before any message), so no cards are lost.
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

  // Nothing invoked yet. Streaming turns to its live view the moment a stream
  // starts (messages set), so this only shows pre-invoke; the hint reflects kind.
  if (!streamMode && !response) {
    return (
      <Centered>
        {kind === "u" ? "No response yet. Click Invoke." : "No stream yet. Click Invoke."}
      </Centered>
    );
  }

  // code is defined whenever a terminal result exists (always so for unary here);
  // undefined means a stream is still open before its result frame → "pending".
  const code = response ? response.status?.code ?? 0 : undefined;
  const ok = code === 0;
  const reqMd = metadataEntries(response?.requestMetadata as Record<string, unknown> | undefined);
  const resMd = metadataEntries(response?.responseMetadata as Record<string, unknown> | undefined);

  const bodyText = streamMode ? streamMsgs.map((m) => m.body).join("\n\n") : pretty;
  const copyBody = () => navigator.clipboard?.writeText(bodyText);
  const downloadBody = () => {
    const blob = new Blob([bodyText], { type: "application/json" });
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
        {code !== undefined ? (
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
        ) : (
          <span
            className="tag font-mono"
            style={{ fontWeight: 600, background: "var(--panel-2)", color: "var(--color-neutral-400)" }}
          >
            pending
          </span>
        )}

        {streamMode && (
          <span
            className="flex items-center gap-[6px] font-mono"
            style={{ fontSize: 12, color: "var(--color-neutral-500)" }}
          >
            {streaming ? (
              <>
                <span className="dot pulse" style={{ background: "var(--ok)" }} />
                streaming
              </>
            ) : (
              "closed"
            )}
          </span>
        )}
        {streamMode && (
          <span
            className="flex items-center gap-[5px] font-mono"
            style={{ fontSize: 12, color: "var(--color-neutral-500)" }}
          >
            <Stack size={13} />
            {streamMsgs.length} {streamMsgs.length === 1 ? "msg" : "msgs"}
          </span>
        )}

        {response?.latency && (
          <span className="font-mono" style={{ fontSize: 12, color: "var(--color-neutral-500)" }}>
            {latencyLabel(response.latency)}
          </span>
        )}
        {response?.timestamp && (
          <span className="font-mono" style={{ fontSize: 12, color: "var(--color-neutral-500)" }}>
            {timestampLabel(response.timestamp)}
          </span>
        )}
        <div className="ml-auto flex items-center" style={{ gap: 2 }}>
          {streaming && (
            <Button
              variant="danger"
              onClick={onStop}
              style={{ padding: "4px 12px", fontSize: 12, gap: 6, marginRight: 6 }}
              title="Stop the stream"
            >
              <Stop weight="fill" size={12} />
              Stop
            </Button>
          )}
          <IconButton title="Copy body" onClick={copyBody}>
            <Copy />
          </IconButton>
          <IconButton title="Download body" onClick={downloadBody}>
            <DownloadSimple />
          </IconButton>
        </div>
      </div>

      {/* gRPC status message, if the call failed */}
      {!ok && response?.status?.message && (
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
        streamMode ? (
          <StreamMessagesView msgs={streamMsgs} streaming={streaming} />
        ) : (
          <div style={{ flex: 1, minHeight: 0 }}>
            {ok ? (
              <JsonViewer value={pretty} />
            ) : (
              <div className="text-muted" style={{ fontSize: 13, padding: 16 }}>
                No response body.
              </div>
            )}
          </div>
        )
      ) : (
        <div style={{ flex: 1, overflow: "auto", padding: "12px 14px" }}>
          <MetaSection title="Request metadata" entries={reqMd} />
          <MetaSection title="Response metadata" entries={resMd} />
        </div>
      )}
    </div>
  );
}

// StreamMessagesView renders one card per received payload, and — while the
// stream is open — an "awaiting next message…" row (pulsing dot). Payload bodies
// are pretty-printed JSON shown mono with wrapping preserved.
function StreamMessagesView({ msgs, streaming }: { msgs: StreamMessage[]; streaming: boolean }) {
  return (
    <div
      className="flex flex-col"
      style={{ flex: 1, minHeight: 0, overflow: "auto", padding: 12, gap: 10 }}
    >
      {msgs.map((m, i) => (
        <div
          key={i}
          style={{
            border: "1px solid var(--line)",
            borderRadius: 8,
            background: "var(--panel-2)",
            overflow: "hidden",
          }}
        >
          <div
            className="flex items-center gap-[8px]"
            style={{ padding: "6px 10px", borderBottom: "1px solid var(--line)" }}
          >
            <Tag variant="accent">#{i + 1}</Tag>
            <span
              className="ml-auto font-mono"
              style={{ fontSize: 11, color: "var(--color-neutral-500)" }}
            >
              {new Date(m.at).toLocaleTimeString()}
            </span>
          </div>
          <div
            className="font-mono"
            style={{
              whiteSpace: "pre-wrap",
              wordBreak: "break-word",
              padding: "10px 12px",
              fontSize: 12.5,
              lineHeight: 1.5,
              color: "var(--color-neutral-200)",
            }}
          >
            {m.body}
          </div>
        </div>
      ))}
      {streaming ? (
        <div className="flex items-center gap-[8px]" style={{ padding: "4px 2px" }}>
          <span className="dot pulse" style={{ background: "var(--ok)" }} />
          <span className="text-muted" style={{ fontSize: 12 }}>
            awaiting next message…
          </span>
        </div>
      ) : (
        msgs.length === 0 && (
          <div className="text-muted" style={{ fontSize: 13, padding: 4 }}>
            No messages received.
          </div>
        )
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
