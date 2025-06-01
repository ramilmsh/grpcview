<script lang="ts" setup>
import * as monaco from "monaco-editor";
import type { Service } from "@grpcview/v1/service_pb";
import { onMounted, useTemplateRef } from "vue";
import { deepToRaw } from "@/util";

let container = useTemplateRef("container");

const baseUri = monaco.Uri.from({
  scheme: "grpcview",
  path: "schemas",
});

const { services, data } = defineProps<{
  data: string;
  services: Service[];
}>();

let schemas = [];
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
      fileMatch: [uri],
      schema: method.input?.schema,
    });
  }
}

monaco.languages.json.jsonDefaults.setDiagnosticsOptions({
  validate: true,
  schemaValidation: "error",
  schemas: deepToRaw(schemas),
});

onMounted(() => {
  monaco.editor.setModelLanguage;
  const uri = monaco.Uri.parse("grpcview:schemas/grpcview.v1/Workspace/Add");
  let model = monaco.editor.getModel(uri);
  if (!model) {
    model = monaco.editor.createModel(data, "json", uri);
  } else {
    model.setValue(data);
  }

  const editor = monaco.editor.create(container.value!, {
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
      console.info(e.key);
      if (e.key === "s" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        editor.getAction("editor.action.formatDocument")?.run();
      }
    },
    false
  );
});
</script>

<template><div ref="container"></div></template>
