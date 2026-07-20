import { useRef, useState, type ReactNode } from "react";
import { Editor as MonacoEditor, type OnMount } from "@monaco-editor/react";
import { Play, Warning } from "@/components/ui/icons";
import { Button } from "@/components/ui/Button";
import { Kbd } from "@/components/ui/Kbd";
import { useRunScript } from "@/lib/workspace-query";
import { NOCTURNE_MONACO_THEME } from "@/theme/monaco-nocturne";
// Side-effect import (configures the TS language service for the scratch buffer)
// plus the model path it hands the editor. See monaco-scripts.ts.
import { SCRATCH_PATH } from "./monaco-scripts";
import type { RunScriptResponse } from "@grpcview/v1/service_pb";

// ScriptsView is a minimal scratchpad that validates the scripting engine end to
// end: type TypeScript, Run, see the value / console output / error the
// QuickJS-in-wasm engine returns. The editor is Monaco with real TypeScript
// IntelliSense — autocomplete / hover / signature help / diagnostics for the
// script environment and for the bundled dayjs library (see monaco-scripts.ts).
// It runs the scenario profile with NO capabilities and NO workspace inputs (see
// the RunScript RPC); capability grants, request/vars/env inputs, and saved
// scripts arrive with the engine's Management UI (next-steps §6).
const DEFAULT_SNIPPET = `// TypeScript with IntelliSense — try typing "dayjs()." below.
// Top-level await is allowed, and the script's VALUE is its last expression.
import dayjs from "dayjs";

console.log("running in QuickJS (wasm)");

const d = dayjs("2024-03-14").add(1, "day");

({ tomorrow: d.format("YYYY-MM-DD"), engine: "quickjs-wasm" })
`;

// Console level -> text color. log/info fall through to the default text color.
const LEVEL_COLOR: Record<string, string | undefined> = {
  error: "var(--err-fg)",
  warn: "var(--warn)",
  debug: "var(--color-neutral-500)",
};

// prettyValue re-indents the returned JSON text; the raw string is a safe
// fallback if it somehow isn't JSON (the backend always sends valid JSON).
function prettyValue(value: string): string {
  try {
    return JSON.stringify(JSON.parse(value), null, 2);
  } catch {
    return value;
  }
}

export function ScriptsView() {
  const [source, setSource] = useState(DEFAULT_SNIPPET);
  const runScript = useRunScript();

  const run = () => {
    if (source.trim() && !runScript.isPending) runScript.mutate({ source });
  };
  // Monaco commands registered in onMount capture their closure once, so route ⌘↵
  // through a ref that always points at the latest run() (fresh source / pending
  // state) instead of the stale closure from first mount.
  const runRef = useRef(run);
  runRef.current = run;

  const onMount: OnMount = (editor, m) => {
    // ⌘↵ / Ctrl+↵ runs the script (preserving the old textarea shortcut); ⌘S /
    // Ctrl+S formats the document (matches the request Editor, plan §7).
    editor.addCommand(m.KeyMod.CtrlCmd | m.KeyCode.Enter, () => runRef.current());
    editor.addCommand(m.KeyMod.CtrlCmd | m.KeyCode.KeyS, () => {
      editor.getAction("editor.action.formatDocument")?.run();
    });
  };

  return (
    <div className="flex flex-col" style={{ flex: 1, minWidth: 0, minHeight: 0 }}>
      <div
        className="flex items-center gap-[12px]"
        style={{ flex: "none", padding: "14px 20px", borderBottom: "1px solid var(--line)" }}
      >
        <div>
          <h4 style={{ margin: 0 }}>Scripts</h4>
          <span className="text-muted" style={{ fontSize: 12 }}>
            TypeScript scratchpad for the QuickJS engine — dayjs available; no
            capabilities, no workspace inputs.
          </span>
        </div>
        <Button
          variant="primary"
          className="ml-auto"
          style={{ padding: "6px 13px", fontSize: 13, gap: 7 }}
          onClick={run}
          disabled={runScript.isPending || !source.trim()}
        >
          <Play size={14} weight="fill" />
          {runScript.isPending ? "Running…" : "Run"}
          <Kbd>⌘↵</Kbd>
        </Button>
      </div>

      <div className="flex" style={{ flex: 1, minHeight: 0 }}>
        <div
          className="flex flex-col"
          style={{ flex: 1, minWidth: 0, borderRight: "1px solid var(--line)" }}
        >
          <MonacoEditor
            path={SCRATCH_PATH}
            language="typescript"
            theme={NOCTURNE_MONACO_THEME}
            value={source}
            onChange={(v: string | undefined) => setSource(v ?? "")}
            onMount={onMount}
            options={{
              automaticLayout: true,
              minimap: { enabled: false },
              scrollBeyondLastLine: false,
              fontFamily: "var(--mono)",
              fontSize: 13,
              padding: { top: 12, bottom: 12 },
              tabSize: 2,
            }}
          />
        </div>

        <div className="flex flex-col" style={{ flex: 1, minWidth: 0, minHeight: 0 }}>
          <div style={{ flex: 1, overflow: "auto", padding: "14px 16px" }}>
            <OutputPane
              result={runScript.data}
              connectError={runScript.isError ? runScript.error : null}
              pending={runScript.isPending}
            />
          </div>
        </div>
      </div>
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
          : "Press Run (⌘↵) to evaluate the script. Its value, console output, and any error appear here."}
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
            <Muted>undefined — the script's last statement produced no value.</Muted>
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
