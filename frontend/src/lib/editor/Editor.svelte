<script lang="ts">
  import { onMount } from "svelte";
  import * as monaco from "monaco-editor";
  import { createClient, type AddRequest } from "$lib/client";

  const baseUri = monaco.Uri.from({
    scheme: "grpcview",
    path: "schemas",
  });

  const client = createClient();

  client
    .add({
      source: {
        case: "reflection",
        value: {
          host: "127.0.0.1",
          port: 10000,
        },
      },
    })
    .then((response) => {
      console.log(response.services[0].methods[0].input?.schema);

      let schemas = [];
      for (const service of response.services) {
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
    });

  let { data }: { data: string } = $props();

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
