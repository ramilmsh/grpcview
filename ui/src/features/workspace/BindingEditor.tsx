import { useEffect, useState, type ReactNode } from "react";
import clsx from "clsx";
import { Editor as MonacoEditor } from "@monaco-editor/react";
import {
  ArrowClockwise,
  CheckCircle,
  Function as FunctionIcon,
  Plus,
  Warning,
  X,
} from "@/components/ui/icons";
import { Button, IconButton } from "@/components/ui/Button";
import { Backdrop } from "@/components/ui/Backdrop";
import {
  useWorkspace,
  useCreateScript,
  useUpdateScript,
  useRunScript,
  WORKSPACE_NAME,
} from "@/lib/workspace-query";
import { useUIStore } from "@/lib/ui-store";
import { NOCTURNE_MONACO_THEME } from "@/theme/monaco-nocturne";
// Reuse the Scripts view's Monaco TypeScript setup (compiler options, dayjs types,
// ambient env) — importing the model path evaluates that side-effect module. The
// two editors never mount at once (Scripts and Workspace are separate views), so
// sharing the model path is safe and keeps IntelliSense identical.
import { SCRATCH_PATH } from "@/features/scripts/monaco-scripts";
import { ScriptKind, type Script } from "@grpcview/v1/workspace_pb";
import type { RunScriptResponse } from "@grpcview/v1/service_pb";

// BindingEditor is the S2 binding-editor modal (plan §S2, mockup L990–1083): it opens
// for the generator NAMED by a clicked `{{ … }}` token in the request body / metadata
// and lets you edit that generator's source, preview its resolved value (Test run),
// and pick a caching policy (UI-only this pass). Saving persists the generator via
// UpdateScript. A token that names a generator which does not exist yet shows an empty
// state offering to create it. The generator is server data (rides the Get snapshot);
// only its draft source buffer is UI state (ui-store bindingDrafts).

const CACHING_OPTS = [
  { id: "every", label: "Every invoke" },
  { id: "inputs", label: "By inputs" },
  { id: "expiry", label: "Until value expiry" },
] as const;
type Caching = (typeof CACHING_OPTS)[number]["id"];

// starterFor seeds a newly created / empty generator's buffer with a skeleton in the
// generator calling convention (plan §2.5), so it is immediately test-runnable.
function starterFor(name: string): string {
  return `// Generator "${name}" — its return value is spliced into a request wherever
// {{ ${name}() }} appears. Test run calls this default export; on invoke it runs uncached.
export default () => {
  return new Date().toISOString();
};
`;
}

// previewText renders a generator's JSON result for the preview: a string result is
// shown unquoted, any other value is pretty-printed.
function previewText(value?: string): string {
  if (value === undefined) return "";
  try {
    const parsed = JSON.parse(value);
    return typeof parsed === "string" ? parsed : JSON.stringify(parsed, null, 2);
  } catch {
    return value;
  }
}

export function BindingEditor() {
  const open = useUIStore((s) => s.bindingOpen);
  const name = useUIStore((s) => s.bindingGenerator);
  const close = useUIStore((s) => s.closeBinding);
  // Gate the data-fetching inner component behind the open flag so its hooks (and
  // the RunScript mutation) exist only while the modal is open.
  if (!open || !name) return null;
  return <BindingModal name={name} onClose={close} />;
}

function BindingModal({ name, onClose }: { name: string; onClose: () => void }) {
  const { workspace } = useWorkspace();
  const generator =
    workspace?.scripts.find((s) => s.name === name && s.kind === ScriptKind.GENERATOR) ??
    null;

  return (
    <Backdrop onClose={onClose}>
      <div
        role="dialog"
        aria-modal="true"
        onClick={(e) => e.stopPropagation()}
        style={{
          width: 600,
          maxWidth: "95vw",
          maxHeight: "88vh",
          display: "flex",
          flexDirection: "column",
          background: "var(--panel)",
          border: "1px solid var(--line)",
          borderRadius: 12,
          boxShadow: "var(--shadow-lg)",
          overflow: "hidden",
        }}
      >
        <Header name={name} onClose={onClose} />
        {generator ? (
          <BindingBody generator={generator} onClose={onClose} />
        ) : (
          <MissingGenerator name={name} onClose={onClose} />
        )}
      </div>
    </Backdrop>
  );
}

function Header({ name, onClose }: { name: string; onClose: () => void }) {
  return (
    <div
      className="flex items-center gap-[11px]"
      style={{ flex: "none", padding: "14px 18px", borderBottom: "1px solid var(--line)" }}
    >
      <div
        className="flex items-center justify-center"
        style={{
          width: 30,
          height: 30,
          borderRadius: 8,
          background: "var(--color-accent-2-900)",
          color: "var(--color-accent-2-300)",
          flex: "none",
        }}
      >
        <FunctionIcon size={16} />
      </div>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div className="flex items-center gap-[8px]">
          <span
            className="font-mono"
            style={{
              fontWeight: 600,
              fontSize: 15,
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
            }}
          >
            {name}
          </span>
          <span
            className="tag tag-accent-2"
            style={{ fontSize: 10.5, padding: "1px 7px", flex: "none" }}
          >
            generator
          </span>
        </div>
        <div style={{ fontSize: 12, color: "var(--color-neutral-500)" }}>
          Edit the generator this token resolves to · QuickJS·WASM sandbox
        </div>
      </div>
      <IconButton onClick={onClose} title="Close">
        <X size={18} />
      </IconButton>
    </div>
  );
}

