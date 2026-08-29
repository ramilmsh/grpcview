import { CollectionPanel } from "./CollectionPanel";
import { RequestTabs } from "./RequestTabs";
import { RequestWorkspace } from "./RequestWorkspace";

export function WorkspaceView() {
  return (
    <div className="flex" style={{ flex: 1, minWidth: 0, minHeight: 0 }}>
      <CollectionPanel />
      <div
        className="flex flex-col"
        style={{ flex: 1, minWidth: 0, minHeight: 0 }}
      >
        <RequestTabs />
        <RequestWorkspace />
      </div>
    </div>
  );
}
