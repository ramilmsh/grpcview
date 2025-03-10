<script lang="ts">
  import { type Hello, HelloSchema } from "@grpcview/v1/service_pb";
  import Editor from "@frontend/components/editor/Editor.svelte";

  console.log(HelloSchema.fields);
  const m: Hello = { $typeName: "grpcview.v1.Hello", a: "" };
  console.log(m);
</script>

<main style="height: 100vh;">
  <Editor />
</main>
