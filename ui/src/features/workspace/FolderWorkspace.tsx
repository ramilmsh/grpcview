import { useEffect, useRef, useState } from "react";
import { Folder } from "@/components/ui/icons";
import { Subtab } from "@/components/ui/Subtab";
import { Centered } from "@/components/ui/Centered";
import {
  useActiveWorkspace,
  useRootItems,
  useWorkspaceMutations,
} from "@/lib/workspace-query";
import { useUIStore } from "@/lib/ui-store";
import { findByKey } from "@/lib/format";
import { MetadataTab } from "./MetadataTab";
import { defaultMetadataModule, hostMetadataScript } from "./metadata-wrapper";

const DEBOUNCE_MS = 400;

type FolderSubtab = "metadata" | "server";

// FolderWorkspace is the Folder tab's working area — RequestWorkspace's shape (header +
// Subtab chrome), with neither Message (a folder has no body) nor Middleware (no backend
// concept for folder/collection-scoped middleware yet — Request.middleware is the only
// kind that exists). Metadata itself is unchanged: MetadataTab/MetadataEditor, exactly as
// FolderMetadataDialog used to wrap them. Server is a placeholder — no backend concept
// yet either. Subtab selection is local state, not ui-store: unlike Request's subtab,
// nothing outside this component reads it.
export function FolderWorkspace() {
  const [subtab, setSubtab] = useState<FolderSubtab>("metadata");

  const { collection: activeCollection, workspace } = useActiveWorkspace();
  // Non-null everywhere this pane renders (App gates on the collection listing).
  const collection = activeCollection ?? "";
  const rootItems = useRootItems(workspace);
  const { updateFolder } = useWorkspaceMutations();

  const activeKey = useUIStore((s) => s.activeKey);
  const draft = useUIStore((s) =>
    activeKey ? s.folderDrafts[activeKey] : undefined,
  );
  const seedFolderDraft = useUIStore((s) => s.seedFolderDraft);
  const setFolderDraft = useUIStore((s) => s.setFolderDraft);

  const activeItem = findByKey(rootItems, activeKey);
  const folder =
    activeItem?.item.content.case === "folder"
      ? activeItem.item.content.value
      : null;

  // Keyed by folder key, like RequestWorkspace's scheduleSave — a pending save fires
  // for the key it was scheduled under even after the tab is switched away from.
  const timers = useRef<Record<string, number>>({});

  useEffect(() => {
    if (activeKey && folder) {
      seedFolderDraft(
        activeKey,
        hostMetadataScript(folder.draftMetadataScript) ||
          defaultMetadataModule(),
      );
    }
  }, [activeKey, folder, seedFolderDraft]);

  if (!activeItem || !folder || !activeKey) {
    return <Centered>Select a folder to edit its metadata.</Centered>;
  }

  const key = activeKey;
  const path = activeItem.path;
  const itemName = activeItem.item.name;
  // Fallback covers the first render, before the seed effect commits.
  const metadata =
    draft ??
    (hostMetadataScript(folder.draftMetadataScript) || defaultMetadataModule());

  const onMetadataChange = (v: string) => {
    setFolderDraft(key, v);
    window.clearTimeout(timers.current[key]);
    timers.current[key] = window.setTimeout(() => {
      updateFolder.mutate({
        collection,
        path,
        itemName,
        draftMetadataScript: v,
      });
    }, DEBOUNCE_MS);
  };

  return (
    <div
      className="flex flex-col"
      style={{ flex: 1, minWidth: 0, minHeight: 0 }}
    >
      <div
        className="flex items-center gap-[10px]"
        style={{
          flex: "none",
          padding: "11px 16px 12px",
          background: "var(--color-bg)",
          borderBottom: "1px solid var(--line)",
        }}
      >
        {/* Same icon a folder row draws in the tree (request-tree.tsx's renderRequestRow):
            filled, neutral-500. */}
        <Folder
          weight="fill"
          size={15}
          style={{ color: "var(--color-neutral-500)" }}
        />
        <span
          className="font-heading"
          style={{ fontSize: 15, fontWeight: 600, color: "var(--color-text)" }}
        >
          {itemName}
        </span>
      </div>

      <div
        className="flex flex-col"
        style={{ flex: 1, minWidth: 0, minHeight: 0 }}
      >
        <div
          className="flex items-center"
          style={{
            flex: "none",
            padding: "0 6px",
            borderBottom: "1px solid var(--line)",
            background: "var(--color-bg)",
          }}
        >
          <Subtab
            active={subtab === "metadata"}
            onClick={() => setSubtab("metadata")}
          >
            Metadata
          </Subtab>
          <Subtab
            active={subtab === "server"}
            onClick={() => setSubtab("server")}
          >
            Server
          </Subtab>
        </div>
        {subtab === "metadata" ? (
          <MetadataTab
            metadata={metadata}
            onChange={onMetadataChange}
            currentKey={key}
          />
        ) : (
          <Centered>Server config isn't wired up yet.</Centered>
        )}
      </div>
    </div>
  );
}
