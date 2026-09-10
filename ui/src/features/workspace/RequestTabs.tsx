import { useState } from "react";
import clsx from "clsx";
import { X } from "@/components/ui/icons";
import { Menu } from "@/components/ui/Menu";
import { TreeIcon } from "@/components/tree/icon-map";
import { useUIStore, type OpenTab } from "@/lib/ui-store";
import { MethodKindTag } from "@/components/ui/Tag";
import { useActiveWorkspace, useRootItems } from "@/lib/workspace-query";
import { findByKey, methodKind, resolveMethod } from "@/lib/format";
import { tabMenuItems, type TabMenuActions } from "./tab-menu";

// RequestTabs is the open-request tab strip. Frontend-only state; no persistence.
export function RequestTabs() {
  const { workspace, services } = useActiveWorkspace();
  const rootItems = useRootItems(workspace);
  const openTabs = useUIStore((s) => s.openTabs);
  const activeKey = useUIStore((s) => s.activeKey);
  const setActiveKey = useUIStore((s) => s.setActiveKey);
  const closeTab = useUIStore((s) => s.closeTab);
  const closeOtherTabs = useUIStore((s) => s.closeOtherTabs);
  const closeTabsToLeft = useUIStore((s) => s.closeTabsToLeft);
  const closeTabsToRight = useUIStore((s) => s.closeTabsToRight);
  const closeAllTabs = useUIStore((s) => s.closeAllTabs);
  const [menu, setMenu] = useState<{
    x: number;
    y: number;
    tab: OpenTab;
  } | null>(null);

  const menuActions: TabMenuActions = {
    close: closeTab,
    closeOthers: closeOtherTabs,
    closeToTheLeft: closeTabsToLeft,
    closeToTheRight: closeTabsToRight,
    closeAll: closeAllTabs,
  };

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
          item?.item.content.case === "request"
            ? item.item.content.value
            : undefined;
        const kind = methodKind(
          resolveMethod(services, req?.service ?? "", req?.method ?? ""),
        );
        return (
          <div
            key={tab.key}
            className={clsx("flex items-center gap-[8px]")}
            style={{
              padding: "0 14px",
              // Fixed, not min/max: every tab must render at the exact same width
              // regardless of title length, so the strip's rhythm doesn't jump
              // per-request; the name span below does the eliding.
              width: 160,
              flex: "0 0 auto",
              fontSize: 13,
              cursor: "pointer",
              color: active ? "var(--color-text)" : "var(--color-neutral-400)",
              borderRight: "1px solid var(--line)",
              borderBottom: active
                ? "2px solid var(--color-accent)"
                : "2px solid transparent",
              background: active ? "var(--color-bg)" : "transparent",
            }}
            // The collection rides on the tab, so activating one in another collection
            // switches to it without anyone parsing a collection out of the key.
            onClick={() => setActiveKey(tab.key, tab.collection)}
            onContextMenu={(e) => {
              e.preventDefault();
              setMenu({ x: e.clientX, y: e.clientY, tab });
            }}
          >
            {/* The tree's own icon resolver (icon-map.tsx), not a hand-drawn copy — the
                same "folder" token request-tree.tsx assigns a folder row. */}
            {tab.kind === "folder" ? (
              <TreeIcon token="folder" />
            ) : (
              <MethodKindTag kind={kind} />
            )}
            {/* Live name, not the stored one: the key is slug-based, so a rename no
                longer rewrites the tab. tab.name only covers a deleted item. flex:1 +
                minWidth:0 is what lets a flex child ellipsize instead of forcing the row
                to grow — the same convention as request-tree.tsx's row label. */}
            <span
              style={{
                flex: 1,
                minWidth: 0,
                overflow: "hidden",
                textOverflow: "ellipsis",
                whiteSpace: "nowrap",
              }}
            >
              {item?.item.name ?? tab.name}
            </span>
            <X
              size={12}
              style={{ opacity: 0.5, flex: "none" }}
              onClick={(e) => {
                e.stopPropagation();
                closeTab(tab.key);
              }}
            />
          </div>
        );
      })}

      {/* Keyed on the summon point plus the tab so a second right-click remounts. */}
      {menu ? (
        <Menu
          key={`${menu.x},${menu.y},${menu.tab.key}`}
          x={menu.x}
          y={menu.y}
          items={tabMenuItems(menu.tab, openTabs, menuActions)}
          onClose={() => setMenu(null)}
        />
      ) : null}
    </div>
  );
}
