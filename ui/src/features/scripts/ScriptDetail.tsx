import { useEffect, useMemo, useRef, useState } from "react";
import clsx from "clsx";
import { Editor as MonacoEditor, useMonaco, type OnMount } from "@monaco-editor/react";
import type * as Monaco from "monaco-editor";
import {
  registerGeneratorLibs,
  type GeneratorDef,
} from "@/features/workspace/generator-libs";
import { Code, FloppyDisk, Package, Play, Shield, Trash } from "@/components/ui/icons";
import { Button } from "@/components/ui/Button";
import { Dialog } from "@/components/ui/Dialog";
import { Kbd } from "@/components/ui/Kbd";
import { Subtab } from "@/components/ui/Subtab";
import { EditableName } from "@/components/ui/EditableName";
import {
  useWorkspace,
  useUpdateScript,
  useDeleteScript,
  useRunScript,
  COLLECTION_ID,
} from "@/lib/workspace-query";
import { useUIStore } from "@/lib/ui-store";
import { NOCTURNE_MONACO_THEME } from "@/theme/monaco-nocturne";
import { SCRATCH_PATH } from "./monaco-scripts";
import { ScriptKind, type Script } from "@grpcview/v1/workspace_pb";
import type { RunScriptResponse } from "@grpcview/v1/service_pb";
import { kindMeta, starterSource } from "./script-kinds";
import { OutputRegion } from "./ScriptOutput";

