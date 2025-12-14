import { create } from "zustand";
import { persist } from "zustand/middleware";
import { Service, Method } from "@grpcview/v1/workspace_pb";

// Proto Mirrors
// We are partially mirroring the proto structure here for the state.
// Since we are using creating items on the client side before saving (or just keeping them in local state),
// we need a mutable structure.

export interface RequestItem {
  service?: Service;
  method?: Method;
  request: string; // Stored as JSON string locally for editor
}

export interface FolderItem {
  items: Item[];
}

export interface Item {
  name: string;
  id: string; // We enforce ID on the client
  content:
    | {
        case: "folder";
        value: FolderItem;
      }
    | {
        case: "request";
        value: RequestItem;
      };
}

interface WorkspaceState {
  rootItems: Item[]; // Changed from single rootItem to list of items at root to match WorkspaceSnapshot.items
  services: Service[];

  // Actions
  setServices: (services: Service[]) => void;
  setRootItems: (items: any[]) => void; // Accepts proto items and converts them
  addItem: (parent: Item | null, item: Item) => void; // parent null = root
  removeItem: (parent: Item | null, index: number) => void;
  renameItem: (item: Item, newName: string) => void;
  updateRequestData: (item: Item, data: string) => void;
}

// Helpers
const ensureIds = (protoItem: any): Item => {
  const item: Item = {
    name: protoItem.name || "Untitled",
    id: crypto.randomUUID(),
    content: { case: "folder", value: { items: [] } }, // default
  };

  if (protoItem.content?.case === "folder" || protoItem.content?.value?.items) {
    // Handle Proto conversion looseness
    // If it came from proto, it might be nested under `folder`.
    // But `WorkspaceSnapshot.items` is `repeated Item`.
    // `Item` has `oneof content`.
    const folder = protoItem.content?.value || protoItem.folder;
    const children = folder?.items || [];
    item.content = {
      case: "folder",
      value: {
        items: children.map(ensureIds),
      },
    };
  } else if (protoItem.content?.case === "request" || protoItem.request) {
    const req = protoItem.content?.value || protoItem.request;
    // Convert bytes request to string
    let reqBody = "{}";
    if (req.request instanceof Uint8Array) {
      reqBody = new TextDecoder().decode(req.request);
    } else if (typeof req.request === "string") {
      // It might be base64 if coming from partial JSON
      try {
        reqBody = atob(req.request);
      } catch {
        reqBody = req.request;
      }
    }

    item.content = {
      case: "request",
      value: {
        service: req.service,
        method: req.method,
        request: reqBody,
      },
    };
  }

  return item;
};

// Recursive finder
const findAndModify = (
  items: Item[],
  targetId: string,
  action: (node: Item, parentArray: Item[], index: number) => void
): boolean => {
  for (let i = 0; i < items.length; i++) {
    const item = items[i];
    if (item.id === targetId) {
      action(item, items, i);
      return true;
    }
    if (item.content.case === "folder") {
      if (findAndModify(item.content.value.items, targetId, action))
        return true;
    }
  }
  return false;
};

export const useWorkspaceStore = create<WorkspaceState>()(
  persist(
    (set) => ({
      rootItems: [],
      services: [],

      setServices: (services) => set({ services }),

      setRootItems: (protoItems) =>
        set({
          rootItems: protoItems.map(ensureIds),
        }),

      addItem: (parent, newItem) =>
        set((state) => {
          const newRoots = JSON.parse(
            JSON.stringify(state.rootItems)
          ) as Item[];
          if (!parent) {
            // Add to root
            newRoots.push(newItem);
          } else {
            // Find parent
            findAndModify(newRoots, parent.id, (node) => {
              if (node.content.case === "folder") {
                node.content.value.items.push(newItem);
              }
            });
          }
          return { rootItems: newRoots };
        }),

      removeItem: (parent, index) =>
        set((state) => {
          const newRoots = JSON.parse(
            JSON.stringify(state.rootItems)
          ) as Item[];
          if (!parent) {
            if (index >= 0 && index < newRoots.length) {
              newRoots.splice(index, 1);
            }
          } else {
            findAndModify(newRoots, parent.id, (node) => {
              if (node.content.case === "folder") {
                node.content.value.items.splice(index, 1);
              }
            });
          }
          return { rootItems: newRoots };
        }),

      renameItem: (item, newName) =>
        set((state) => {
          const newRoots = JSON.parse(
            JSON.stringify(state.rootItems)
          ) as Item[];
          findAndModify(newRoots, item.id, (node) => {
            node.name = newName;
          });
          return { rootItems: newRoots };
        }),

      updateRequestData: (item, data) =>
        set((state) => {
          const newRoots = JSON.parse(
            JSON.stringify(state.rootItems)
          ) as Item[];
          findAndModify(newRoots, item.id, (node) => {
            if (node.content.case === "request") {
              node.content.value.request = data;
            }
          });
          return { rootItems: newRoots };
        }),
    }),
    {
      name: "workspace-storage",
      partialize: (state) => ({ rootItems: state.rootItems }), // Don't persist services as they might be stale/large, reload on start
    }
  )
);
