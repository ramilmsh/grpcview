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

    function removeItem(parent: Item, index: number): void {
      if (parent.children && index >= 0 && index < parent.children.length) {
        parent.children.splice(index, 1);
      }
    }

    function renameItem(item: Item, newName: string): void {
      item.name = newName;
    }

    function setRootItem(item: Item): void {
      rootItem.value = item;
    }

    return { rootItem, addItem, removeItem, renameItem, setRootItem };
  },
  { persist: true }
);
