import { useEffect, useState, type ReactNode } from "react";
import type { Message } from "@grpcview/v1/workspace_pb";
import { Dialog } from "@/components/ui/Dialog";

// Type-only reference to proto-types.ts's exports. proto-types.ts statically imports
// @bufbuild/protoc-gen-es (a heavy, otherwise-lazy dependency — see Editor.tsx's own T2 effect),
// so this module must never import it eagerly; `typeof import(...)` is erased at compile time
// and costs nothing at runtime, letting us type the dynamically-imported members below.
type ProtoTypes = typeof import("./proto-types");

// TypesModal (message-shape-visibility plan §Feature 2/Phase 2) is a read-only viewer for the
// concrete generated `<Message>Json` protojson TypeScript shape of the active method's request
// (input) and response (output) messages — exactly what the body is authored as (Editor.tsx/T2)
// and the response is decoded as. It dynamically imports the SAME client-side protoc-gen-es
// generator the body editor's typing uses (proto-types.ts), memoized by descriptorSet identity —
// in practice the body editor has almost always already warmed that cache by the time this
// opens, so the "Generating types…" state below is mostly a first-open safety net, not the
// common case. Fully decoupled from the `gv` scripting work: no backend/proto/store changes.
export function TypesModal({
  open,
  onClose,
  descriptorSet,
  input,
  output,
}: {
  open: boolean;
  onClose: () => void;
  // The workspace-global merged FileDescriptorSet (the same prop Editor.tsx types the body
  // against); absent/empty when no reflection source has resolved yet.
  descriptorSet?: Uint8Array;
  // The active method's input/output coordinates (Method.input / Method.output).
  input?: Message;
  output?: Message;
}) {
  const [loading, setLoading] = useState(false);
  const [gen, setGen] = useState<{
    files: Map<string, string>;
    messageTypeText: ProtoTypes["messageTypeText"];
  } | null>(null);

  // (Re)generate only while open, so closed tabs never pay for a stale descriptorSet. Re-runs
  // whenever `open` flips true OR the descriptorSet reference changes (a fresh reflect/refresh) —
  // `generateWorkspaceTypes`'s own WeakMap memo makes every call beyond the very first, for a
  // given descriptorSet, effectively instant.
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

// A per-message-slot render state: no message resolved at all, a well-known type (excluded from
// local generation — see proto-types.ts), or generated text (the sliced single block, or —
// rarely — the whole-file fallback when a balanced block can't be found).
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
  // A successful slice always starts with the declaration itself; the whole-file fallback
  // starts with the generated preamble comment / imports instead — a cheap, reliable tell.
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
