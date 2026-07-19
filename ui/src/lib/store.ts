import { create } from "zustand";
import { createClient } from "@connectrpc/connect";
import { transport } from "./client";
import {
  WorkspaceService,
  CreateFolderRequestSchema,
  CreateRequestRequestSchema,
  DeleteRequestRequestSchema,
  UpdateRequestRequestSchema,
  GetRequestSchema,
  InvokeRequestSchema,
} from "@grpcview/v1/service_pb";
import {
  Service,
  Item,
  Workspace,
  Request_Response,
} from "@grpcview/v1/workspace_pb";
import { create as createMessage, JsonObject } from "@bufbuild/protobuf";

const client = createClient(WorkspaceService, transport);

// Extended Item with path for UI navigation
// We track paths separately since proto Item doesn't have a path field
export interface ItemWithPath {
  item: Item;
  path: string[]; // Path to this item's parent folder (empty for root items)
  children?: ItemWithPath[]; // Populated for folders
}

// itemKey is the stable identity of a request within the tree (parent path +
// name), used to key per-request invocation state so a response survives
// navigating away and back.
export const itemKey = (item: ItemWithPath): string =>
  [...item.path, item.item.name].join("/");

interface WorkspaceState {
  // The full workspace from the backend
  workspace: Workspace | null;

  // Flattened items for easier access (computed from workspace.item)
  rootItems: ItemWithPath[];
  services: Service[];

  // Per-request invocation state, keyed by itemKey(). Responses are ephemeral
  // (not persisted): they live here until the next send or a page reload.
  responses: Record<string, Request_Response>;
  responseErrors: Record<string, string>;
  invoking: Record<string, boolean>;

  // Actions
  loadWorkspace: () => Promise<void>;
  addDescriptorSource: (req: any) => Promise<void>;
  createFolder: (parent: ItemWithPath | null, name: string) => Promise<void>;
  createRequest: (
    parent: ItemWithPath | null,
    name: string,
    service: string,
    method: string
  ) => Promise<void>;
  deleteItem: (item: ItemWithPath) => Promise<void>;
  renameItem: (item: ItemWithPath, newName: string) => Promise<void>;
  updateRequestData: (item: ItemWithPath, data: string) => Promise<void>;
  updateRequestMetadata: (
    item: ItemWithPath,
    metadata: JsonObject
  ) => Promise<void>;
  invoke: (
    item: ItemWithPath,
    body: string,
    metadata: JsonObject
  ) => Promise<void>;
}

// Recursively convert proto Items to ItemWithPath with path tracking
const convertToItemWithPath = (
  protoItem: Item,
  parentPath: string[]
): ItemWithPath => {
  const result: ItemWithPath = {
    item: protoItem,
    path: parentPath,
  };

  switch (protoItem.content.case) {
    case "folder":
      const childPath = [...parentPath, protoItem.name];
      result.children = protoItem.content.value.items.map((child) =>
        convertToItemWithPath(child, childPath)
      );
      break;
    default:
      break;
  }

  return result;
};

// sendRequestUpdate persists a partial request update (draft body and/or
// metadata) and reloads the workspace. Shared by the body and metadata editors.
const sendRequestUpdate = async (
  item: ItemWithPath,
  patch: { draftBody?: string; draftMetadata?: JsonObject },
  get: () => WorkspaceState
) => {
  const req = createMessage(UpdateRequestRequestSchema, {
    workspaceName: "default",
    path: item.path,
    itemName: item.item.name,
    ...patch,
  });
  await client.updateRequest(req);
  await get().loadWorkspace();
};

export const useWorkspaceStore = create<WorkspaceState>()((set, get) => ({
  workspace: null,
  rootItems: [],
  services: [],
  responses: {},
  responseErrors: {},
  invoking: {},

  loadWorkspace: async () => {
    try {
      const req = createMessage(GetRequestSchema, { workspaceName: "default" });
      const res = await client.get(req);
      if (res.workspace) {
        const ws = res.workspace;
        set({ workspace: ws, services: ws.services });

        // Convert root folder's items to ItemWithPath
        if (ws.item && ws.item.content.case === "folder") {
          const rootItems = ws.item.content.value.items.map((child) =>
            convertToItemWithPath(child, [])
          );
          set({ rootItems });
        } else {
          set({ rootItems: [] });
        }
      }
    } catch (e) {
      console.error("Failed to load workspace", e);
    }
  },

  addDescriptorSource: async (reqData: any) => {
    try {
      reqData.workspaceName = "default";
      await client.addDescriptorSource(reqData);
      await get().loadWorkspace();
    } catch (e) {
      console.error("Add descriptor failed", e);
      throw e;
    }
  },

  createFolder: async (parent, folderName) => {
    try {
      const parentPath = parent ? [...parent.path, parent.item.name] : [];
      const req = createMessage(CreateFolderRequestSchema, {
        workspaceName: "default",
        path: parentPath,
        itemName: folderName,
      });
      await client.createFolder(req);
      await get().loadWorkspace();
    } catch (e) {
      console.error("Create folder failed", e);
    }
  },

  createRequest: async (parent, reqName, service, method) => {
    try {
      const parentPath = parent ? [...parent.path, parent.item.name] : [];
      const req = createMessage(CreateRequestRequestSchema, {
        workspaceName: "default",
        path: parentPath,
        itemName: reqName,
        service,
        method,
      });
      await client.createRequest(req);
      await get().loadWorkspace();
    } catch (e) {
      console.error("Create request failed", e);
    }
  },

  deleteItem: async (item) => {
    try {
      const req = createMessage(DeleteRequestRequestSchema, {
        workspaceName: "default",
        path: item.path,
        itemName: item.item.name,
      });
      await client.deleteRequest(req);
      await get().loadWorkspace();
    } catch (e) {
      console.error("Delete item failed", e);
    }
  },

  renameItem: async (_item, _newName) => {
    console.warn("Rename not fully supported by backend yet");
  },

  updateRequestData: async (item, data) => {
    try {
      await sendRequestUpdate(item, { draftBody: data }, get);
    } catch (e) {
      console.error("Update request failed", e);
    }
  },

  updateRequestMetadata: async (item, metadata) => {
    try {
      await sendRequestUpdate(item, { draftMetadata: metadata }, get);
    } catch (e) {
      console.error("Update request metadata failed", e);
    }
  },

  invoke: async (item, body, metadata) => {
    if (item.item.content.case !== "request") return;
    const { service, method } = item.item.content.value;
    const key = itemKey(item);

    set((s) => ({
      invoking: { ...s.invoking, [key]: true },
      responseErrors: { ...s.responseErrors, [key]: "" },
    }));

    try {
      const req = createMessage(InvokeRequestSchema, {
        workspaceName: "default",
        path: item.path,
        itemName: item.item.name,
        service,
        method,
        body,
        metadata,
      });
      const res = await client.invoke(req);
      set((s) => ({
        responses: res.response
          ? { ...s.responses, [key]: res.response }
          : s.responses,
      }));
    } catch (e: any) {
      // A gRPC-level failure of the invoked call comes back as data (in
      // response.status); this catch only fires for grpcview-side errors
      // (unreachable schema, bad body, no target).
      set((s) => ({
        responseErrors: {
          ...s.responseErrors,
          [key]: e?.message ?? String(e),
        },
      }));
    } finally {
      set((s) => ({ invoking: { ...s.invoking, [key]: false } }));
    }
  },
}));
