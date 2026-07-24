import { useState } from "react";
import { CheckCircle, Warning } from "@/components/ui/icons";
import { Editor } from "./Editor";
import type { GeneratorDef } from "./generator-libs";

// MessageTab is the request body editor (Monaco) plus a footer that reports the body's
// validity from Monaco's TS markers. The body is always authored as TypeScript (a generator
// whose returned object becomes the message) — there is no JSON authoring mode. Token chips /
// the JSON⇄TS toggle were removed with the all-JS phase; metadata `{{ }}` tokens are unaffected
// (a separate surface).
export function MessageTab({
  body,
  onChange,
  currentKey,
  inputTypeName,
  descriptorSet,
  inputPackage,
  inputFile,
  generators,
}: {
  body: string;
  onChange: (value: string) => void;
  currentKey: string;
  inputTypeName?: string;
  // T2 typed-body inputs, forwarded to Editor.
  descriptorSet?: Uint8Array;
  inputPackage?: string;
  inputFile?: string;
  // Composition (T3 + §P5): workspace generators (name + source), forwarded to Editor for ambient
  // autocomplete with inferred signatures.
  generators: GeneratorDef[];
}) {
  const [errors, setErrors] = useState(0);

  return (
    <div className="flex flex-col" style={{ flex: 1, minHeight: 0 }}>
      <div style={{ flex: 1, minHeight: 0 }}>
        <Editor
          data={body}
          onChange={onChange}
          currentKey={currentKey}
          descriptorSet={descriptorSet}
          inputPackage={inputPackage}
          inputName={inputTypeName}
          inputFile={inputFile}
          generators={generators}
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
      </div>
    </div>
  );
}
