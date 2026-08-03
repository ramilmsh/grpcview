import { create } from "@bufbuild/protobuf";
import { HardDrives, LockSimple, LockSimpleOpen } from "@/components/ui/icons";
import { ServerSchema, Server_TLSSchema, type Server } from "@grpcview/v1/workspace_pb";

// TargetBar shows and edits the request's invoke target (host:port + TLS).
export function TargetBar({
  reflection,
  override,
  onChange,
}: {
  // The source backing this request; the live default shown when there is no override.
  reflection: Server | null;
  override?: Server;
  onChange: (t: Server) => void;
}) {
  const cur = override ?? reflection;
  const tls = cur?.tls != null;

  // A message field has no partial patch, so every edit emits a whole Server.
  const emit = (patch: { address?: string; tls?: boolean }) => {
    const nextTls = patch.tls ?? tls;
    onChange(
      create(ServerSchema, {
        address: patch.address ?? cur?.address ?? "",
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
          <input
            className="font-mono"
            value={cur.address}
            onChange={(e) => emit({ address: e.target.value })}
            placeholder="host:port"
            aria-label="Target address"
            spellCheck={false}
            style={{
              background: "transparent",
              border: "none",
              outline: "none",
              padding: 0,
              color: "var(--color-text)",
              fontSize: 13,
              width: `${Math.max(cur.address.length, 12)}ch`,
            }}
          />
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
