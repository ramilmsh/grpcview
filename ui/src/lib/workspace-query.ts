// connect-query data layer: every server read/write goes through here.
import { useMemo, useRef } from "react";
import {
  useQuery,
  useMutation,
  useTransport,
  createConnectQueryKey,
  createQueryOptions,
  skipToken,
} from "@connectrpc/connect-query";
import { useQueries, useQueryClient, type QueryKey } from "@tanstack/react-query";
import { createClient, ConnectError, Code, type Transport } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import { WorkspaceService } from "@grpcview/v1/service_pb";
import {
  get,
  listCollections,
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
  setDescriptorSourceCommit,
  invoke,
  runScript,
  createScript,
  updateScript,
  deleteScript,
} from "@grpcview/v1/service-WorkspaceService_connectquery";
import { GetResponseSchema } from "@grpcview/v1/service_pb";
import type { CollectionSummary } from "@grpcview/v1/service_pb";
import type { Collection, Server } from "@grpcview/v1/workspace_pb";
import { rootItemsOf, type ItemWithPath } from "./format";
import { resolveActiveCollection } from "./active-collection";
import { useUIStore } from "./ui-store";

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

// The Get key for ONE collection. Per-collection by construction, so switching the
// active collection reads a different cache entry instead of clobbering a shared one.
// Exported so a test can pin it against the key connect-query's own builders produce —
// useCollectionItems and every write's cache seed have to agree on it or a seeded snapshot
// never reaches the tier.
export function keyForCollection(transport: Transport, id: string): QueryKey {
  return createConnectQueryKey({
    schema: get,
    transport,
    input: { collection: id },
    cardinality: "finite",
  });
}

function useCollectionsKey(): QueryKey {
  const transport = useTransport();
  return createConnectQueryKey({
    schema: listCollections,
    transport,
    input: {},
    cardinality: "finite",
  });
}

// Every collection in the workspace, plus the workspace root's absolute path. Cheap by
// construction (ListCollections reads manifests, never trees), so it is the one query
// that runs before anything knows which collection to Get.
export function useCollections(): {
  root: string;
  collections: CollectionSummary[];
  isPending: boolean;
  error: ConnectError | null;
} {
  const query = useQuery(listCollections, {});
  return {
    root: query.data?.root ?? "",
    collections: query.data?.collections ?? [],
    isPending: query.isPending,
    error: query.error,
  };
}

// The collection every scoped view addresses: the user's explicit choice while it still
// exists in the workspace, else the first collection listed. Null only when the
// workspace holds none, which is what puts <NoCollection> on screen. A pure derivation —
// no effect, no write-back (see active-collection.ts).
export function useActiveCollectionId(): string | null {
  const stored = useUIStore((s) => s.activeCollection);
  const { collections } = useCollections();
  return resolveActiveCollection(stored, collections);
}

