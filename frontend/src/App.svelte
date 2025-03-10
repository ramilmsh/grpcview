<script lang="ts">
  import { type Hello, HelloSchema } from "@grpcview/v1/service_pb";
  import Editor from "@frontend/components/editor/Editor.svelte";
  import Navbar from "@frontend/components/navbar/Navbar.svelte";

  console.log(HelloSchema.fields);
  const m: Hello = { $typeName: "grpcview.v1.Hello", a: "" };
  console.log(m);
</script>

<main style="height: 100vh;">
  <Navbar />
  <div class="row" style="height: 100vh;">
    <div class="col" style="height: 100vh;">Column</div>
    <div class="col">
      <Editor />
    </div>
    <div class="col"></div>
  </div>
</main>
