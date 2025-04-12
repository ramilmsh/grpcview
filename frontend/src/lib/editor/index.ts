import * as monaco from "monaco-editor";
import schema from "./schema.json";

const modelUri = monaco.Uri.parse("file://b/foo.json");

export class Editor {
  constructor() {
    monaco.languages.json.jsonDefaults.setDiagnosticsOptions({
      validate: true,
      schemaValidation: "error",
      schemas: [
        {
          uri: "proto3_unittest.TestHasbits",
          fileMatch: [modelUri.toString()],
          schema: schema,
        },
      ],
    });

    var model = monaco.editor.createModel("{}", "json", modelUri);

    const editor = monaco.editor.create(document.getElementById("container")!, {
      model: model,
      formatOnType: true,
      formatOnPaste: true,
      autoIndent: "full",
    });

    document.addEventListener(
      "keydown",
      (e) => {
        if (e.key === "s" && (e.metaKey || e.ctrlKey)) {
          editor.getAction("editor.action.formatDocument")?.run();
        }
      },
      false
    );
  }
}
