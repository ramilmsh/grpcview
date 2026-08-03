import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type ComponentType,
  type ReactNode,
} from "react";
import clsx from "clsx";
import { Editor as MonacoEditor, useMonaco, type OnMount } from "@monaco-editor/react";
import type * as Monaco from "monaco-editor";
import {
  registerGeneratorLibs,
  type GeneratorDef,
} from "@/features/workspace/generator-libs";
import {
  ArrowsSplit,
  BracketsCurly,
  CaretDown,
  CaretRight,
  Code,
  Flask,
  FloppyDisk,
  Function as FunctionIcon,
  MagnifyingGlass,
  Package,
  Play,
  Plus,
  Shield,
  ShieldCheck,
  Trash,
  Warning,
  type IconProps,
} from "@/components/ui/icons";
import { Button, IconButton } from "@/components/ui/Button";
import { Dialog } from "@/components/ui/Dialog";
import { Input, Field } from "@/components/ui/Input";
import { Kbd } from "@/components/ui/Kbd";
import { Subtab } from "@/components/ui/Subtab";
import { EditableName } from "@/components/ui/EditableName";
import {
  useWorkspace,
  useCreateScript,
  useUpdateScript,
  useDeleteScript,
  useRunScript,
  WORKSPACE_NAME,
} from "@/lib/workspace-query";
import { useUIStore } from "@/lib/ui-store";
import { NOCTURNE_MONACO_THEME } from "@/theme/monaco-nocturne";
import { SCRATCH_PATH } from "./monaco-scripts";
import { ScriptKind, type Script } from "@grpcview/v1/workspace_pb";
import type { RunScriptResponse } from "@grpcview/v1/service_pb";

type IconComp = ComponentType<IconProps>;

interface KindMeta {
  label: string;
  section: string;
  Icon: IconComp;
  color: string;
  tag: "accent" | "accent-2" | "neutral";
}

const SCENARIO_META: KindMeta = {
  label: "Scenario",
  section: "Scenarios",
  Icon: Flask,
  color: "var(--ok)",
  tag: "neutral",
};

const KIND_META: Partial<Record<ScriptKind, KindMeta>> = {
  [ScriptKind.MIDDLEWARE]: {
    label: "Middleware",
    section: "Middleware",
    Icon: ArrowsSplit,
    color: "var(--color-accent)",
    tag: "accent",
  },
  [ScriptKind.GENERATOR]: {
    label: "Generator",
    section: "Generators",
    Icon: FunctionIcon,
    color: "var(--color-accent-2-300)",
    tag: "accent-2",
  },
  [ScriptKind.SCENARIO]: SCENARIO_META,
};

const kindMeta = (kind: ScriptKind): KindMeta => KIND_META[kind] ?? SCENARIO_META;

const SIDEBAR_ORDER: ScriptKind[] = [
  ScriptKind.MIDDLEWARE,
  ScriptKind.GENERATOR,
  ScriptKind.SCENARIO,
];
const NEW_KIND_ORDER: ScriptKind[] = [
  ScriptKind.GENERATOR,
  ScriptKind.MIDDLEWARE,
  ScriptKind.SCENARIO,
];

function starterSource(kind: ScriptKind): string {
  switch (kind) {
    case ScriptKind.GENERATOR:
      return `// Generator — its default export is invoked by name: call name() from a
// request body/metadata or from another generator. Test run calls it with no arguments.
export default () => {
  return new Date().toISOString();
};
`;
    case ScriptKind.MIDDLEWARE:
      return `// Middleware — runs before invoke. Mutate ctx (body / metadata / target) and
// return it. Test run calls handle with an empty ctx.
export function handle(ctx) {
  console.log("middleware ran");
  return ctx;
}
`;
    default:
      return `// Scenario — runs as a scratchpad: the value is the last expression. dayjs is
// available; there are no capabilities or workspace inputs.
import dayjs from "dayjs";

console.log("running in QuickJS (wasm)");

({ today: dayjs().format("YYYY-MM-DD"), engine: "quickjs-wasm" })
`;
  }
}

