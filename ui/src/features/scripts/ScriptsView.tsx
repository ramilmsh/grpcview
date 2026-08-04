import { useState } from "react";
import { BracketsCurly, Plus } from "@/components/ui/icons";
import { Button } from "@/components/ui/Button";
import { useWorkspace, useCreateScript, COLLECTION_ID } from "@/lib/workspace-query";
import { useUIStore } from "@/lib/ui-store";
import type { ScriptKind } from "@grpcview/v1/workspace_pb";
import { ScriptSidebar } from "./ScriptSidebar";
import { ScriptDetail } from "./ScriptDetail";
import { NewScriptDialog } from "./NewScriptDialog";

export function ScriptsView() {
  const { workspace } = useWorkspace();
  const scripts = workspace?.scripts ?? [];

  const selectedName = useUIStore((s) => s.selectedScript);
  const selectScript = useUIStore((s) => s.selectScript);
  const setScriptSubtab = useUIStore((s) => s.setScriptSubtab);

  const createScript = useCreateScript();
  const [newOpen, setNewOpen] = useState(false);

  const selected = scripts.find((s) => s.name === selectedName) ?? null;

  const onCreate = (name: string, kind: ScriptKind) => {
    createScript.mutate(
      { collection: COLLECTION_ID, name, kind },
      {
        onSuccess: () => {
          selectScript(name);
          setScriptSubtab("code");
          setNewOpen(false);
        },
      }
    );
  };

  return (
    <div className="flex" style={{ flex: 1, minHeight: 0 }}>
      <ScriptSidebar
        scripts={scripts}
        selectedName={selectedName}
        onSelect={selectScript}
        onNew={() => setNewOpen(true)}
      />
      {selected ? (
        <ScriptDetail key={selected.name} script={selected} />
      ) : (
        <ScriptsEmptyState onNew={() => setNewOpen(true)} />
      )}
      <NewScriptDialog
        open={newOpen}
        onClose={() => setNewOpen(false)}
        onCreate={onCreate}
        pending={createScript.isPending}
        error={createScript.isError ? createScript.error : null}
        existingNames={scripts.map((s) => s.name)}
      />
    </div>
  );
}

function ScriptsEmptyState({ onNew }: { onNew: () => void }) {
  return (
    <div
      className="flex flex-col items-center justify-center"
      style={{ flex: 1, minWidth: 0, gap: 14, padding: 24, textAlign: "center" }}
    >
      <div
        className="flex items-center justify-center"
        style={{
          width: 48,
          height: 48,
          borderRadius: 13,
          background: "var(--panel-2)",
          border: "1px solid var(--line)",
          color: "var(--color-neutral-500)",
        }}
      >
        <BracketsCurly size={24} />
      </div>
      <div style={{ fontSize: 15, color: "var(--color-neutral-200)" }}>No script selected</div>
      <p className="text-muted" style={{ fontSize: 13, lineHeight: 1.6, margin: 0, maxWidth: 420 }}>
        Pick a script from the sidebar, or create a generator, middleware, or scenario
        to author and test-run it. See a script's Capabilities tab for what the runtime
        enforces.
      </p>
      <Button variant="primary" onClick={onNew} style={{ padding: "6px 13px", fontSize: 13, gap: 7 }}>
        <Plus size={14} />
        New script
      </Button>
    </div>
  );
}
