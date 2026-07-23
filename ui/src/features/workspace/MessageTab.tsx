import { useEffect, useMemo, useState } from "react";
import { BodyLanguage } from "@grpcview/v1/workspace_pb";
import { CheckCircle, Warning } from "@/components/ui/icons";
import { useUIStore } from "@/lib/ui-store";
import { Editor } from "./Editor";
import { scanTokens } from "./tokens";

// MessageTab is the request body editor (Monaco) plus a footer that reports the
// schema-validity from Monaco's markers and, when the body carries `{{ … }}`
// generator tokens, how many resolve on invoke (plan §S2). Clicking a token opens
// the binding editor for the generator it names. The footer also carries the
// JSON ⇄ TypeScript body-language toggle (ts-request-body-plan §T1): switching to
// TypeScript makes the body a generator whose returned object becomes the message.
export function MessageTab({
  schema,
  body,
  onChange,
  currentMethod,
  currentKey,
  inputTypeName,
  bodyLanguage,
  onBodyLanguageChange,
}: {
  schema?: object;
  body: string;
  onChange: (value: string) => void;
  currentMethod: { service: string; method: string };
  currentKey: string;
  inputTypeName?: string;
  bodyLanguage: BodyLanguage;
  onBodyLanguageChange: (next: BodyLanguage) => void;
}) {
  const [errors, setErrors] = useState(0);
  const openBinding = useUIStore((s) => s.openBinding);
  const tokenCount = useMemo(() => scanTokens(body).length, [body]);
  const isTS = bodyLanguage === BodyLanguage.TYPESCRIPT;
  // Token chips are a JSON-mode concern only — a TS body calls its generators
  // directly, so the backend never runs the `{{ }}` resolver over it. Hide the footer
  // chip in TS mode (the editor gates the token decorations off there too).
  const showTokens = !isTS && tokenCount > 0;
  // When the mode flips (JSON⇄TS) the editor remounts with a fresh model at the other
  // URI. A buffer that validates clean fires no marker event, so the footer would
  // otherwise strand the previous mode's error count. Reset on the flip; an erroring
  // new buffer repopulates it via onDidChangeMarkers.
  useEffect(() => {
    setErrors(0);
  }, [isTS]);
  // The effective language (UNSPECIFIED reads as JSON), used so clicking the already-
  // active side is a no-op rather than a redundant persist.
  const effective = isTS ? BodyLanguage.TYPESCRIPT : BodyLanguage.JSON;
  const select = (next: BodyLanguage) => {
    if (next !== effective) onBodyLanguageChange(next);
  };

  return (
    <div className="flex flex-col" style={{ flex: 1, minHeight: 0 }}>
      <div style={{ flex: 1, minHeight: 0 }}>
        <Editor
          schema={schema}
          data={body}
          onChange={onChange}
          currentMethod={currentMethod}
          currentKey={currentKey}
          bodyLanguage={bodyLanguage}
          onErrorsChange={setErrors}
          onTokenClick={openBinding}
        />
      </div>
      <div
        className="flex items-center gap-[12px] font-mono"
        style={{
          flex: "none",
          padding: "6px 12px",
          borderTop: "1px solid var(--line)",
          fontSize: 11,
          color: "var(--color-neutral-600)",
        }}
      >
        {errors === 0 ? (
          <span className="inline-flex items-center gap-[5px]">
            <CheckCircle style={{ color: "var(--ok)" }} /> valid{" "}
            {inputTypeName ?? "message"}
          </span>
        ) : (
          <span className="inline-flex items-center gap-[5px]" style={{ color: "var(--warn)" }}>
            <Warning weight="fill" /> {errors} {errors === 1 ? "error" : "errors"}
          </span>
        )}
        {showTokens && (
          <span style={{ marginLeft: "auto", color: "var(--color-accent-2-300)" }}>
            {tokenCount} {tokenCount === 1 ? "token" : "tokens"} resolve
          </span>
        )}
        <div
          className={`inline-flex items-center gap-[6px] ${showTokens ? "" : "ml-auto"}`}
          role="group"
          aria-label="Body language"
        >
          <button
            type="button"
            onClick={() => select(BodyLanguage.JSON)}
            title="Interpret the body as JSON — sent as-is (today's path + {{ }} tokens)"
            style={{
              background: "transparent",
              border: "none",
              padding: 0,
              font: "inherit",
              cursor: "pointer",
              color: isTS ? "var(--color-neutral-600)" : "var(--color-neutral-300)",
              fontWeight: isTS ? 400 : 600,
            }}
          >
            JSON
          </button>
          <span aria-hidden style={{ color: "var(--color-neutral-700)" }}>
            ⇄
          </span>
          <button
            type="button"
            onClick={() => select(BodyLanguage.TYPESCRIPT)}
            title="Interpret the body as TypeScript — its returned object becomes the message"
            style={{
              background: "transparent",
              border: "none",
              padding: 0,
              font: "inherit",
              cursor: "pointer",
              color: isTS ? "var(--color-accent-2-300)" : "var(--color-neutral-600)",
              fontWeight: isTS ? 600 : 400,
            }}
          >
            TypeScript
          </button>
        </div>
      </div>
    </div>
  );
}
