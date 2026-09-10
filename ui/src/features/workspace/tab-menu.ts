// The tab strip's right-click menu items. Its own module for the same reason as
// collection-menu.ts: testable without pulling in RequestTabs' monaco-adjacent imports.
import type { MenuItem } from "@/components/ui/Menu";
import type { OpenTab } from "@/lib/ui-store";

export interface TabMenuActions {
  close(key: string): void;
  closeOthers(key: string): void;
  closeToTheLeft(key: string): void;
  closeToTheRight(key: string): void;
  closeAll(): void;
}

export function tabMenuItems(
  tab: OpenTab,
  tabs: readonly OpenTab[],
  actions: TabMenuActions,
): MenuItem[] {
  const index = tabs.findIndex((t) => t.key === tab.key);
  return [
    { label: "Close", onSelect: () => actions.close(tab.key) },
    {
      label: "Close others",
      disabled: tabs.length <= 1,
      onSelect: () => actions.closeOthers(tab.key),
    },
    {
      label: "Close to the left",
      disabled: index === -1 || index === 0,
      onSelect: () => actions.closeToTheLeft(tab.key),
    },
    {
      label: "Close to the right",
      disabled: index === -1 || index === tabs.length - 1,
      onSelect: () => actions.closeToTheRight(tab.key),
    },
    {
      label: "Close all",
      separatorBefore: true,
      onSelect: () => actions.closeAll(),
    },
  ];
}
