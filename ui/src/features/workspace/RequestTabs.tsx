import clsx from "clsx";
import { X } from "@/components/ui/icons";
import { useUIStore } from "@/lib/ui-store";
import { MethodKindTag } from "@/components/ui/Tag";
import { useActiveWorkspace, useRootItems } from "@/lib/workspace-query";
import { findByKey, methodKind, resolveMethod } from "@/lib/format";

// RequestTabs is the open-request tab strip. Frontend-only state; no persistence.
export function RequestTabs() {
  const { workspace, services } = useActiveWorkspace();
  const rootItems = useRootItems(workspace);
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
        const item = findByKey(rootItems, tab.key);
        const req =
          item?.item.content.case === "request" ? item.item.content.value : undefined;
        const kind = methodKind(
          resolveMethod(services, req?.service ?? "", req?.method ?? "")
        );
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
            // The collection rides on the tab, so activating one in another collection
            // switches to it without anyone parsing a collection out of the key.
            onClick={() => setActiveKey(tab.key, tab.collection)}
          >
            <MethodKindTag kind={kind} />
            {/* Live name, not the stored one: the key is slug-based, so a rename no
                longer rewrites the tab. tab.name only covers a deleted item. */}
            {item?.item.name ?? tab.name}
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