function configDigest(source: string): string {
  let h = 0x811c9dc5;
  for (let i = 0; i < source.length; i++) {
    h ^= source.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return `cfg:${(h >>> 0).toString(16).padStart(8, "0").slice(0, 4)}`;
}

const LEVEL_COLOR: Record<string, string | undefined> = {
  error: "var(--err-fg)",
  warn: "var(--warn)",
  debug: "var(--color-neutral-500)",
};

function prettyValue(value: string): string {
  try {
    return JSON.stringify(JSON.parse(value), null, 2);
  } catch {
    return value;
  }
}

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
      { workspaceName: WORKSPACE_NAME, name, kind },
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

function ScriptSidebar({
  scripts,
  selectedName,
  onSelect,
  onNew,
}: {
  scripts: Script[];
  selectedName: string | null;
  onSelect: (name: string) => void;
  onNew: () => void;
}) {
  const [filter, setFilter] = useState("");
  const q = filter.trim().toLowerCase();
  const visible = q
    ? scripts.filter((s) => s.name.toLowerCase().includes(q))
    : scripts;

  const sections = SIDEBAR_ORDER.map((kind) => ({
    kind,
    meta: kindMeta(kind),
    rows: visible.filter((s) => s.kind === kind),
  })).filter((sec) => sec.rows.length > 0);

  return (
    <div
      className="bg-panel flex flex-col"
      style={{ width: 280, flex: "none", borderRight: "1px solid var(--line)", minHeight: 0 }}
    >
      <div
        className="flex items-center gap-[8px]"
        style={{ height: 40, flex: "none", padding: "0 12px", borderBottom: "1px solid var(--line)" }}
      >
        <MagnifyingGlass size={14} style={{ color: "var(--color-neutral-500)" }} />
        <input
          className="bare"
          style={{ fontSize: 13 }}
          placeholder="Filter scripts…"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
        <IconButton title="New script" onClick={onNew}>
          <Plus />
        </IconButton>
      </div>

      <div style={{ flex: 1, overflow: "auto", padding: "10px 8px" }}>
        {scripts.length === 0 ? (
          <div
            className="text-muted"
            style={{ fontSize: 12, padding: "16px 6px", lineHeight: 1.6 }}
          >
            No scripts yet. Use + to create a generator, middleware, or scenario.
          </div>
        ) : sections.length === 0 ? (
          <div
            className="text-muted"
            style={{ fontSize: 12, padding: "16px 6px", lineHeight: 1.6 }}
          >
            No scripts match “{filter.trim()}”.
          </div>
        ) : (
          sections.map(({ kind, meta, rows }, i) => {
            const SectionIcon = meta.Icon;
            return (
              <div key={kind}>
                <div
                  className="flex items-center gap-[6px]"
                  style={{ padding: `${i === 0 ? 2 : 14}px 6px 6px` }}
                >
                  <SectionIcon size={14} style={{ color: meta.color }} />
                  <span
                    style={{
                      fontSize: 10,
                      letterSpacing: ".1em",
                      textTransform: "uppercase",
                      color: "var(--color-neutral-500)",
                    }}
                  >
                    {meta.section}
                  </span>
                </div>
                {rows.map((s) => (
                  <div
                    key={s.name}
                    className={clsx("scriptrow", s.name === selectedName && "on")}
                    onClick={() => onSelect(s.name)}
                  >
                    <SectionIcon size={16} style={{ color: meta.color, flex: "none" }} />
                    <span
                      style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
                    >
                      {s.name}
                    </span>
                  </div>
                ))}
              </div>
            );
          })
        )}
      </div>

      <div
        className="flex items-center gap-[7px] font-mono"
        style={{
          flex: "none",
          padding: "9px 12px",
          borderTop: "1px solid var(--line)",
          fontSize: 11,
          color: "var(--color-neutral-500)",
        }}
        title="QuickJS compiled to WASM, run in-process by wazero — hard memory + wall-clock bounds, default-deny host access"
      >
        <ShieldCheck size={14} style={{ color: "var(--ok)" }} />
        QuickJS·WASM<span style={{ color: "var(--color-neutral-700)" }}>·</span>sandboxed
      </div>
    </div>
  );
}

function ScriptDetail({ script }: { script: Script }) {
  const meta = kindMeta(script.kind);
  const KindIcon = meta.Icon;

  const { workspace } = useWorkspace();
  const monaco = useMonaco();

  const subtab = useUIStore((s) => s.scriptSubtab);
  const setSubtab = useUIStore((s) => s.setScriptSubtab);
  const draftSource = useUIStore((s) => s.scriptDrafts[script.name]);
  const seedScriptDraft = useUIStore((s) => s.seedScriptDraft);
  const setScriptDraft = useUIStore((s) => s.setScriptDraft);
  const renameScript = useUIStore((s) => s.renameScript);
  const forgetScript = useUIStore((s) => s.forgetScript);

  const updateScript = useUpdateScript();
  const deleteScript = useDeleteScript();
  const runScript = useRunScript();

  const [editingName, setEditingName] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [outputOpen, setOutputOpen] = useState(false);

  useEffect(() => {
    seedScriptDraft(script.name, script.source || starterSource(script.kind));
  }, [script.name, script.source, script.kind, seedScriptDraft]);

  const source = draftSource ?? script.source;
  const dirty = draftSource !== undefined && draftSource !== script.source;

  const save = () => {
    if (!dirty || updateScript.isPending) return;
    updateScript.mutate({ workspaceName: WORKSPACE_NAME, name: script.name, source });
  };
  const testRun = () => {
    if (!source.trim() || runScript.isPending) return;
    setOutputOpen(true);
    runScript.mutate({ workspaceName: WORKSPACE_NAME, source, kind: script.kind });
  };
  const rename = (next: string) => {
    updateScript.mutate(
      { workspaceName: WORKSPACE_NAME, name: script.name, newName: next },
      { onSuccess: () => renameScript(script.name, next) }
    );
  };
  const doDelete = () => {
    deleteScript.mutate(
      { workspaceName: WORKSPACE_NAME, name: script.name },
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

  const otherGenerators = useMemo<GeneratorDef[]>(() => {
    const gens = (workspace?.scripts ?? [])
      .filter((s) => s.kind === ScriptKind.GENERATOR && s.name !== script.name)
      .map((s) => ({ name: s.name, source: s.source }));
    return gens;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    (workspace?.scripts ?? [])
      .filter((s) => s.kind === ScriptKind.GENERATOR && s.name !== script.name)
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
          onCommit={rename}
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
          {configDigest(source)}
        </span>

        <div className="ml-auto flex items-center gap-[8px]">
          <Button
            variant="primary"
            style={{ padding: "5px 13px", fontSize: 12, gap: 6 }}
            onClick={testRun}
            disabled={runScript.isPending || !source.trim()}
            title="Run the current buffer under this script's kind"
          >
            <Play weight="fill" size={12} />
            {runScript.isPending ? "Running…" : "Test run"}
            <Kbd>⌘↵</Kbd>
          </Button>
          <Button
            variant="secondary"
            style={{ padding: "5px 11px", fontSize: 12, gap: 6 }}
            onClick={save}
            disabled={!dirty || updateScript.isPending}
            title={dirty ? "Save the script's source" : "No unsaved changes"}
          >
            <FloppyDisk size={14} />
            {updateScript.isPending ? "Saving…" : "Save"}
            <Kbd>⌘S</Kbd>
          </Button>
          <Button
            variant="danger"
            style={{ padding: "5px 11px", fontSize: 12, gap: 6 }}
            onClick={() => setConfirmDelete(true)}
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
            value={source}
            onChange={(v: string | undefined) => setScriptDraft(script.name, v ?? "")}
            onMount={onMount}
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
          <FullySandboxed />
        )}
      </div>

      <OutputRegion
        open={outputOpen}
        onToggle={() => setOutputOpen((o) => !o)}
        result={runScript.data}
        connectError={runScript.isError ? runScript.error : null}
        pending={runScript.isPending}
      />

      <Dialog
        open={confirmDelete}
        onClose={() => setConfirmDelete(false)}
        title="Delete script"
        width={400}
      >
        <p style={{ margin: 0, fontSize: 13, lineHeight: 1.6 }}>
          Delete <strong>{script.name}</strong>? This removes it from the collection.
        </p>
        <div className="dialog-actions">
          <Button onClick={() => setConfirmDelete(false)}>Cancel</Button>
          <Button variant="danger" onClick={doDelete} disabled={deleteScript.isPending}>
            {deleteScript.isPending ? "Deleting…" : "Delete"}
          </Button>
        </div>
      </Dialog>
    </div>
  );
}

function OutputRegion({
  open,
  onToggle,
  result,
  connectError,
  pending,
}: {
  open: boolean;
  onToggle: () => void;
  result?: RunScriptResponse;
  connectError: Error | null;
  pending: boolean;
}) {
  let summary = "no run yet";
  let summaryColor = "var(--color-neutral-600)";
  if (pending) {
    summary = "running…";
    summaryColor = "var(--color-neutral-400)";
  } else if (connectError) {
    summary = "engine error";
    summaryColor = "var(--err-fg)";
  } else if (result?.error) {
    summary = "uncaught error";
    summaryColor = "var(--err-fg)";
  } else if (result) {
    const logs = result.logs.length ? ` · ${result.logs.length} logs` : "";
    summary = (result.value !== undefined ? "value returned" : "no value") + logs;
    summaryColor = result.value !== undefined ? "var(--ok)" : "var(--color-neutral-500)";
  }

  return (
    <div
      className="flex flex-col"
      style={{ flex: "none", borderTop: "1px solid var(--line)", minHeight: 0, ...(open ? { height: 260 } : {}) }}
    >
      <button
        className="bg-panel flex items-center gap-[8px]"
        style={{
          flex: "none",
          height: 34,
          width: "100%",
          padding: "0 16px",
          border: "none",
          cursor: "pointer",
          textAlign: "left",
          fontFamily: "inherit",
          color: "var(--color-neutral-300)",
        }}
        onClick={onToggle}
        title="Test-run output"
      >
        {open ? (
          <CaretDown size={12} style={{ color: "var(--color-neutral-500)" }} />
        ) : (
          <CaretRight size={12} style={{ color: "var(--color-neutral-500)" }} />
        )}
        <span
          style={{
            fontSize: 10,
            letterSpacing: ".1em",
            textTransform: "uppercase",
            color: "var(--color-neutral-500)",
          }}
        >
          Test run output
        </span>
        <span className="font-mono ml-auto" style={{ fontSize: 11, color: summaryColor }}>
          {summary}
        </span>
      </button>
      {open && (
        <div
          style={{
            flex: 1,
            overflow: "auto",
            padding: "12px 16px",
            borderTop: "1px solid var(--line)",
            background: "var(--color-bg)",
          }}
        >
          <OutputPane result={result} connectError={connectError} pending={pending} />
        </div>
      )}
    </div>
  );
}

function OutputPane({
  result,
  connectError,
  pending,
}: {
  result?: RunScriptResponse;
  connectError: Error | null;
  pending: boolean;
}) {
  if (connectError) {
    return (
      <ErrorBox title="Engine error">
        <ErrorDetail>{connectError.message}</ErrorDetail>
      </ErrorBox>
    );
  }
  if (!result) {
    return (
      <Muted>
        {pending
          ? "Running…"
          : "Press Test run (⌘↵) to evaluate the buffer. Its value, console output, and any error appear here."}
      </Muted>
    );
  }

  const { value, logs, error } = result;
  return (
    <div className="flex flex-col" style={{ gap: 16 }}>
      {error ? (
        <ErrorBox title={`Uncaught ${error.message}`}>
          {error.line > 0 && <ErrorDetail>line {error.line}</ErrorDetail>}
          {error.stack && (
            <pre
              className="font-mono"
              style={{ margin: "6px 0 0", fontSize: 12, whiteSpace: "pre-wrap" }}
            >
              {error.stack}
            </pre>
          )}
        </ErrorBox>
      ) : (
        <Section label="Value">
          {value === undefined ? (
            <Muted>undefined — the script produced no value.</Muted>
          ) : (
            <pre
              className="font-mono"
              style={{ margin: 0, fontSize: 13, whiteSpace: "pre-wrap", lineHeight: 1.55 }}
            >
              {prettyValue(value)}
            </pre>
          )}
        </Section>
      )}

      <Section label={logs.length ? `Console (${logs.length})` : "Console"}>
        {logs.length === 0 ? (
          <Muted>No console output.</Muted>
        ) : (
          <div className="flex flex-col font-mono" style={{ gap: 3, fontSize: 12.5 }}>
            {logs.map((line, i) => (
              <div key={i} style={{ display: "flex", gap: 8, whiteSpace: "pre-wrap" }}>
                <span style={{ color: "var(--color-neutral-600)", flex: "none", width: 42 }}>
                  {line.level}
                </span>
                <span style={{ color: LEVEL_COLOR[line.level] }}>{line.message}</span>
              </div>
            ))}
          </div>
        )}
      </Section>
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
        to author and test-run it — fully sandboxed.
      </p>
      <Button variant="primary" onClick={onNew} style={{ padding: "6px 13px", fontSize: 13, gap: 7 }}>
        <Plus size={14} />
        New script
      </Button>
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

function FullySandboxed() {
  return (
    <div style={{ flex: 1, overflow: "auto", padding: "16px 18px" }}>
      <div
        className="flex flex-col items-center"
        style={{ maxWidth: 540, margin: "30px auto", textAlign: "center", gap: 12 }}
      >
        <div
          className="flex items-center justify-center"
          style={{ width: 48, height: 48, borderRadius: 13, background: "var(--ok-bg)", color: "var(--ok)" }}
        >
          <ShieldCheck size={24} weight="fill" />
        </div>
        <div style={{ fontSize: 14, color: "var(--color-neutral-200)" }}>Fully sandboxed</div>
        <p className="text-muted" style={{ fontSize: 12.5, lineHeight: 1.6, margin: 0 }}>
          No capabilities requested. QuickJS·WASM enforces hard memory + wall-clock bounds;
          this script has no filesystem, network, or process access. Importing{" "}
          <span className="font-mono">node:fs</span>, <span className="font-mono">std/http</span>,
          or <span className="font-mono">exec</span> would surface here for consent.
        </p>
      </div>
    </div>
  );
}

function NewScriptDialog({
  open,
  onClose,
  onCreate,
  pending,
  error,
  existingNames,
}: {
  open: boolean;
  onClose: () => void;
  onCreate: (name: string, kind: ScriptKind) => void;
  pending: boolean;
  error: Error | null;
  existingNames: string[];
}) {
  const [name, setName] = useState("");
  const [kind, setKind] = useState<ScriptKind>(ScriptKind.GENERATOR);

  useEffect(() => {
    if (open) {
      setName("");
      setKind(ScriptKind.GENERATOR);
    }
  }, [open]);

  const trimmed = name.trim();
  const collision = existingNames.includes(trimmed);
  const canCreate = !!trimmed && !collision && !pending;
  const submit = () => {
    if (canCreate) onCreate(trimmed, kind);
  };

  return (
    <Dialog open={open} onClose={onClose} title="New script" width={420}>
      <div className="kindseg">
        {NEW_KIND_ORDER.map((k) => {
          const m = kindMeta(k);
          const OptIcon = m.Icon;
          return (
            <button
              key={k}
              type="button"
              className={clsx("kindopt", k === kind && "on")}
              onClick={() => setKind(k)}
            >
              <OptIcon size={14} />
              {m.label}
            </button>
          );
        })}
      </div>
      <Field label="Name">
        <Input
          autoFocus
          placeholder="e.g. uuid, sign-request"
          value={name}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") submit();
          }}
        />
      </Field>
      {collision && (
        <p style={{ margin: 0, fontSize: 12, color: "var(--err-fg)" }}>
          A script named “{trimmed}” already exists.
        </p>
      )}
      {error && !collision && (
        <p style={{ margin: 0, fontSize: 12, color: "var(--err-fg)" }}>{error.message}</p>
      )}
      <div className="dialog-actions">
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" onClick={submit} disabled={!canCreate}>
          {pending ? "Creating…" : "Create"}
        </Button>
      </div>
    </Dialog>
  );
}

function Muted({ children }: { children: ReactNode }) {
  return (
    <div className="text-muted" style={{ fontSize: 13, lineHeight: 1.6 }}>
      {children}
    </div>
  );
}

function Section({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div>
      <div
        style={{
          fontSize: 11,
          textTransform: "uppercase",
          letterSpacing: 0.6,
          color: "var(--color-neutral-500)",
          marginBottom: 6,
        }}
      >
        {label}
      </div>
      {children}
    </div>
  );
}

function ErrorBox({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div
      style={{
        padding: "10px 12px",
        borderRadius: 8,
        background: "var(--err-bg)",
        border: "1px solid var(--err-border)",
        color: "var(--err-fg)",
      }}
    >
      <div className="flex items-center gap-[8px]" style={{ fontSize: 13 }}>
        <Warning weight="fill" />
        <span className="font-mono">{title}</span>
      </div>
      {children}
    </div>
  );
}

function ErrorDetail({ children }: { children: ReactNode }) {
  return <div style={{ fontSize: 12, opacity: 0.85, marginTop: 4 }}>{children}</div>;
}
