import { Editor as MonacoEditor } from "@monaco-editor/react";
import { MONACO_THEME } from "@/theme/monaco-theme";

export function JsonViewer({ value }: { value: string }) {
  return (
    <MonacoEditor
      language="json"
      theme={MONACO_THEME}
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
