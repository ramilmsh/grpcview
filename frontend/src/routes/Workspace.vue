<script setup lang="ts">
import { pinia } from "@/stores";
import { createClient } from "@/client";
import Editor from "@/components/Editor.vue";
import TreeView from "@/components/TreeView.vue";
import AddSourceModal from "@/components/AddSourceModal.vue";
import type { Service } from "@grpcview/v1/service_pb";
import { ref } from "vue";
import { useWorkspaceStore, type Item } from "@/stores/workspace";

const store = useWorkspaceStore(pinia);

const services = ref<Service[]>([]);
const showAddSourceModal = ref(false);

const client = createClient();

const loadServices = async () => {
  // Ideally we would fetch existing sources/services here if persisted.
  // For now, we rely on local state or re-adding.
  // Actually, services ref is just local display.
};

const onAddReflection = (host: string, port: number) => {
  client
    .add({
      source: {
        case: "reflection",
        value: {
          host: host,
          port: port,
        },
      },
    })
    .then((response) => {
      services.value = [...services.value, ...response.services];
    })
    .catch((err) => {
      console.error("Failed to add reflection source", err);
      alert("Failed to add reflection source: " + err.message);
    });
};

const onAddDescriptor = async (file: File) => {
  try {
    const buffer = await file.arrayBuffer();
    const bytes = new Uint8Array(buffer);

    client
      .add({
        source: {
          case: "descriptorSet",
          value: bytes,
        },
      })
      .then((response) => {
        services.value = [...services.value, ...response.services];
      })
      .catch((err) => {
        console.error("Failed to add descriptor source", err);
        alert("Failed to add descriptor source: " + err.message);
      });
  } catch (err) {
    console.error("Failed to process descriptor file", err);
    alert("Failed to process file: " + err);
  }
};

const downloadWorkspace = () => {
  const data = JSON.stringify(store.rootItem, null, 2);
  const blob = new Blob([data], { type: "application/json" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = "workspace.json";
  a.click();
  URL.revokeObjectURL(url);
};

const uploadWorkspace = (event: Event) => {
  const file = (event.target as HTMLInputElement).files?.[0];
  if (!file) return;
  const reader = new FileReader();
  reader.onload = (e) => {
    try {
      const json = JSON.parse(e.target?.result as string) as Item;
      store.setRootItem(json);
    } catch (err) {
      console.error("Failed to parse workspace file", err);
      alert("Invalid JSON file");
    }
  };
  reader.readAsText(file);
};
</script>

<template>
  <div class="container">
    <div class="sidebar">
      <div class="toolbar">
        <button @click="showAddSourceModal = true">+ Source</button>
        <button @click="downloadWorkspace">Export</button>
        <label class="import-btn">
          Import
          <input
            type="file"
            @change="uploadWorkspace"
            accept=".json"
            style="display: none"
          />
        </label>
      </div>
      <TreeView
        class="tree-view"
        :item="store.rootItem"
        @add-item="(parent: Item, item: Item) => store.addItem(parent, item)"
        @remove-item="(parent: Item, index: number) => store.removeItem(parent, index)"
      ></TreeView>
    </div>
    <Editor class="editor" data="{}" :services="services"></Editor>

    <AddSourceModal
      :show="showAddSourceModal"
      @close="showAddSourceModal = false"
      @add-reflection="onAddReflection"
      @add-descriptor="onAddDescriptor"
    />
  </div>
</template>

<style lang="css" scoped>
.container {
  display: flex;
  width: 100%;
  height: 100%;
}
.sidebar {
  width: 280px; /* Standard sidebar width */
  height: 100%;
  display: flex;
  flex-direction: column;
  border-right: 1px solid #e0e0e0;
  background-color: #fff;
}
.toolbar {
  height: 48px;
  display: flex;
  align-items: center;
  padding: 0 16px;
  border-bottom: 1px solid #e0e0e0;
  gap: 8px;
}
.tree-view {
  flex-grow: 1;
  overflow-y: auto;
  padding: 8px 0;
}
.editor {
  flex-grow: 1;
  height: 100%;
}

/* Material Button Styles */
button,
.import-btn {
  font-family: "Roboto", "Segoe UI", sans-serif;
  font-size: 14px;
  font-weight: 500;
  text-transform: uppercase;
  padding: 6px 16px;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  background-color: transparent;
  color: #6200ee; /* Primary Color */
  transition: background-color 0.2s;
  line-height: 1.25;
}

button:hover,
.import-btn:hover {
  background-color: rgba(98, 0, 238, 0.04);
}

.import-btn {
  display: inline-flex;
  align-items: center;
  border: 1px solid #e0e0e0; /* Outlined button style for Import */
  color: #333;
}
</style>
