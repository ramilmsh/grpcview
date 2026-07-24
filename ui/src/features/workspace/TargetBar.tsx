import { create } from "@bufbuild/protobuf";
import { HardDrives, LockSimple, LockSimpleOpen } from "@/components/ui/icons";
import { ServerSchema, Server_TLSSchema, type Server } from "@grpcview/v1/workspace_pb";

// TargetBar shows and edits the request's invoke target (host:port + TLS). The
// displayed value is the per-request override when set, otherwise the workspace's
// first reflection source (a live fallback). Editing any field emits a full Server
// override (seeded from the currently-displayed value) via onChange; the empty
// state shows only when there is neither an override nor a reflection source.
export function TargetBar({
  reflection,
  override,
  onChange,
}: {
  reflection: Server | null;
  override?: Server;
  onChange: (t: Server) => void;
}) {
  const cur = override ?? reflection;
  const tls = cur?.tls != null;

  // Build a full Server from the currently-displayed values with the one edited
  // field changed, so an edit never drops the other two (a message field has no
  // partial patch). Port parses to a number, NaN -> 0.
  const emit = (patch: { host?: string; port?: number; tls?: boolean }) => {
    const host = patch.host ?? cur?.host ?? "";
    const port = patch.port ?? cur?.port ?? 0;
    const nextTls = patch.tls ?? tls;
    onChange(
      create(ServerSchema, {
        host,
        port,
        tls: nextTls ? create(Server_TLSSchema, {}) : undefined,
      })
    );
  };

  return (
    <div
      className="flex items-center gap-[8px]"
      style={{
        background: "var(--panel-2)",
        border: "1px solid var(--line)",
        borderRadius: 8,
        padding: "6px 10px",
      }}
    >
      <HardDrives size={15} style={{ color: "var(--color-neutral-500)" }} />
      {cur ? (
        <>
          <div className="flex items-center font-mono" style={{ fontSize: 13 }}>
            <input
              className="font-mono"
              value={cur.host}
              onChange={(e) => emit({ host: e.target.value })}
              placeholder="host"
              aria-label="Target host"
              spellCheck={false}
              style={{
                background: "transparent",
                border: "none",
                outline: "none",
                padding: 0,
                color: "var(--color-text)",
                fontSize: 13,
                width: `${Math.max(cur.host.length, 4)}ch`,
              }}
            />
            <span style={{ color: "var(--color-neutral-600)" }}>:</span>
            <input
              className="font-mono"
              type="number"
              value={cur.port}
              onChange={(e) => emit({ port: parseInt(e.target.value, 10) || 0 })}
              aria-label="Target port"
              style={{
                background: "transparent",
                border: "none",
                outline: "none",
                padding: 0,
                color: "var(--color-text)",
                fontSize: 13,
                width: "8ch",
              }}
            />
          </div>
          <button
            type="button"
            onClick={() => emit({ tls: !tls })}
            className="ml-auto flex items-center gap-[5px]"
            title={tls ? "TLS on — click to disable" : "insecure — click to enable TLS"}
            style={{
              background: "transparent",
              border: "none",
              cursor: "pointer",
              padding: 0,
              fontSize: 12,
              color: "var(--color-neutral-400)",
            }}
          >
            {tls ? (
              <>
                <LockSimple weight="fill" size={13} style={{ color: "var(--ok)" }} />
                TLS
              </>
            ) : (
              <>
                <LockSimpleOpen size={13} style={{ color: "var(--color-neutral-500)" }} />
                insecure
              </>
            )}
          </button>
        </>
      ) : (
        <div className="font-mono" style={{ fontSize: 13 }}>
          <span style={{ color: "var(--color-neutral-500)" }}>
            no target — add a reflection source
          </span>
        </div>
      )}
    </div>
  );
}
