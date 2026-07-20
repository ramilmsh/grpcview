import clsx from "clsx";
import { X } from "@/components/ui/icons";
import { useUIStore } from "@/lib/ui-store";
import { MethodKindTag } from "@/components/ui/Tag";

// RequestTabs is the client-side open-request tab strip (plan §1.2). Purely
// frontend state; no persistence.
export function RequestTabs() {
  const openTabs = useUIStore((s) => s.openTabs);
  const activeKey = useUIStore((s) => s.activeKey);
  const setActiveKey = useUIStore((s) => s.setActiveKey);
  const closeTab = useUIStore((s) => s.closeTab);

  if (openTabs.length === 0) return null;

  return (
    <div
      className="bg-panel flex items-stretch"
      style={{
        height: 38,
        flex: "none",
        borderBottom: "1px solid var(--line)",
        overflowX: "auto",
      }}
    >
      {openTabs.map((tab) => {
        const active = tab.key === activeKey;
        return (
          <div
            key={tab.key}
            className={clsx("flex items-center gap-[8px]")}
            style={{
              padding: "0 14px",
              fontSize: 13,
              cursor: "pointer",
              whiteSpace: "nowrap",
              color: active ? "var(--color-text)" : "var(--color-neutral-400)",
              borderRight: "1px solid var(--line)",
              borderBottom: active ? "2px solid var(--color-accent)" : "2px solid transparent",
              background: active ? "var(--color-bg)" : "transparent",
            }}
            onClick={() => setActiveKey(tab.key)}
          >
            <MethodKindTag kind="u" />
            {tab.name}
            <X
              size={12}
              style={{ opacity: 0.5 }}
              onClick={(e) => {
                e.stopPropagation();
                closeTab(tab.key);
              }}
            />
          </div>
        );
      })}
    </div>
  );
}
