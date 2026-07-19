import React from "react";
import { Editor as MonacoEditor } from "@monaco-editor/react";

interface JsonViewerProps {
  value: string;
  theme?: string;
}

// JsonViewer renders read-only JSON with Monaco's syntax highlighting. Used for
// response bodies, which are display-only — it shares Monaco (and its JSON
// tokenizer) with the request Editor so both panels look the same.
export const JsonViewer: React.FC<JsonViewerProps> = ({
  value,
  theme = "vs-dark",
}) => {
  return (
    <div className="h-full w-full">
      <MonacoEditor
        width="100%"
        height="100%"
        language="json"
        theme={theme}
        value={value}
        options={{
          readOnly: true,
          domReadOnly: true,
          automaticLayout: true,
          minimap: { enabled: false },
          scrollBeyondLastLine: false,
          wordWrap: "on",
        }}
      />
    </div>
  );
};
