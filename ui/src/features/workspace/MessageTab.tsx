import { useMemo, useState } from "react";
import { CheckCircle, Warning } from "@/components/ui/icons";
import { useUIStore } from "@/lib/ui-store";
import { Editor } from "./Editor";
import { scanTokens } from "./tokens";

// MessageTab is the request body editor (Monaco) plus a footer that reports the
// schema-validity from Monaco's markers and, when the body carries `{{ … }}`
// generator tokens, how many resolve on invoke (plan §S2). Clicking a token opens
// the binding editor for the generator it names.
export function MessageTab({
  schema,
  body,
  onChange,
  currentMethod,
  currentKey,
  inputTypeName,
}: {
  schema?: object;
  body: string;
  onChange: (value: string) => void;
  currentMethod: { service: string; method: string };
  currentKey: string;
  inputTypeName?: string;
}) {
  const [errors, setErrors] = useState(0);
  const openBinding = useUIStore((s) => s.openBinding);
  const tokenCount = useMemo(() => scanTokens(body).length, [body]);

  return (
    <div className="flex flex-col" style={{ flex: 1, minHeight: 0 }}>
      <div style={{ flex: 1, minHeight: 0 }}>
        <Editor
          schema={schema}
          data={body}
          onChange={onChange}
          currentMethod={currentMethod}
          currentKey={currentKey}
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
        {tokenCount > 0 && (
          <span style={{ marginLeft: "auto", color: "var(--color-accent-2-300)" }}>
            {tokenCount} {tokenCount === 1 ? "token" : "tokens"} resolve
          </span>
        )}
        <span className={tokenCount > 0 ? undefined : "ml-auto"}>JSON · UTF-8</span>
      </div>
    </div>
  );
}
