import React, { useMemo } from "react";
import { Request_Response } from "@grpcview/v1/workspace_pb";
import type { Duration, Timestamp } from "@bufbuild/protobuf/wkt";
import { JsonViewer } from "@/components/JsonViewer";
import { metadataValueToString } from "@/components/MetadataEditor";

// gRPC status codes -> canonical names (google.rpc.Code).
const CODE_NAMES: Record<number, string> = {
  0: "OK",
  1: "CANCELLED",
  2: "UNKNOWN",
  3: "INVALID_ARGUMENT",
  4: "DEADLINE_EXCEEDED",
  5: "NOT_FOUND",
  6: "ALREADY_EXISTS",
  7: "PERMISSION_DENIED",
  8: "RESOURCE_EXHAUSTED",
  9: "FAILED_PRECONDITION",
  10: "ABORTED",
  11: "OUT_OF_RANGE",
  12: "UNIMPLEMENTED",
  13: "INTERNAL",
  14: "UNAVAILABLE",
  15: "DATA_LOSS",
  16: "UNAUTHENTICATED",
};

const codeName = (code: number): string => CODE_NAMES[code] ?? `CODE_${code}`;

const latencyLabel = (d?: Duration): string => {
  if (!d) return "";
  const ms = Number(d.seconds) * 1000 + d.nanos / 1e6;
  return ms < 1000 ? `${ms.toFixed(1)} ms` : `${(ms / 1000).toFixed(3)} s`;
};

const timestampLabel = (t?: Timestamp): string => {
  if (!t) return "";
  return new Date(Number(t.seconds) * 1000 + t.nanos / 1e6).toLocaleTimeString();
};

// prettyBody decodes the response bytes and pretty-prints them when they parse
// as JSON (they always do for a real response), falling back to raw text.
const prettyBody = (bytes: Uint8Array): string => {
  const text = new TextDecoder().decode(bytes);
  try {
    return JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    return text;
  }
};

const metadataEntries = (
  md?: Record<string, unknown>
): Array<[string, string]> => {
  if (!md) return [];
  return Object.entries(md).map(([k, v]) => [k, metadataValueToString(v)]);
};

interface ResponsePanelProps {
  response?: Request_Response;
  error?: string;
  loading?: boolean;
}

export const ResponsePanel: React.FC<ResponsePanelProps> = ({
  response,
  error,
  loading,
}) => {
  // Pretty-printing re-parses the whole response body; memoize it so editing the
  // request (which re-renders this panel) doesn't re-run it on every keystroke.
  const prettyResponse = useMemo(
    () => (response ? prettyBody(response.response) : ""),
    [response]
  );

  if (loading) {
    return (
      <div className="flex items-center justify-center h-full text-gray-400 text-sm">
        Invoking…
      </div>
    );
  }

  if (error) {
    return (
      <div className="p-4">
        <div className="text-xs uppercase font-medium text-red-600 mb-1">
          Error
        </div>
        <pre className="text-sm text-red-700 bg-red-50 border border-red-100 rounded p-3 whitespace-pre-wrap break-words">
          {error}
        </pre>
      </div>
    );
  }

  if (!response) {
    return (
      <div className="flex items-center justify-center h-full text-gray-400 text-sm">
        No response yet. Click Send.
      </div>
    );
  }

  const code = response.status?.code ?? 0;
  const ok = code === 0;
  const responseMd = metadataEntries(
    response.responseMetadata as Record<string, unknown> | undefined
  );

  return (
    <div className="flex flex-col h-full overflow-hidden">
      {/* Status bar */}
      <div className="flex items-center gap-3 px-4 py-2 border-b border-gray-200 text-sm">
        <span
          className={`px-2 py-0.5 rounded font-medium text-xs ${
            ok ? "bg-green-100 text-green-700" : "bg-red-100 text-red-700"
          }`}
        >
          {code} {codeName(code)}
        </span>
        {response.latency && (
          <span className="text-gray-500">{latencyLabel(response.latency)}</span>
        )}
        {response.timestamp && (
          <span className="text-gray-400 ml-auto text-xs">
            {timestampLabel(response.timestamp)}
          </span>
        )}
      </div>

      {/* Error message from the invoked call, if any */}
      {!ok && response.status?.message && (
        <div className="px-4 py-2 text-sm text-red-700 bg-red-50 border-b border-red-100 break-words">
          {response.status.message}
        </div>
      )}

      {/* Response body */}
      <div className="flex-grow overflow-hidden">
        {ok ? (
          <JsonViewer value={prettyResponse} />
        ) : (
          <div className="text-sm text-gray-400 p-4">No response body.</div>
        )}
      </div>

      {/* Response metadata */}
      {responseMd.length > 0 && (
        <div className="border-t border-gray-200 max-h-40 overflow-auto">
          <div className="px-4 py-1 text-xs uppercase font-medium text-gray-400">
            Response metadata
          </div>
          <div className="px-4 pb-3 space-y-0.5">
            {responseMd.map(([k, v]) => (
              <div key={k} className="flex gap-2 text-xs font-mono">
                <span className="text-purple-700 shrink-0">{k}:</span>
                <span className="text-gray-600 break-all">{v}</span>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
};
