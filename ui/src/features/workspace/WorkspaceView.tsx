import { CollectionPanel } from "./CollectionPanel";
import { RequestTabs } from "./RequestTabs";
import { RequestWorkspace } from "./RequestWorkspace";
import { BindingEditor } from "./BindingEditor";

// WorkspaceView is the collection + request/response surface (plan §9). The
// binding-editor modal (S2) is mounted here so a `{{ generator }}` token clicked in
// the request body / metadata can open it; it renders nothing until opened.
export function WorkspaceView() {
  return (
    <div className="flex" style={{ flex: 1, minHeight: 0 }}>
      <CollectionPanel />
      <div className="flex flex-col" style={{ flex: 1, minWidth: 0, minHeight: 0 }}>
        <RequestTabs />
        <RequestWorkspace />
      </div>
      <BindingEditor />
    </div>
  );
}