function BindingBody({ generator, onClose }: { generator: Script; onClose: () => void }) {
  const name = generator.name;
  const draft = useUIStore((s) => s.bindingDrafts[name]);
  const seedBindingDraft = useUIStore((s) => s.seedBindingDraft);
  const setBindingDraft = useUIStore((s) => s.setBindingDraft);

  const updateScript = useUpdateScript();
  const runScript = useRunScript();
  const [caching, setCaching] = useState<Caching>("every");

  // Seed the buffer from the server source (or a starter for an empty generator) once
  // per generator — idempotent, then the draft is authoritative (mirrors scriptDrafts).
  useEffect(() => {
    seedBindingDraft(name, generator.source || starterFor(name));
  }, [name, generator.source, seedBindingDraft]);

  const source = draft ?? generator.source;
  const dirty = draft !== undefined && draft !== generator.source;

  const testRun = () => {
    if (!source.trim() || runScript.isPending) return;
    runScript.mutate({ source, kind: ScriptKind.GENERATOR });
  };
  const save = () => {
    if (!dirty || updateScript.isPending) return;
    updateScript.mutate(
      { workspaceName: WORKSPACE_NAME, name, source },
      { onSuccess: onClose }
    );
  };

  return (
    <>
      <div
        style={{
          flex: 1,
          display: "flex",
          flexDirection: "column",
          minWidth: 0,
          overflow: "auto",
          paddingBottom: 6,
        }}
      >
        {/* script editor */}
        <Section label="Script">
          <div
            style={{
              height: 172,
              border: "1px solid var(--line)",
              borderRadius: 8,
              overflow: "hidden",
              background: "var(--color-bg)",
            }}
          >
            <MonacoEditor
              path={SCRATCH_PATH}
              language="typescript"
              theme={NOCTURNE_MONACO_THEME}
              value={source}
              onChange={(v: string | undefined) => setBindingDraft(name, v ?? "")}
              options={{
                automaticLayout: true,
                minimap: { enabled: false },
                scrollBeyondLastLine: false,
                fontFamily: "var(--mono)",
                fontSize: 12.5,
                padding: { top: 10, bottom: 10 },
                tabSize: 2,
              }}
            />
          </div>
          <div
            className="font-mono"
            style={{ marginTop: 8, fontSize: 11, color: "var(--color-neutral-600)" }}
          >
            QuickJS·WASM · sandboxed · no host access
          </div>
        </Section>

        {/* resolved preview */}
        <Section label="Resolved preview">
          <Preview
            result={runScript.data}
            connectError={runScript.isError ? runScript.error : null}
            pending={runScript.isPending}
            onTestRun={testRun}
          />
        </Section>

        {/* caching (UI-only this pass) */}
        <Section label="Caching">
          <div className="flex" style={{ gap: 6, flexWrap: "wrap" }}>
            {CACHING_OPTS.map((o) => (
              <button
                key={o.id}
                type="button"
                className={clsx("freshopt", caching === o.id && "on")}
                onClick={() => setCaching(o.id)}
              >
                {o.label}
              </button>
            ))}
          </div>
          <p className="text-muted" style={{ fontSize: 11.5, margin: "10px 0 0", lineHeight: 1.55 }}>
            Display-only this pass. Server-side, every invoke re-runs the generator{" "}
            <span className="font-mono">uncached</span> (so{" "}
            <span className="font-mono">uuid()</span> / <span className="font-mono">now()</span>{" "}
            vary per call); “By inputs” and “Until value expiry” are not yet enforced.
          </p>
        </Section>
      </div>

      {/* footer */}
      <div
        className="flex items-center gap-[12px]"
        style={{ flex: "none", padding: "12px 18px", borderTop: "1px solid var(--line)" }}
      >
        {updateScript.error && (
          <span style={{ fontSize: 11, color: "var(--err-fg)" }}>
            {updateScript.error.message}
          </span>
        )}
        <div className="ml-auto flex" style={{ gap: 8 }}>
          <Button onClick={onClose} style={{ padding: "6px 14px", fontSize: 13 }}>
            Cancel
          </Button>
          <Button
            variant="primary"
            onClick={save}
            disabled={!dirty || updateScript.isPending}
            style={{ padding: "6px 16px", fontSize: 13 }}
            title={dirty ? "Persist the generator's source" : "No unsaved changes"}
          >
            {updateScript.isPending ? "Saving…" : "Save generator"}
          </Button>
        </div>
      </div>
    </>
  );
}

