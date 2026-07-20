import { useWorkspace } from "@/lib/workspace-query";
import { useUIStore } from "@/lib/ui-store";
import { latencyLabel } from "@/lib/format";

// StatusBar: real bits only (plan §8) — source count and the active request's
// last-invoke latency. Git/versions/QuickJS indicators wait for their backends.
export function StatusBar() {
  const { sources, reflection } = useWorkspace();
  const activeKey = useUIStore((s) => s.activeKey);
  const invoke = useUIStore((s) => (activeKey ? s.invokes[activeKey] : undefined));
  const latency = invoke?.response?.latency;

  return (
    <div
      className="bg-panel flex items-center font-mono"
      style={{
        height: 26,
        flex: "none",
        gap: 16,
        padding: "0 14px",
        borderTop: "1px solid var(--line)",
        fontSize: 11,
        color: "var(--color-neutral-500)",
      }}
    >
      <span className="inline-flex items-center gap-[5px]" style={{ color: reflection ? "var(--ok)" : undefined }}>
        <span
          className="dot"
          style={{ background: reflection ? "var(--ok)" : "var(--color-neutral-600)" }}
        />
        {reflection ? "Local" : "No source"}
      </span>
      <span>
        {sources.length} {sources.length === 1 ? "source" : "sources"}
      </span>
      {latency && <span className="ml-auto">last invoke {latencyLabel(latency)}</span>}
    </div>
  );
}
