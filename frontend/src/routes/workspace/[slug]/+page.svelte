<script lang="ts">
  import { createClient } from "$lib/client";
  import Editor from "@frontend/lib/editor/Editor.svelte";

  const client = createClient();

  let addPromise = client.add({
    source: {
      case: "reflection",
      value: {
        host: "127.0.0.1",
        port: 10000,
      },
    },
  });
</script>

<div id="page" class="container">
  {#await addPromise then response}
    <Editor services={response.services} data={"{}"}></Editor>
  {/await}
</div>

<style>
  #page {
    height: 100%;
    width: 100%;
  }
</style>
