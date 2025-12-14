import { create } from "zustand";
import { persist } from "zustand/middleware";

export interface Item {
  name: string;
  type: "grpc" | "folder";
  children?: Item[];
}

interface WorkspaceState {
  rootItem: Item;
  addItem: (parent: Item, item: Item) => void;
  removeItem: (parent: Item, index: number) => void;
  renameItem: (item: Item, newName: string) => void;
  setRootItem: (item: Item) => void;
}

// Helper to find and modify the nested item structure
// Since zustand state is immutable, we need to clone.
// But we are passing references to 'parent'.
// In Zustand/React, we usually operate on IDs or paths.
// The Vue implementation passed object references directly because Vue's reactivity system handles it.
// In React/Zustand, passing object references from the state and mutating them DIRECTLY inside the store
// might work if we use Immer or careful copying.
// However, the `addItem` signature `(parent: Item, item: Item)` assumes we have the parent object.
// If `parent` is a reference to an object inside the `rootItem` tree, we can mutate it if we use Immer
// or if we manually traverse.

// Simplified approach used in Vue:
// function addItem(parent: Item, item: Item): void {
//   parent.children = parent.children ?? [];
//   parent.children.push(item);
// }
// This mutates the object.

// In Zustand with `persist`, we should return a NEW state.
// We can use `immer` middleware to allow mutation syntax.
// Or we can just trust that `parent` is a reference to the mutable object if we were using a mutable store,
// but Zustand isn't mutable by default in that way without Immer.
// Actually, if we pass the object from the UI, it's a reference to the read-only state. Mutating it won't trigger update.

// Let's use Immer to make this easier and closer to the Vue logic.
// But I didn't install immer.
// I will implement a path-based or recursive find approach,
// OR I will just assume for now I can mutate if I clone the root.

// BETTER APPROACH:
// The Vue app works by passing the item reference.
// The React way is usually passing an ID or Path.
// Since the Items don't have IDs, we probably rely on Path or Reference.
// Providing a path array is safer.
// But to keep it simple and effective:
// I'll add an ID to every item or just use the reference if I can find it in the tree.

// Let's try to stick to the Vue logic but wrap in a state update that clones.
// Use `immer` would be best. I'll add `immer` to possible deps or just implement a deep clone helper.
// Actually, I can use `produce` from `immer` if I install it, but I didn't.
// Let's just implement `setRootItem` easily.
// For `addItem`, I might need to traverse.
// Since the tree can be deep, finding the parent by reference in a deep clone is tricky without ids.
//
// OPTION: Add IDs to items.
// Check if I can add `id: string` to Item.
// The Vue app didn't use IDs.
// I will add a `uuid` or similar.

// Let's modify `Item` to have an optional `id` for React keyING anyway.

export interface ItemWithId extends Item {
  id?: string;
  children?: ItemWithId[];
}

export const ensureIds = (item: ItemWithId): ItemWithId => {
  if (!item.id) item.id = crypto.randomUUID();
  if (item.children) {
    item.children.forEach(ensureIds);
  }
  return item;
};

// Function to find node and perform action
const findAndModify = (
  root: ItemWithId,
  targetId: string,
  action: (node: ItemWithId) => void
): boolean => {
  if (root.id === targetId) {
    action(root);
    return true;
  }
  if (root.children) {
    for (const child of root.children) {
      if (findAndModify(child, targetId, action)) return true;
    }
  }
  return false;
};

export const useWorkspaceStore = create<WorkspaceState>()(
  persist(
    (set) => ({
      rootItem: { name: "Root", type: "folder", id: "root", children: [] },
      addItem: (parent: ItemWithId, item: ItemWithId) =>
        set((state) => {
          const newRoot = JSON.parse(JSON.stringify(state.rootItem)); // Deep clone
          const targetId = parent.id || "root"; // Fallback to root if undefined, but should be defined.
          ensureIds(item);

          // Find parent in newRoot
          findAndModify(newRoot, targetId, (node) => {
            node.children = node.children || [];
            node.children.push(item);
          });

          return { rootItem: newRoot };
        }),
      removeItem: (parent: ItemWithId, index: number) =>
        set((state) => {
          const newRoot = JSON.parse(JSON.stringify(state.rootItem));
          const targetId = parent.id || "root";
          findAndModify(newRoot, targetId, (node) => {
            if (node.children) {
              node.children.splice(index, 1);
            }
          });
          return { rootItem: newRoot };
        }),
      renameItem: (item: ItemWithId, newName: string) =>
        set((state) => {
          const newRoot = JSON.parse(JSON.stringify(state.rootItem));
          const targetId = item.id;
          if (!targetId) return {};
          findAndModify(newRoot, targetId, (node) => {
            node.name = newName;
          });
          return { rootItem: newRoot };
        }),
      setRootItem: (item: Item) =>
        set(() => {
          const withIds = ensureIds(item as ItemWithId);
          return { rootItem: withIds };
        }),
    }),
    {
      name: "workspace-storage",
    }
  )
);
