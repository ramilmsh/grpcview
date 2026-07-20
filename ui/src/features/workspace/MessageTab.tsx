import { useState } from "react";
import { CheckCircle, Warning } from "@/components/ui/icons";
import { Editor } from "./Editor";

// MessageTab is the request body editor (Monaco) plus a footer that reports the
// schema-validity from Monaco's markers (plan §1.4).
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
        <span className="ml-auto">JSON · UTF-8</span>
      </div>
    </div>
  );
}
