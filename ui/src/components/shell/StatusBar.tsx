import { useActiveWorkspace } from "@/lib/workspace-query";
import { useUIStore } from "@/lib/ui-store";
import { latencyLabel } from "@/lib/format";

export function StatusBar() {
  const { sources, reflection } = useActiveWorkspace();
  const activeKey = useUIStore((s) => s.activeKey);
  const invoke = useUIStore((s) =>
    activeKey ? s.invokes[activeKey] : undefined,
  );
  const latency = invoke?.response?.latency;

  return (
    <div
      className="flex items-center font-mono"
      style={{
        height: 28,
        flex: "none",
        gap: 16,
        padding: "0 16px",
        background: "var(--v-shell)",
        fontSize: 12,
        color: "var(--v-text-2)",
      }}
    >
      <span
        className="inline-flex items-center gap-[6px]"
        title={
          reflection
            ? `Reflection source: ${reflection.address}`
            : sources.length > 0
              ? "Definitions come from uploaded descriptor sets — no server to reflect from"
              : "No definition source added yet"
        }
      >
        <span
          className="dot"
          style={{
            background: reflection ? "var(--v-ok)" : "var(--v-text-muted)",
          }}
        />
        {reflection ? "Local" : sources.length > 0 ? "No server" : "No source"}
      </span>
      <span>
        {sources.length} {sources.length === 1 ? "source" : "sources"}
      </span>
      {latency && (
        <span className="ml-auto">last invoke {latencyLabel(latency)}</span>
      )}
    </div>
  );
}
