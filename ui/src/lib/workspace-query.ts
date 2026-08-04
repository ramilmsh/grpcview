// connect-query data layer: every server read/write goes through here.
import { useMemo } from "react";
import {
  useQuery,
  useMutation,
  useTransport,
  createConnectQueryKey,
} from "@connectrpc/connect-query";
import { useQueryClient, type QueryKey } from "@tanstack/react-query";
import { createClient, ConnectError, Code } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import { WorkspaceService } from "@grpcview/v1/service_pb";
import {
  get,
  createCollection,
  createFolder,
  createRequest,
  deleteRequest,
  updateRequest,
  updateFolder,
  moveItem,
  addDescriptorSource,
  removeDescriptorSource,
  refreshDescriptorSource,
  reorderDescriptorSources,
  invoke,
  runScript,
  createScript,
  updateScript,
  deleteScript,
} from "@grpcview/v1/service-WorkspaceService_connectquery";
import { GetResponseSchema } from "@grpcview/v1/service_pb";
import type { Collection, Server } from "@grpcview/v1/workspace_pb";
import { rootItemsOf, type ItemWithPath } from "./format";

const COLLECTION_STORAGE_KEY = "grpcview.collection";

// 1a addresses ONE collection per load. Phase 1c adds the multi-collection tier and
// turns this into state; until then, switching collections is a reload, which also
// guarantees every connect-query key is rebuilt.
export const COLLECTION_ID = localStorage.getItem(COLLECTION_STORAGE_KEY) ?? ".";

// Switches the active collection and reloads, so every connect-query key (all of
// which close over COLLECTION_ID at module load) is rebuilt against the new one.
export function openCollection(id: string): void {
  localStorage.setItem(COLLECTION_STORAGE_KEY, id);
  window.location.reload();
}

export const firstReflectionSource = (ws?: Collection): Server | null => {
  for (const s of ws?.sources ?? []) {
    if (s.source.case === "reflection") return s.source.value;
  }
  return null;
};

// The Server a service is dialed at: the first reflection source that serves it,
// falling back to the collection's first reflection source (upload-only services).
export const sourceForService = (
  ws: Collection | undefined,
  service: string
): Server | null => {
  const svc = ws?.services.find((s) => `${s.package}.${s.name}` === service);
  return svc?.source ?? firstReflectionSource(ws);
};

// Where a service's SCHEMA came from (the source that won the priority merge),
// which need not be where its traffic goes. `live` is true only for reflection.
export const schemaSourceFor = (
  ws: Collection | undefined,
  service: string
): { id: string; live: boolean } | null => {
  const winner = ws?.sources.find((s) => s.resolved?.wonServiceNames.includes(service));
  if (winner) return { id: winner.id, live: winner.source.case === "reflection" };
  // Fallback for a collection whose sources predate per-source contributions.
  const dial = sourceForService(ws, service);
  return dial ? { id: `reflection:${dial.address}`, live: true } : null;
};

export const hostLabel = (s: Server | null): string => s?.address ?? "";

function useWorkspaceKey(): QueryKey {
  const transport = useTransport();
  return createConnectQueryKey({
    schema: get,
    transport,
    input: { collection: COLLECTION_ID },
    cardinality: "finite",
  });
}

export function useWorkspace() {
  const query = useQuery(get, { collection: COLLECTION_ID });
  const workspace = query.data?.collection;
  const notFound = query.isError && ConnectError.from(query.error).code === Code.NotFound;
  return {
    workspace,
    services: workspace?.services ?? [],
    sources: workspace?.sources ?? [],
    reflection: firstReflectionSource(workspace),
    notFound,
  };
}

// Creates a collection at a workspace-relative directory. Not wired to the Get
// cache: the caller (NoCollection) always follows success with openCollection,
// which reloads and re-seeds everything against the new COLLECTION_ID.
export function useCreateCollection() {
  return useMutation(createCollection);
}

export function useRootItems(workspace?: Collection): ItemWithPath[] {
  return useMemo(() => rootItemsOf(workspace?.item), [workspace]);
}

function useSeedGetCache() {
  const qc = useQueryClient();
  const key = useWorkspaceKey();
  return {
    onSuccess: (res: { collection?: Collection }) => {
      if (res.collection) qc.setQueryData(key, create(GetResponseSchema, { collection: res.collection }));
    },
  };
}

// The write RPCs, each seeding the Get cache with the fresh Collection it returns.
export function useWorkspaceMutations() {
  const opts = useSeedGetCache();
  return {
    createFolder: useMutation(createFolder, opts),
    createRequest: useMutation(createRequest, opts),
    deleteRequest: useMutation(deleteRequest, opts),
    updateRequest: useMutation(updateRequest, opts),
    updateFolder: useMutation(updateFolder, opts),
    moveItem: useMutation(moveItem, opts),
    addDescriptorSource: useMutation(addDescriptorSource, opts),
    removeDescriptorSource: useMutation(removeDescriptorSource, opts),
    refreshDescriptorSource: useMutation(refreshDescriptorSource, opts),
    reorderDescriptorSources: useMutation(reorderDescriptorSources, opts),
  };
}

export function useCreateScript() {
  return useMutation(createScript, useSeedGetCache());
}

export function useUpdateScript() {
  return useMutation(updateScript, useSeedGetCache());
}

export function useDeleteScript() {
  return useMutation(deleteScript, useSeedGetCache());
}

export function useInvoke() {
  return useMutation(invoke);
}

// Invalidates the Get query: Invoke persists run history server-side but does not
// return the fresh Collection the History timeline rides on.
export function useRefreshWorkspace() {
  const qc = useQueryClient();
  const key = useWorkspaceKey();
  return useMemo(() => () => void qc.invalidateQueries({ queryKey: key }), [qc, key]);
}

// The raw connect client: connect-query has no streaming hook, so InvokeStreaming
// is called directly on this.
export function useStreamingClient() {
  const transport = useTransport();
  return useMemo(() => createClient(WorkspaceService, transport), [transport]);
}

export function useRunScript() {
  return useMutation(runScript);
}
