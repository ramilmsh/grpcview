import { useUIStore } from "@/lib/ui-store";
import { CollectionPanel } from "./CollectionPanel";
import { RequestTabs } from "./RequestTabs";
import { RequestWorkspace } from "./RequestWorkspace";
import { FolderWorkspace } from "./FolderWorkspace";

export function WorkspaceView() {
  const openTabs = useUIStore((s) => s.openTabs);
  const activeKey = useUIStore((s) => s.activeKey);
  const activeTab = openTabs.find((t) => t.key === activeKey);

  return (
    <div className="flex" style={{ flex: 1, minWidth: 0, minHeight: 0 }}>
      <CollectionPanel />
      <div
        className="flex flex-col"
        style={{ flex: 1, minWidth: 0, minHeight: 0 }}
      >
        <RequestTabs />
        {activeTab?.kind === "folder" ? (
          <FolderWorkspace />
        ) : (
          <RequestWorkspace />
        )}
      </div>
    </div>
  );
}
