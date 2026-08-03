import { useEffect, useState, type ReactNode } from "react";
import type { Message } from "@grpcview/v1/workspace_pb";
import { Dialog } from "@/components/ui/Dialog";

// Type-only: proto-types.ts pulls in the heavy protoc-gen-es, so it must stay a lazy import.
type ProtoTypes = typeof import("./proto-types");

export function TypesModal({
  open,
  onClose,
  descriptorSet,
  input,
  output,
}: {
  open: boolean;
  onClose: () => void;
  descriptorSet?: Uint8Array;
  input?: Message;
  output?: Message;
}) {
  const [loading, setLoading] = useState(false);
  const [gen, setGen] = useState<{
    files: Map<string, string>;
    messageTypeText: ProtoTypes["messageTypeText"];
  } | null>(null);

  useEffect(() => {
    if (!open || !descriptorSet?.length) return;
    let cancelled = false;
    setLoading(true);
    void (async () => {
      const { generateWorkspaceTypes, messageTypeText } = await import("./proto-types");
      if (cancelled) return;
      setGen({ files: generateWorkspaceTypes(descriptorSet), messageTypeText });
      setLoading(false);
    })();
    return () => {
      cancelled = true;
    };
  }, [open, descriptorSet]);

  return (
    <Dialog open={open} onClose={onClose} title="Message types" width={640}>
      {!descriptorSet?.length ? (
        <Note>Schema unavailable — no reflected descriptor set yet.</Note>
      ) : loading || !gen ? (
        <Note>Generating types…</Note>
      ) : (
        <div className="flex flex-col" style={{ gap: 16 }}>
          <TypeSection label="Request" message={input} gen={gen} />
          <TypeSection label="Response" message={output} gen={gen} />
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
  gen: { files: Map<string, string>; messageTypeText: ProtoTypes["messageTypeText"] }
): SectionState {
  if (!message || !message.name) return { kind: "unavailable" };
  if (message.file.startsWith("google/protobuf/")) {
    return {
      kind: "wkt",
      fullName: message.package ? `${message.package}.${message.name}` : message.name,
    };
  }
  const result = gen.messageTypeText(gen.files, message.package, message.name, message.file);
  if (!result) return { kind: "unavailable" };
  const wholeFile = !result.text.trimStart().startsWith("export type");
  return { kind: "text", symbol: result.symbol, text: result.text, wholeFile };
}

function TypeSection({
  label,
  message,
  gen,
}: {
  label: string;
  message?: Message;
  gen: { files: Map<string, string>; messageTypeText: ProtoTypes["messageTypeText"] };
}) {
  const state = resolveSection(message, gen);
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
        <span className="font-mono" style={{ color: "var(--color-accent-300)" }}>
          {message?.name || "unknown"}
        </span>
      </div>

      {state.kind === "unavailable" && (
        <Note>Type unavailable — this message's schema couldn't be resolved.</Note>
      )}

      {state.kind === "wkt" && (
        <Note>
          Well-known type <span className="font-mono">{state.fullName}</span> — not generated
          locally (common for responses, e.g.{" "}
          <span className="font-mono">google.protobuf.Empty</span>).
        </Note>
      )}

      {state.kind === "text" && (
        <>
          {state.wholeFile && (
            <div className="text-muted" style={{ fontSize: 11, marginBottom: 5 }}>
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
              color: "var(--color-neutral-200)",
              background: "var(--panel-2)",
              border: "1px solid var(--line)",
              borderRadius: 8,
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
    <p className="text-muted" style={{ margin: 0, fontSize: 13, lineHeight: 1.6 }}>
      {children}
    </p>
  );
}
