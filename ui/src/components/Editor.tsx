import React, { useEffect, useRef } from "react";
import MonacoEditor from "react-monaco-editor";
import * as monaco from "monaco-editor";
import { Service } from "@grpcview/v1/service_pb";

interface EditorProps {
  services: Service[];
  data?: string;
}

export const Editor: React.FC<EditorProps> = ({ services, data = "{}" }) => {
  const editorRef = useRef<monaco.editor.IStandaloneCodeEditor | null>(null);
  const monacoRef = useRef<typeof monaco | null>(null);

  useEffect(() => {
    if (!monacoRef.current) return;

    const baseUri = monaco.Uri.from({
      scheme: "grpcview",
      path: "schemas",
    });

    const schemas: any[] = [];
    for (const service of services) {
      for (const method of service.methods) {
        const uri = monaco.Uri.joinPath(
          baseUri,
          service.package,
          service.name,
          method.name
        ).toString();

        schemas.push({
          uri: uri,
          fileMatch: [uri], // This matches the file/model URI
          schema: method.input?.schema,
        });
      }
    }

    monacoRef.current.languages.json.jsonDefaults.setDiagnosticsOptions({
      validate: true,
      schemaValidation: "error",
      schemas: schemas,
    });
  }, [services]);

  const handleEditorDidMount = (
    editor: monaco.editor.IStandaloneCodeEditor,
    m: typeof monaco
  ) => {
    editorRef.current = editor;
    monacoRef.current = m;

    const uri = m.Uri.parse("grpcview:schemas/grpcview.v1/Workspace/Add");
    let model = m.editor.getModel(uri);
    if (!model) {
      model = m.editor.createModel(data, "json", uri);
    } else {
      model.setValue(data);
    }
    editor.setModel(model);

    // Add format action keybinding
    editor.addCommand(m.KeyMod.CtrlCmd | m.KeyCode.KeyS, () => {
      editor.getAction("editor.action.formatDocument")?.run();
    });
  };

  return (
    <div className="h-full w-full">
      <MonacoEditor
        width="100%"
        height="100%"
        language="json"
        theme="vs-light"
        options={{
          formatOnType: true,
          formatOnPaste: true,
          autoIndent: "full",
          quickSuggestions: true,
          minimap: { enabled: false },
        }}
        editorDidMount={handleEditorDidMount}
      />
    </div>
  );
};