function configDigest(source: string): string {
  let h = 0x811c9dc5;
  for (let i = 0; i < source.length; i++) {
    h ^= source.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return `cfg:${(h >>> 0).toString(16).padStart(8, "0").slice(0, 4)}`;
}

interface ScriptDraft {
  source: string;
  dirty: boolean;
  setSource: (next: string) => void;
  save: () => void;
  saving: boolean;
  testRun: () => void;
  running: boolean;
  runResult?: RunScriptResponse;
  runError: Error | null;
  rename: (next: string) => void;
  doDelete: () => void;
  deleting: boolean;
  confirmDelete: boolean;
  setConfirmDelete: (open: boolean) => void;
  outputOpen: boolean;
  toggleOutput: () => void;
  // Binds ⌘↵ / ⌘S; must be the editor's onMount so the commands see fresh closures.
  onMount: OnMount;
}

// The whole mutable half of the detail pane: the draft buffer against the saved
// script, and the four mutations that act on it.
function useScriptDraft(script: Script): ScriptDraft {
  const draftSource = useUIStore((s) => s.scriptDrafts[script.name]);
  const seedScriptDraft = useUIStore((s) => s.seedScriptDraft);
  const setScriptDraft = useUIStore((s) => s.setScriptDraft);
  const renameScript = useUIStore((s) => s.renameScript);
  const forgetScript = useUIStore((s) => s.forgetScript);

  const updateScript = useUpdateScript();
  const deleteScript = useDeleteScript();
  const runScript = useRunScript();

  const [confirmDelete, setConfirmDelete] = useState(false);
  const [outputOpen, setOutputOpen] = useState(false);

  useEffect(() => {
    seedScriptDraft(script.name, script.source || starterSource(script.kind));
  }, [script.name, script.source, script.kind, seedScriptDraft]);

  const source = draftSource ?? script.source;
  const dirty = draftSource !== undefined && draftSource !== script.source;

  const save = () => {
    if (!dirty || updateScript.isPending) return;
    updateScript.mutate({ collection: COLLECTION_ID, name: script.name, source });
  };
  const testRun = () => {
    if (!source.trim() || runScript.isPending) return;
    setOutputOpen(true);
    runScript.mutate({ collection: COLLECTION_ID, source, kind: script.kind });
  };
  const rename = (next: string) => {
    updateScript.mutate(
      { collection: COLLECTION_ID, name: script.name, newName: next },
      { onSuccess: () => renameScript(script.name, next) }
    );
  };
  const doDelete = () => {
    deleteScript.mutate(
      { collection: COLLECTION_ID, name: script.name },
      {
        onSuccess: () => {
          forgetScript(script.name);
          setConfirmDelete(false);
        },
      }
    );
  };

  // Monaco commands capture their closure once at mount, hence the refs.
  const testRunRef = useRef(testRun);
  testRunRef.current = testRun;
  const saveRef = useRef(save);
  saveRef.current = save;

  const onMount: OnMount = (editor, m) => {
    editor.addCommand(m.KeyMod.CtrlCmd | m.KeyCode.Enter, () => testRunRef.current());
    editor.addCommand(m.KeyMod.CtrlCmd | m.KeyCode.KeyS, () => saveRef.current());
  };

  return {
    source,
    dirty,
    setSource: (next) => setScriptDraft(script.name, next),
    save,
    saving: updateScript.isPending,
    testRun,
    running: runScript.isPending,
    runResult: runScript.data,
    runError: runScript.isError ? runScript.error : null,
    rename,
    doDelete,
    deleting: deleteScript.isPending,
    confirmDelete,
    setConfirmDelete,
    outputOpen,
    toggleOutput: () => setOutputOpen((o) => !o),
    onMount,
  };
}

// Registers the OTHER generators as ambient libs, so this buffer gets IntelliSense
// for the ones it can call by name.
function useGeneratorLibs(scriptName: string) {
  const { workspace } = useWorkspace();
  const monaco = useMonaco();

  const otherGenerators = useMemo<GeneratorDef[]>(() => {
    const gens = (workspace?.scripts ?? [])
      .filter((s) => s.kind === ScriptKind.GENERATOR && s.name !== scriptName)
      .map((s) => ({ name: s.name, source: s.source }));
    return gens;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    (workspace?.scripts ?? [])
      .filter((s) => s.kind === ScriptKind.GENERATOR && s.name !== scriptName)
      .map((s) => s.name + "\0" + s.source)
      .join("|"),
  ]);

  const genLibs = useRef<Monaco.IDisposable[]>([]);
  useEffect(() => {
    if (!monaco) return;
    const tsDefaults = monaco.languages.typescript.typescriptDefaults;
    genLibs.current.forEach((d) => d.dispose());
    genLibs.current = registerGeneratorLibs(tsDefaults, otherGenerators, "scripts");
    return () => {
      genLibs.current.forEach((d) => d.dispose());
      genLibs.current = [];
    };
  }, [monaco, otherGenerators]);
}

export function ScriptDetail({ script }: { script: Script }) {
  const meta = kindMeta(script.kind);
  const KindIcon = meta.Icon;

  const subtab = useUIStore((s) => s.scriptSubtab);
  const setSubtab = useUIStore((s) => s.setScriptSubtab);
  const [editingName, setEditingName] = useState(false);

  const draft = useScriptDraft(script);
  useGeneratorLibs(script.name);

  return (
    <div className="flex flex-col" style={{ flex: 1, minWidth: 0, minHeight: 0 }}>
      <div
        className="flex items-center gap-[11px]"
        style={{ flex: "none", padding: "12px 18px", borderBottom: "1px solid var(--line)" }}
      >
        <span
          className={clsx("tag", `tag-${meta.tag}`)}
          style={{ display: "inline-flex", alignItems: "center", gap: 5 }}
        >
          <KindIcon size={12} />
          {meta.label}
        </span>
        <EditableName
          value={script.name}
          editing={editingName}
          onEditingChange={setEditingName}
          onCommit={draft.rename}
          activateOnClick
          className="font-mono"
          title="Click to rename"
          ariaLabel="Script name"
          style={{
            fontSize: 15,
            color: "var(--color-text)",
            maxWidth: 260,
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
          inputStyle={{ fontSize: 15, color: "var(--color-text)", maxWidth: 260 }}
        />
        <span
          className="digest"
          title="config digest — a short client-side hash of the source (display-only; the grant-binding digest is S4)"
        >
          {configDigest(draft.source)}
        </span>

        <div className="ml-auto flex items-center gap-[8px]">
          <Button
            variant="primary"
            style={{ padding: "5px 13px", fontSize: 12, gap: 6 }}
            onClick={draft.testRun}
            disabled={draft.running || !draft.source.trim()}
            title="Run the current buffer under this script's kind"
          >
            <Play weight="fill" size={12} />
            {draft.running ? "Running…" : "Test run"}
            <Kbd>⌘↵</Kbd>
          </Button>
          <Button
            variant="secondary"
            style={{ padding: "5px 11px", fontSize: 12, gap: 6 }}
            onClick={draft.save}
            disabled={!draft.dirty || draft.saving}
            title={draft.dirty ? "Save the script's source" : "No unsaved changes"}
          >
            <FloppyDisk size={14} />
            {draft.saving ? "Saving…" : "Save"}
            <Kbd>⌘S</Kbd>
          </Button>
          <Button
            variant="danger"
            style={{ padding: "5px 11px", fontSize: 12, gap: 6 }}
            onClick={() => draft.setConfirmDelete(true)}
            title="Delete script"
          >
            <Trash size={14} />
            Delete
          </Button>
        </div>
      </div>

      <div
        className="flex items-center"
        style={{ flex: "none", padding: "0 10px", borderBottom: "1px solid var(--line)", background: "var(--color-bg)" }}
      >
        <Subtab active={subtab === "code"} onClick={() => setSubtab("code")}>
          <Code size={14} />
          Code
        </Subtab>
        <Subtab active={subtab === "deps"} onClick={() => setSubtab("deps")}>
          <Package size={14} />
          Dependencies
          <span className="tag tag-neutral" style={{ padding: "0 6px", fontSize: 10 }}>
            0
          </span>
        </Subtab>
        <Subtab active={subtab === "caps"} onClick={() => setSubtab("caps")}>
          <Shield size={14} />
          Capabilities
        </Subtab>
        <span
          className="font-mono ml-auto"
          style={{ fontSize: 11, color: "var(--color-neutral-600)", paddingRight: 6 }}
        >
          TypeScript · esbuild → ES2022
        </span>
      </div>

      <div className="flex flex-col" style={{ flex: 1, minHeight: 0 }}>
        {subtab === "code" ? (
          <MonacoEditor
            path={SCRATCH_PATH}
            language="typescript"
            theme={NOCTURNE_MONACO_THEME}
            value={draft.source}
            onChange={(v: string | undefined) => draft.setSource(v ?? "")}
            onMount={draft.onMount}
            options={{
              // monaco defaults quickSuggestions.strings=false (monaco-editor#2883).
              quickSuggestions: { other: true, comments: false, strings: true },
              automaticLayout: true,
              minimap: { enabled: false },
              scrollBeyondLastLine: false,
              fontFamily: "var(--mono)",
              fontSize: 13,
              padding: { top: 12, bottom: 12 },
              tabSize: 2,
            }}
          />
        ) : subtab === "deps" ? (
          <NoDependencies />
        ) : (
          <CapabilitiesPane />
        )}
      </div>

      <OutputRegion
        open={draft.outputOpen}
        onToggle={draft.toggleOutput}
        result={draft.runResult}
        connectError={draft.runError}
        pending={draft.running}
      />

      <Dialog
        open={draft.confirmDelete}
        onClose={() => draft.setConfirmDelete(false)}
        title="Delete script"
        width={400}
      >
        <p style={{ margin: 0, fontSize: 13, lineHeight: 1.6 }}>
          Delete <strong>{script.name}</strong>? This removes it from the collection.
        </p>
        <div className="dialog-actions">
          <Button onClick={() => draft.setConfirmDelete(false)}>Cancel</Button>
          <Button variant="danger" onClick={draft.doDelete} disabled={draft.deleting}>
            {draft.deleting ? "Deleting…" : "Delete"}
          </Button>
        </div>
      </Dialog>
    </div>
  );
}

function NoDependencies() {
  return (
    <div style={{ flex: 1, overflow: "auto", padding: "16px 18px" }}>
      <div
        className="flex flex-col items-center"
        style={{ maxWidth: 520, margin: "34px auto", textAlign: "center", gap: 12 }}
      >
        <div
          className="flex items-center justify-center"
          style={{
            width: 46,
            height: 46,
            borderRadius: 12,
            background: "var(--panel-2)",
            border: "1px solid var(--line)",
            color: "var(--color-neutral-500)",
          }}
        >
          <Package size={22} />
        </div>
        <div style={{ fontSize: 14, color: "var(--color-neutral-200)" }}>No dependencies</div>
        <p className="text-muted" style={{ fontSize: 12.5, lineHeight: 1.6, margin: 0 }}>
          This script runs on the built-in runtime and inert stdlib (
          <span className="font-mono">node:path</span>, text codecs) — nothing is fetched,
          pinned, or executed at install.
        </p>
      </div>
    </div>
  );
}

function CapabilitiesPane() {
  return (
    <div style={{ flex: 1, overflow: "auto", padding: "16px 18px" }}>
      <div
        className="flex flex-col items-center"
        style={{ maxWidth: 540, margin: "30px auto", textAlign: "center", gap: 12 }}
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
          <Shield size={24} />
        </div>
        <div style={{ fontSize: 14, color: "var(--color-neutral-200)" }}>Bounded, network open</div>
        <p className="text-muted" style={{ fontSize: 12.5, lineHeight: 1.6, margin: 0 }}>
          QuickJS·WASM enforces hard memory + wall-clock bounds on every run — that part is
          real today. Network is not gated: a browser-style global{" "}
          <span className="font-mono">fetch</span> is there for every script, with no grant to
          ask for and no way to withhold it. Filesystem (<span className="font-mono">node:fs</span>)
          and process access are denied, because nothing can grant them yet. Per-capability
          grants — and this pane asking for consent — are planned; there is nothing to approve
          here now.
        </p>
      </div>
    </div>
  );
}
