import { Editor as MonacoEditor } from "@monaco-editor/react";
import { NOCTURNE_MONACO_THEME } from "@/theme/monaco-nocturne";

// Read-only JSON view for response bodies. Shares the bundled Monaco + Nocturne
// theme with the request editor (plan §7).
export function JsonViewer({ value }: { value: string }) {
  return (
    <MonacoEditor
      language="json"
      theme={NOCTURNE_MONACO_THEME}
      value={value}
      options={{
        readOnly: true,
        domReadOnly: true,
        automaticLayout: true,
        minimap: { enabled: false },
        scrollBeyondLastLine: false,
        wordWrap: "on",
        fontFamily: "var(--mono)",
        fontSize: 13,
        padding: { top: 10, bottom: 10 },
      }}
    />
  );
}