function Preview({
  result,
  connectError,
  pending,
  onTestRun,
}: {
  result?: RunScriptResponse;
  connectError: Error | null;
  pending: boolean;
  onTestRun: () => void;
}) {
  const failed = !!connectError || !!result?.error;
  const errorText = connectError
    ? connectError.message
    : result?.error
      ? `Uncaught ${result.error.message}`
      : "";

  return (
    <>
      <div
        className="font-mono"
        style={{
          background: "var(--color-bg)",
          border: "1px solid var(--line)",
          borderRadius: 8,
          padding: "10px 12px",
          fontSize: 12.5,
          minHeight: 40,
          maxHeight: 140,
          overflow: "auto",
          color: failed ? "var(--err-fg)" : "var(--color-neutral-300)",
          whiteSpace: "pre-wrap",
          lineHeight: 1.55,
        }}
      >
        {pending ? (
          <span style={{ color: "var(--color-neutral-500)" }}>running…</span>
        ) : failed ? (
          errorText
        ) : result?.value !== undefined ? (
          previewText(result.value)
        ) : result ? (
          <span style={{ color: "var(--color-neutral-500)" }}>
            no value — the generator returned undefined.
          </span>
        ) : (
          <span style={{ color: "var(--color-neutral-600)" }}>
            Press Test run to preview the value this token resolves to.
          </span>
        )}
      </div>
      <div
        className="flex items-center gap-[10px] font-mono"
        style={{ marginTop: 9, fontSize: 11.5, color: "var(--color-neutral-500)" }}
      >
        {pending ? (
          <span className="inline-flex items-center gap-[6px]" style={{ color: "var(--color-accent-2-300)" }}>
            <span className="dot pulse" style={{ background: "var(--color-accent-2-300)" }} />
            resolving…
          </span>
        ) : failed ? (
          <span className="inline-flex items-center gap-[6px]" style={{ color: "var(--err-fg)" }}>
            <Warning weight="fill" /> failed
          </span>
        ) : result?.value !== undefined ? (
          <span className="inline-flex items-center gap-[6px]" style={{ color: "var(--ok)" }}>
            <CheckCircle weight="fill" /> resolved · uncached (varies per invoke)
          </span>
        ) : (
          <span>not run yet</span>
        )}
        <Button
          variant="ghost"
          onClick={onTestRun}
          disabled={pending}
          style={{ marginLeft: "auto", fontSize: 11.5, padding: "3px 9px", gap: 5 }}
        >
          <ArrowClockwise size={13} /> Test run
        </Button>
      </div>
    </>
  );
}

function MissingGenerator({ name, onClose }: { name: string; onClose: () => void }) {
  const createScript = useCreateScript();
  // On success the Get cache re-seeds with the new Workspace, so the generator is
  // found and this modal re-renders into the editor view — no manual switch needed.
  const create = () => {
    if (createScript.isPending) return;
    createScript.mutate({ workspaceName: WORKSPACE_NAME, name, kind: ScriptKind.GENERATOR });
  };

  return (
    <>
      <div style={{ flex: 1, overflow: "auto", padding: "10px 18px 18px" }}>
        <div
          className="flex flex-col items-center"
          style={{ maxWidth: 460, margin: "26px auto", textAlign: "center", gap: 12 }}
        >
          <div
            className="flex items-center justify-center"
            style={{
              width: 46,
              height: 46,
              borderRadius: 12,
              background: "var(--color-accent-2-900)",
              color: "var(--color-accent-2-300)",
            }}
          >
            <FunctionIcon size={22} />
          </div>
          <div style={{ fontSize: 15, color: "var(--color-neutral-100)" }}>
            No generator named <span className="font-mono">{name}</span>
          </div>
          <p className="text-muted" style={{ fontSize: 12.5, lineHeight: 1.6, margin: 0 }}>
            The token{" "}
            <span className="tok gen" style={{ margin: 0 }}>
              {name}
            </span>{" "}
            references a generator that doesn’t exist in this workspace yet. Create it to
            author what this token resolves to — fully sandboxed.
          </p>
          <Button
            variant="primary"
            onClick={create}
            disabled={createScript.isPending}
            style={{ padding: "6px 14px", fontSize: 13, gap: 7 }}
          >
            <Plus size={14} />
            {createScript.isPending ? "Creating…" : "Create generator"}
          </Button>
          {createScript.error && (
            <p style={{ margin: 0, fontSize: 12, color: "var(--err-fg)" }}>
              {createScript.error.message}
            </p>
          )}
        </div>
      </div>
      <div
        className="flex items-center"
        style={{ flex: "none", padding: "12px 18px", borderTop: "1px solid var(--line)" }}
      >
        <Button onClick={onClose} className="ml-auto" style={{ padding: "6px 14px", fontSize: 13 }}>
          Cancel
        </Button>
      </div>
    </>
  );
}

function Section({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div style={{ padding: "15px 18px 4px" }}>
      <div
        style={{
          fontSize: 10,
          letterSpacing: ".1em",
          textTransform: "uppercase",
          color: "var(--color-neutral-600)",
          marginBottom: 9,
        }}
      >
        {label}
      </div>
      {children}
    </div>
  );
}
