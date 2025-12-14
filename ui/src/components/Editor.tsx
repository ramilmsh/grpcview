import React, { useEffect, useRef, useState } from "react";
import {
  Editor as MonacoEditor,
  useMonaco,
  OnMount,
} from "@monaco-editor/react";
import * as monaco from "monaco-editor";
import { Service } from "@grpcview/v1/workspace_pb";

interface EditorProps {
  services: Service[];
  data: string;
  onChange: (value: string) => void;
  currentMethod?: { service: string; method: string };
  theme?: string;
}

const _DEFAULT_BASE_URI_COMPONENTS = {
  scheme: "grpcview",
  path: "schemas",
};

export const Editor: React.FC<EditorProps> = ({
  services,
  data,
  onChange,
  currentMethod,
  theme = "vs-dark",
}) => {
  const editorRef = useRef<monaco.editor.IStandaloneCodeEditor | null>(null);
  const monacoInstance = useMonaco();
  const [isEditorReady, setIsEditorReady] = useState(false);

  // Register schemas
  useEffect(() => {
    if (!monacoInstance) return;

    const baseUri = monacoInstance.Uri.from(_DEFAULT_BASE_URI_COMPONENTS);

    const schemas: any[] = [];
    for (const service of services) {
      for (const method of service.methods) {
        // Construct schema URI
        const uri = monacoInstance.Uri.joinPath(
          baseUri,
          service.package + "." + service.name,
          method.name
        ).toString();

        if (method.input?.schema) {
          schemas.push({
            uri: uri,
            fileMatch: [uri],
            schema: method.input.schema,
          });
        }
      }
    }

    monacoInstance.languages.json.jsonDefaults.setDiagnosticsOptions({
      validate: true,
      schemaValidation: "error",
      schemas: schemas,
      enableSchemaRequest: true,
    });
  }, [monacoInstance, services]);

  // Update Model URI based on selection to trigger correct schema validation
  useEffect(() => {
    if (!monacoInstance || !isEditorReady || !editorRef.current) return;
    if (!currentMethod) return; // Guard against empty values
    const baseUri = monacoInstance.Uri.from(_DEFAULT_BASE_URI_COMPONENTS);

    const modalUri = monacoInstance.Uri.joinPath(
      baseUri,
      currentMethod.service,
      currentMethod.method
    );

    let model = monacoInstance.editor.getModel(modalUri);

    const currentModel = editorRef.current.getModel();
    const needsModelSwitch = currentModel !== model;

    if (!model) {
      // Create new model with current data
      model = monacoInstance.editor.createModel(data, "json", modalUri);
    } else if (needsModelSwitch) {
      // Switching to existing model - update its content to current data
      model.setValue(data);
    }
    // If same model and no switch, don't touch content - let Monaco manage it

    if (needsModelSwitch) {
      editorRef.current.setModel(model);
    }
  }, [monacoInstance, currentMethod, isEditorReady]); // Re-run when editor becomes ready

  const handleEditorDidMount: OnMount = (editor, _monaco) => {
    editorRef.current = editor;
    setIsEditorReady(true);

    // Add format action keybinding
    editor.addCommand(_monaco.KeyMod.CtrlCmd | _monaco.KeyCode.KeyS, () => {
      editor.getAction("editor.action.formatDocument")?.run();
    });
  };

  const handleChange = (value: string | undefined) => {
    if (value !== undefined) {
      onChange(value);
    }
  };

  return (
    <div className="h-full w-full">
      <MonacoEditor
        width="100%"
        height="100%"
        language="json"
        theme={theme}
        defaultValue={data}
        options={{
          formatOnType: true,
          formatOnPaste: true,
          automaticLayout: true,
          minimap: { enabled: false },
          scrollBeyondLastLine: false,
        }}
        onMount={handleEditorDidMount}
        onChange={handleChange}
      />
    </div>
  );
};
