<script setup lang="ts">
import { pinia } from "@/stores";
import { createClient } from "@/client";
import Editor from "@/components/Editor.vue";
import TreeView from "@/components/TreeView.vue";
import type { Service } from "@grpcview/v1/service_pb";
import { ref } from "vue";
import { useWorkspaceStore, type Item } from "@/stores/workspace";

const store = useWorkspaceStore(pinia);

const services = ref<Service[]>([]);

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
    services.value = response.services;
  });
</script>

<template>
  <div class="container">
    <TreeView
      class="tree-view"
      :item="store.rootItem"
      @add-item="(parent: Item, item: Item) => store.addItem(parent, item)"
    ></TreeView>
    <Editor
      class="editor"
      data="{}"
      v-if="services.length > 0"
      :services="services"
    ></Editor>
  </div>
</template>

<style lang="css" scoped>
.container {
  display: flex;
  width: 100%;
  height: 100%;
}
.tree-view {
  width: 20%;
  height: 100%;
}
.editor {
  width: 80%;
  height: 100%;
}
</style>
