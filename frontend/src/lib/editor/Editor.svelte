<script lang="ts">
  import { onMount } from "svelte";
  import * as monaco from "monaco-editor";
  import type { Service } from "@grpcview/v1/service_pb";

  const baseUri = monaco.Uri.from({
    scheme: "grpcview",
    path: "schemas",
  });

  const {
    services,
    data,
  }: {
    services: Service[];
    data: string;
  } = $props();

  let schemas = [];
  for (const service of services) {
    for (const method of service.methods) {
      const uri = monaco.Uri.joinPath(
        baseUri,
        service.package,
        service.name,
        method.name
      ).toString();
      console.log(uri);
      schemas.push({
        uri: uri,
        fileMatch: [uri],
        schema: method.input?.schema,
      });
    }
  }

  monaco.languages.json.jsonDefaults.setDiagnosticsOptions({
    validate: true,
    schemaValidation: "error",
    schemas: schemas,
  });

  onMount(() => {
    var model = monaco.editor.createModel(
      data,
      "json",
      monaco.Uri.parse("grpcview:schemas/grpcview.v1/Workspace/Add")
    );

    const editor = monaco.editor.create(document.getElementById("container")!, {
      model: model,
      formatOnType: true,
      formatOnPaste: true,
      autoIndent: "full",
      quickSuggestions: true,
      minimap: { enabled: false },
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

<div id="container"></div>

<style>
  #container {
    height: 100%;
    width: 100%;
  }
</style>
