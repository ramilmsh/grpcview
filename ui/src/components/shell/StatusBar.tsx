import { useUIStore } from "@/lib/ui-store";
import { latencyLabel } from "@/lib/format";

export function StatusBar() {
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
      {latency && (
        <span className="ml-auto">last invoke {latencyLabel(latency)}</span>
      )}
    </div>
  );
}
