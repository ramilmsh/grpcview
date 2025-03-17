<script lang="ts">
  import { Workspace } from "@grpcview/v1/service_pb";
  import { createClient } from "@connectrpc/connect";
  import { createConnectTransport } from "@connectrpc/connect-web";
  import Editor from "@frontend/components/editor/Editor.svelte";
  import Navbar from "@frontend/components/navbar/Navbar.svelte";

  const client = createClient(
    Workspace,
    createConnectTransport({ baseUrl: "http://127.0.0.1:54321" })
  );
  client
    .add({
      source: {
        case: "descriptorSet",
        value: {},
      },
    })
    .then((response) => {
      console.log(response);
      for (const fileDescriptor of response.descriptorSet?.file || []) {
        fileDescriptor.package;
      }
    });
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
