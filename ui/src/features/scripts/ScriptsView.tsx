import { useState } from "react";
import { BracketsCurly, Plus } from "@/components/ui/icons";
import { Button } from "@/components/ui/Button";
import { useActiveWorkspace, useCreateScript } from "@/lib/workspace-query";
import { useUIStore } from "@/lib/ui-store";
import { MainPanel } from "@/components/ui/MainPanel";
import { ScriptSidebar } from "./ScriptSidebar";
import { ScriptDetail } from "./ScriptDetail";
import { NewScriptDialog } from "./NewScriptDialog";

export function ScriptsView() {
  // Scoped to the active collection: the merged descriptor set, and therefore what a
  // script can call, differs per collection.
  const { collection, workspace } = useActiveWorkspace();
  const scripts = workspace?.scripts ?? [];

  const selectedPath = useUIStore((s) => s.selectedScript);
  const selectScript = useUIStore((s) => s.selectScript);
  const setScriptSubtab = useUIStore((s) => s.setScriptSubtab);

  const createScript = useCreateScript();
  const [newOpen, setNewOpen] = useState(false);

  const selected = scripts.find((s) => s.path === selectedPath) ?? null;

  const onCreate = (path: string) => {
    createScript.mutate(
      { collection: collection ?? "", path },
      {
        onSuccess: () => {
          selectScript(path);
          setScriptSubtab("code");
          setNewOpen(false);
        },
      },
    );
  };

  return (
    <div className="flex" style={{ flex: 1, minHeight: 0 }}>
      <ScriptSidebar
        scripts={scripts}
        selectedPath={selectedPath}
        onSelect={selectScript}
        onNew={() => setNewOpen(true)}
      />
      <MainPanel>
        {selected ? (
          <ScriptDetail key={selected.path} script={selected} />
        ) : (
          <ScriptsEmptyState onNew={() => setNewOpen(true)} />
        )}
      </MainPanel>
      <NewScriptDialog
        open={newOpen}
        onClose={() => setNewOpen(false)}
        onCreate={onCreate}
        pending={createScript.isPending}
        error={createScript.isError ? createScript.error : null}
        existingPaths={scripts.map((s) => s.path)}
      />
    </div>
  );
}

function ScriptsEmptyState({ onNew }: { onNew: () => void }) {
  return (
    <div
      className="flex flex-col items-center justify-center"
      style={{
        flex: 1,
        minWidth: 0,
        gap: 14,
        padding: 24,
        textAlign: "center",
      }}
    >
      <div
        className="flex items-center justify-center"
        style={{
          width: 48,
          height: 48,
          borderRadius: "var(--v-r)",
          background: "var(--v-hover)",
          color: "var(--v-text-2)",
        }}
      >
        <BracketsCurly size={24} />
      </div>
      <div style={{ fontSize: 15, color: "var(--color-neutral-200)" }}>
        No script selected
      </div>
      <p
        className="text-muted"
        style={{ fontSize: 13, lineHeight: 1.6, margin: 0, maxWidth: 420 }}
      >
        Pick a script from the sidebar, or create one to author and test-run it.
        See a script's Capabilities tab for what the runtime enforces.
      </p>
      <Button variant="primary" onClick={onNew}>
        <Plus size={14} />
        New script
      </Button>
    </div>
  );
}
