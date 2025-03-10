<script lang="ts">
  import * as monaco from "monaco-editor";
  import { onMount } from "svelte";
  import schema from "./schema.json";

  const modelUri = monaco.Uri.parse("file://b/foo.json");

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

  onMount(() => {
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
  });
</script>

<div id="container" style="height: 100%"></div>
