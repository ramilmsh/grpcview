import { Broadcast, CaretDown, MagnifyingGlass, Gear } from "@/components/ui/icons";
import { Button, IconButton } from "@/components/ui/Button";
import { Kbd } from "@/components/ui/Kbd";
import { useWorkspace, hostLabel, WORKSPACE_NAME } from "@/lib/workspace-query";

export function TopBar() {
  const { reflection, sources } = useWorkspace();
  const connected = !!reflection;
  const sourceCount = `${sources.length} source${sources.length === 1 ? "" : "s"}`;

  return (
    <div
      className="flex items-center gap-[14px] px-[14px] bg-panel"
      style={{ height: 46, flex: "none", borderBottom: "1px solid var(--line)" }}
    >
      <div className="flex items-center gap-[9px]">
        <div
          className="flex items-center justify-center"
          style={{
            width: 22,
            height: 22,
            borderRadius: 6,
            background: "var(--color-accent)",
            color: "#161826",
            fontSize: 14,
          }}
        >
          <Broadcast weight="bold" />
        </div>
        <span
          className="font-heading"
          style={{ fontWeight: 600, fontSize: 15, letterSpacing: "-.01em" }}
        >
          grpcview
        </span>
      </div>

      <div style={{ width: 1, height: 20, background: "var(--line)" }} />

      <Button
        className="text-neutral-200"
        style={{ padding: "4px 9px", fontSize: 13, gap: 7, cursor: "default" }}
        title="Single collection in Phase 1"
        disabled
      >
        <span className="text-accent" style={{ fontSize: 13 }}>❯</span>
        {WORKSPACE_NAME}
        <CaretDown size={11} style={{ opacity: 0.5 }} />
      </Button>

      <div className="ml-auto flex items-center gap-[10px]">
        <Button
          variant="secondary"
          style={{ padding: "4px 10px", fontSize: 12, gap: 7 }}
          title="Search — not available in Phase 1"
          disabled
        >
          <MagnifyingGlass size={14} />
          Search
          <Kbd>⌘K</Kbd>
        </Button>
        <span
          className="flex items-center gap-[6px] font-mono"
          style={{ fontSize: 12, color: "var(--color-neutral-400)" }}
          title={
            connected
              ? `Reflection source: ${hostLabel(reflection)}`
              : sources.length > 0
                ? `${sourceCount}, none reflective — requests need a target of their own`
                : "No definition source added yet"
          }
        >
          <span
            className="dot"
            style={{ background: connected ? "var(--ok)" : "var(--color-neutral-600)" }}
          />
          {connected ? hostLabel(reflection) : sources.length > 0 ? sourceCount : "no source"}
        </span>
        <IconButton title="Settings — not available in Phase 1" disabled>
          <Gear />
        </IconButton>
      </div>
    </div>
  );
}
