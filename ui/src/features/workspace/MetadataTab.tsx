import { useState } from "react";
import { CheckCircle, Warning } from "@/components/ui/icons";
import { MetadataEditor } from "./MetadataEditor";
import type { GeneratorDef } from "./generator-libs";

// MetadataTab is the request-metadata editor (Monaco) plus a footer that reports the module's
// validity from Monaco's TS markers. Metadata is authored as TypeScript — a single hidden-wrapper
// module whose returned `{ [key: string]: string[] }` object becomes the outgoing gRPC metadata
// (multi-valued) — replacing the old key/value grid and the metadata `{{ }}` tokens (chips,
// enable toggles, add/remove, and the binding editor are all gone). Mirrors MessageTab.
export function MetadataTab({
  metadata,
  onChange,
  currentKey,
  generators,
}: {
  metadata: string;
  onChange: (value: string) => void;
  currentKey: string;
  // Workspace generators (name + source), forwarded to MetadataEditor for ambient autocomplete
  // with inferred signatures (§P5).
  generators: GeneratorDef[];
}) {
  const [errors, setErrors] = useState(0);

  return (
    <div className="flex flex-col" style={{ flex: 1, minHeight: 0 }}>
      <div style={{ flex: 1, minHeight: 0 }}>
        <MetadataEditor
          data={metadata}
          onChange={onChange}
          currentKey={currentKey}
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
            <CheckCircle style={{ color: "var(--ok)" }} /> valid Metadata
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
