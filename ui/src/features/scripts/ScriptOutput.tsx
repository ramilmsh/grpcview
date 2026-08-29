import type { ReactNode } from "react";
import { CaretDown, CaretRight, Warning } from "@/components/ui/icons";
import type { RunScriptResponse } from "@grpcview/v1/service_pb";

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

export function OutputRegion({
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
    summary =
      (result.value !== undefined ? "value returned" : "no value") + logs;
    summaryColor =
      result.value !== undefined ? "var(--ok)" : "var(--color-neutral-500)";
  }

  return (
    <div
      className="flex flex-col"
      style={{
        flex: "none",
        borderTop: "1px solid var(--line)",
        minHeight: 0,
        ...(open ? { height: 260 } : {}),
      }}
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
        <span
          className="font-mono ml-auto"
          style={{ fontSize: 11, color: summaryColor }}
        >
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
          <OutputPane
            result={result}
            connectError={connectError}
            pending={pending}
          />
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
              style={{
                margin: "6px 0 0",
                fontSize: 12,
                whiteSpace: "pre-wrap",
              }}
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
              style={{
                margin: 0,
                fontSize: 13,
                whiteSpace: "pre-wrap",
                lineHeight: 1.55,
              }}
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
          <div
            className="flex flex-col font-mono"
            style={{ gap: 3, fontSize: 12.5 }}
          >
            {logs.map((line, i) => (
              <div
                key={i}
                style={{ display: "flex", gap: 8, whiteSpace: "pre-wrap" }}
              >
                <span
                  style={{
                    color: "var(--color-neutral-600)",
                    flex: "none",
                    width: 42,
                  }}
                >
                  {line.level}
                </span>
                <span style={{ color: LEVEL_COLOR[line.level] }}>
                  {line.message}
                </span>
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

function ErrorBox({
  title,
  children,
}: {
  title: string;
  children?: ReactNode;
}) {
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
  return (
    <div style={{ fontSize: 12, opacity: 0.85, marginTop: 4 }}>{children}</div>
  );
}