// Takes the id explicitly, because the collection tier needs to Get one that is not
// active. Null means there is nothing to Get: skipToken idles the query rather than
// firing a request for the empty collection id.
export function useWorkspace(collection: string | null) {
  const query = useQuery(get, collection === null ? skipToken : { collection });
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

// What every scoped view uses: the active collection's snapshot, plus the id itself so
// the view can address its mutations at it.
export function useActiveWorkspace() {
  const collection = useActiveCollectionId();
  const ws = useWorkspace(collection);
  return { collection, ...ws };
}

// Creates a collection at a workspace-relative directory, then makes it the active one
// with no reload: the Get cache is seeded off the Collection the RPC returns, the
// listing it must now appear in is invalidated, and the choice is persisted.
export function useCreateCollection() {
  const qc = useQueryClient();
  const transport = useTransport();
  const listKey = useCollectionsKey();
  const setActiveCollection = useUIStore((s) => s.setActiveCollection);
  return useMutation(createCollection, {
    onSuccess: (res) => {
      const id = res.collection?.id;
      if (!id) return;
      qc.setQueryData(
        keyForCollection(transport, id),
        create(GetResponseSchema, { collection: res.collection })
      );
      void qc.invalidateQueries({ queryKey: listKey });
      setActiveCollection(id);
    },
  });
}

// Keys are collection-prefixed, and the collection comes off the RESPONSE, so the tree
// of whatever snapshot is in hand is keyed correctly with no extra argument.
export function useRootItems(workspace?: Collection): ItemWithPath[] {
  return useMemo(() => rootItemsOf(workspace?.item, workspace?.id ?? ""), [workspace]);
}

// The root items of each of `ids`, one Get per collection — what the panel's collection tier
// renders. A dynamic number of queries, so it cannot be a loop of useWorkspace calls; it is
// TanStack's useQueries over connect-query's own options builder.
//
// The key is therefore IDENTICAL to useWorkspace's: `useQuery(get, {collection})` is
// literally `useQuery(createQueryOptions(get, input, {transport}))`, and createQueryOptions
// derives its key with `createConnectQueryKey({schema, input, transport, cardinality:
// "finite"})` — the same call keyForCollection makes. So the active collection is fetched
// ONCE however many hooks watch it, and every write RPC's cache seed (useSeedGetCache)
// lands in these queries too.
//
// A collection whose Get has not landed is ABSENT from the map — which the panel renders as
// a "Loading…" row, distinct from a collection that loaded and is empty ([]).
export function useCollectionItems(
  ids: readonly string[]
): ReadonlyMap<string, ItemWithPath[]> {
  const transport = useTransport();
  const results = useQueries({
    queries: ids.map((id) => createQueryOptions(get, { collection: id }, { transport })),
  });

  // The map's IDENTITY has to survive a render that changed nothing: usePanelTreeAdapter
  // memoizes on it, and a fresh Map per render would rebuild the adapter and cost the tree
  // its node identity. useQueries hands back a new array of new result objects every render,
  // but `data` is the query cache's own object (connect-query's structuralSharing keeps it
  // stable across refetches that changed nothing), so (ids, those references) is the whole
  // signature. A ref rather than useMemo because the dependency list is variable-length.
  const cache = useRef<{
    signature: readonly unknown[];
    map: ReadonlyMap<string, ItemWithPath[]>;
  }>();
  const signature: unknown[] = [];
  for (const [i, id] of ids.entries()) signature.push(id, results[i]?.data?.collection);

  const previous = cache.current;
  if (
    previous !== undefined &&
    previous.signature.length === signature.length &&
    previous.signature.every((value, i) => value === signature[i])
  ) {
    return previous.map;
  }

  const map = new Map<string, ItemWithPath[]>();
  for (const [i, id] of ids.entries()) {
    const collection = results[i]?.data?.collection;
    // Keyed by the id we asked for (the same id ListCollections reported, which is what the
    // tier looks up), while the ITEMS carry the id off the response — exactly as
    // useRootItems does, so both paths build identical keys for the same request.
    if (collection !== undefined) {
      map.set(id, rootItemsOf(collection.item, collection.id || id));
    }
  }
  cache.current = { signature, map };
  return map;
}

// Seeds the Get cache off `res.collection.id`, never off the id the caller happened to
// send: that is the only identifier guaranteed to name the collection the fresh snapshot
// describes, and it is why Collection.id exists. Consequence: none of the write hooks
// below need a collection argument.
function useSeedGetCache() {
  const qc = useQueryClient();
  const transport = useTransport();
  return {
    onSuccess: (res: { collection?: Collection }) => {
      const id = res.collection?.id;
      if (!id) return;
      qc.setQueryData(
        keyForCollection(transport, id),
        create(GetResponseSchema, { collection: res.collection })
      );
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
    setDescriptorSourceCommit: useMutation(setDescriptorSourceCommit, opts),
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

// Invalidates one collection's Get query: Invoke persists run history server-side but
// does not return the fresh Collection the History timeline rides on. A no-op with no
// collection, since there is then no key to invalidate.
export function useRefreshWorkspace(collection: string | null) {
  const qc = useQueryClient();
  const transport = useTransport();
  return useMemo(
    () => () => {
      if (collection === null) return;
      void qc.invalidateQueries({ queryKey: keyForCollection(transport, collection) });
    },
    [qc, transport, collection]
  );
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
