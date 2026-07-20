import { HardDrives, LockSimple, LockSimpleOpen } from "@/components/ui/icons";
import type { Server } from "@grpcview/v1/workspace_pb";

// TargetBar renders the resolved target (host:port from the first reflection
// source) and its TLS state. Read-only in Phase 1; timeout/compression/options
// are omitted (not in the model — plan §1.3).
export function TargetBar({ target }: { target: Server | null }) {
  const tls = !!target?.tls;
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
      <div className="font-mono" style={{ fontSize: 13 }}>
        {target ? (
          <>
            <span style={{ color: "var(--color-text)" }}>{target.host}</span>
            <span style={{ color: "var(--color-neutral-600)" }}>:</span>
            <span style={{ color: "var(--color-text)" }}>{target.port}</span>
          </>
        ) : (
          <span style={{ color: "var(--color-neutral-500)" }}>no target — add a reflection source</span>
        )}
      </div>
      {target && (
        <div
          className="ml-auto flex items-center gap-[5px]"
          style={{ fontSize: 12, color: "var(--color-neutral-400)" }}
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
        </div>
      )}
    </div>
  );
}
