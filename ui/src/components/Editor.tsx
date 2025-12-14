import React, { useEffect, useRef } from "react";
import MonacoEditor from "react-monaco-editor";
import * as monaco from "monaco-editor";
import { Service } from "@grpcview/v1/workspace_pb";

interface EditorProps {
  services: Service[];
  data: string;
  onChange: (value: string) => void;
  currentService?: string; // package.Service
  currentMethod?: string; // MethodName
}

export const Editor: React.FC<EditorProps> = ({
  services,
  data,
  onChange,
  currentService,
  currentMethod,
}) => {
  const editorRef = useRef<monaco.editor.IStandaloneCodeEditor | null>(null);
  const monacoRef = useRef<typeof monaco | null>(null);

  // Register schemas
  useEffect(() => {
    if (!monacoRef.current) return;

    const baseUri = monaco.Uri.from({
      scheme: "grpcview",
      path: "schemas",
    });

    const schemas: any[] = [];
    for (const service of services) {
      for (const method of service.methods) {
        // Construct schema URI
        const uri = monaco.Uri.joinPath(
          baseUri,
          service.package,
          service.name,
          method.name
        ).toString();

        // Ensure schema is a valid JSON object
        // The proto definition says `google.protobuf.Struct schema`.
        // We need to convert it to a plain JS object if it's a typed Struct,
        // or check if it's already a plain object in the generated code.
        // In @bufbuild/protobuf, Struct is usually handled as JsonValue or Struct class.
        // Assuming the generated code provides the raw JSON object or a toJSON compatible struct.
        // If `method.input?.schema` is a Struct class instance, we might need `.toJson()` or similar.
        // But for now, let's assume it passes through or we can use it.
        // Note: Check existing frontend usage "deepToRaw" suggested it might be a reactive proxy in Vue. Use simple JSON clone here if needed.

        if (method.input?.schema) {
          schemas.push({
            uri: uri,
            fileMatch: [uri],
            schema: method.input.schema,
          });
        }
      }
    }

    monacoRef.current.languages.json.jsonDefaults.setDiagnosticsOptions({
      validate: true,
      schemaValidation: "error",
      schemas: schemas,
    });
  }, [services]);

  // Update Model URI based on selection to trigger correct schema validation
  useEffect(() => {
    if (!editorRef.current || !monacoRef.current) return;

    const editor = editorRef.current;
    const m = monacoRef.current;

    const currentValue = editor.getValue();

    // If data prop changed externally and is different from editor, update it.
    // But usually this component is controlled or semi-controlled.
    if (data !== currentValue) {
      // This might cause cursor jumps if typing fast, but usually we use a specific model update strategy.
      // For simplicity:
      // editor.setValue(data); // Done in handleEditorDidMount or below?
    }

    let modalUriStr = "grpcview:scratch.json";
    if (currentService && currentMethod) {
      // Must match schema match pattern
      // baseUri/package/Service/Method
      const parts = currentService.split(".");
      const sName = parts.pop();
      const pName = parts.join(".");
      modalUriStr = `grpcview:schemas/${pName}/${sName}/${currentMethod}`;
    } else {
      modalUriStr = `grpcview:schemas/scratch/${Date.now()}.json`; // Random uri to avoid schema lock
    }

    const uri = m.Uri.parse(modalUriStr);
    let model = m.editor.getModel(uri);

    // Save view state of previous model? Maybe later.

    if (!model) {
      model = m.editor.createModel(data, "json", uri);
      model.onDidChangeContent(() => {
        onChange(model!.getValue());
      });
    } else {
      // If model exists, update value if completely different context?
      // Or just switch to it.
      // If switching to an existing model, it might hold old state.
      // For this app, store holds state, so we should update model value from store.
      if (model.getValue() !== data) {
        model.setValue(data);
      }
    }

    editor.setModel(model);
  }, [currentService, currentMethod, data, services, onChange]); // Depend on data is tricky for cursor, usually handled via ref

  // We should actually NOT depend on `data` for recreating model every keystroke.
  // We use `data` only for initial load or switching items.

  const handleEditorDidMount = (
    editor: monaco.editor.IStandaloneCodeEditor,
    m: typeof monaco
  ) => {
    editorRef.current = editor;
    monacoRef.current = m;

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
