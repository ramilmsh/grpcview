import type { ReactNode } from "react";
import type { Message } from "@grpcview/v1/workspace_pb";
import { Dialog } from "@/components/ui/Dialog";
import { useDefinitions } from "@/lib/workspace-query";
import { messageTypeText } from "./proto-types";

export function TypesModal({
  open,
  onClose,
  input,
  output,
}: {
  open: boolean;
  onClose: () => void;
  input?: Message;
  output?: Message;
}) {
  const { files, isLoading, error } = useDefinitions();
  return (
    <Dialog open={open} onClose={onClose} title="Message types" width={640}>
      {isLoading ? (
        <Note>Generating types…</Note>
      ) : error ? (
        <Note>Could not generate types: {error.message}</Note>
      ) : !files.size ? (
        <Note>Schema unavailable — no generated definitions yet.</Note>
      ) : (
        <div className="flex flex-col" style={{ gap: 16 }}>
          <TypeSection label="Request" message={input} files={files} />
          <TypeSection label="Response" message={output} files={files} />
        </div>
      )}
    </Dialog>
  );
}

type SectionState =
  | { kind: "unavailable" }
  | { kind: "wkt"; fullName: string }
  | { kind: "text"; symbol: string; text: string; wholeFile: boolean };

function resolveSection(
  message: Message | undefined,
  files: ReadonlyMap<string, string>,
): SectionState {
  if (!message || !message.name) return { kind: "unavailable" };
  if (message.file.startsWith("google/protobuf/")) {
    return {
      kind: "wkt",
      fullName: message.package
        ? `${message.package}.${message.name}`
        : message.name,
    };
  }
  const result = messageTypeText(
    files,
    message.package,
    message.name,
    message.file,
  );
  if (!result) return { kind: "unavailable" };
  const wholeFile = !result.text.trimStart().startsWith("export type");
  return { kind: "text", symbol: result.symbol, text: result.text, wholeFile };
}

function TypeSection({
  label,
  message,
  files,
}: {
  label: string;
  message?: Message;
  files: ReadonlyMap<string, string>;
}) {
  const state = resolveSection(message, files);
  return (
    <div>
      <div
        style={{
          fontSize: 11,
          fontWeight: 600,
          color: "var(--color-neutral-400)",
          marginBottom: 6,
        }}
      >
        {label} —{" "}
        <span
          className="font-mono"
          style={{ color: "var(--color-accent-300)" }}
        >
          {message?.name || "unknown"}
        </span>
      </div>

      {state.kind === "unavailable" && (
        <Note>
          Type unavailable — this message's schema couldn't be resolved.
        </Note>
      )}

      {state.kind === "wkt" && (
        <Note>
          Well-known type <span className="font-mono">{state.fullName}</span> —
          not generated locally (common for responses, e.g.{" "}
          <span className="font-mono">google.protobuf.Empty</span>).
        </Note>
      )}

      {state.kind === "text" && (
        <>
          {state.wholeFile && (
            <div
              className="text-muted"
              style={{ fontSize: 11, marginBottom: 5 }}
            >
              Couldn't isolate a single type — showing the full generated file.
            </div>
          )}
          <pre
            className="font-mono"
            style={{
              margin: 0,
              maxHeight: 280,
              overflow: "auto",
              fontSize: 12.5,
              lineHeight: 1.5,
              color: "var(--v-text)",
              background: "var(--v-ground)",
              boxShadow: "var(--v-sunken)",
              borderRadius: "var(--v-r)",
              padding: "10px 12px",
            }}
          >
            {state.text}
          </pre>
        </>
      )}
    </div>
  );
}

function Note({ children }: { children: ReactNode }) {
  return (
    <p
      className="text-muted"
      style={{ margin: 0, fontSize: 13, lineHeight: 1.6 }}
    >
      {children}
    </p>
  );
}
