import { defineStore } from "pinia";
import { ref } from "vue";

export interface Item {
  name: string;
  type: "grpc" | "folder";
  children?: Item[];
}

export const useWorkspaceStore = defineStore(
  "workspace",
  () => {
    const rootItem = ref<Item>({ name: "", type: "folder" });

    function addItem(parent: Item, item: Item): void {
      parent.children = parent.children ?? [];
      parent.children.push(item);
    }

    return { rootItem, addItem };
  },
  { persist: true }
);
