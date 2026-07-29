// connect-query data layer. All server reads/writes go through here (verified
// pattern, plan §6/§13). zustand holds UI-only state (see ui-store.ts); server
// data lives in the react-query cache keyed by the Get query.
import { useMemo } from "react";
import {
  useQuery,
  useMutation,
  useTransport,
  createConnectQueryKey,
} from "@connectrpc/connect-query";
import { useQueryClient, type QueryKey } from "@tanstack/react-query";
import { createClient } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import { WorkspaceService } from "@grpcview/v1/service_pb";
import {
  get,
  createFolder,
  createRequest,
  deleteRequest,
  updateRequest,
  updateFolder,
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
import type { Workspace, Server } from "@grpcview/v1/workspace_pb";
import { rootItemsOf, type ItemWithPath } from "./format";

// Phase 1 is a single hardcoded workspace (plan §3).
export const WORKSPACE_NAME = "default";

// firstReflectionSource returns the Server backing the workspace's first
// reflection source — the source Invoke defaults its target to, and the one the
// target bar / connection indicator display. Null when there is no source yet.
export const firstReflectionSource = (ws?: Workspace): Server | null => {
  for (const s of ws?.sources ?? []) {
    if (s.source.case === "reflection") return s.source.value;
  }
  return null;
};

// sourceForService returns the Server a service is dialed at (Service.source: the
// first reflection source in priority order that serves it), so a request defaults
// its invoke target to a source that actually serves it — not merely the
// workspace's first reflection source. Falls back to the first reflection source
// when no reflection source serves the service at all (upload-only).
export const sourceForService = (
  ws: Workspace | undefined,
  service: string
): Server | null => {
  const svc = ws?.services.find((s) => `${s.package}.${s.name}` === service);
  return svc?.source ?? firstReflectionSource(ws);
};

// schemaSourceFor describes WHERE A SERVICE'S SCHEMA CAME FROM, which is not the
// same question as where its requests go: the definition source that won its
// protos in the priority merge (Resolved.wonServiceNames) may well be an upload,
// while the traffic goes to a reflection source further down the list. `live` is
// true only for a reflection source — an upload has no address to connect to.
//
// The dial fallback covers a workspace whose sources have not been re-resolved
// since per-source contributions started being recorded (nothing has won
// anything yet): showing the source it dials beats showing nothing.
export const schemaSourceFor = (
  ws: Workspace | undefined,
  service: string
): { id: string; live: boolean } | null => {
  const winner = ws?.sources.find((s) => s.resolved?.wonServiceNames.includes(service));
  if (winner) return { id: winner.id, live: winner.source.case === "reflection" };
  const dial = sourceForService(ws, service);
  return dial ? { id: `reflection:${dial.address}`, live: true } : null;
};

export const hostLabel = (s: Server | null): string => s?.address ?? "";

// useWorkspaceKey is the react-query key for the Get query, used to seed the
// cache from mutation results (every mutation returns the fresh Workspace).
function useWorkspaceKey(): QueryKey {
  const transport = useTransport();
  return createConnectQueryKey({
    schema: get,
    transport,
    input: { workspaceName: WORKSPACE_NAME },
    cardinality: "finite",
  });
}

// useWorkspace reads the workspace snapshot and its services/sources. The
// collection tree is derived separately (useRootItems) so the connection/source
// consumers that don't render the tree don't pay for the recursive conversion.
export function useWorkspace() {
  const query = useQuery(get, { workspaceName: WORKSPACE_NAME });
  const workspace = query.data?.workspace;
  return {
    workspace,
    services: workspace?.services ?? [],
    sources: workspace?.sources ?? [],
    reflection: firstReflectionSource(workspace),
  };
}

// useRootItems derives the flattened collection tree from a workspace snapshot,
// memoized so the recursive conversion runs only when the workspace changes.
export function useRootItems(workspace?: Workspace): ItemWithPath[] {
  return useMemo(() => rootItemsOf(workspace?.item), [workspace]);
}

// useSeedGetCache returns the mutation options that re-seed the Get cache with the
// fresh Workspace a write returns, so the UI updates without a refetch round-trip.
// Every WorkspaceService write returns the whole Workspace, so this is shared by
// useWorkspaceMutations and the script mutations below.
function useSeedGetCache() {
  const qc = useQueryClient();
  const key = useWorkspaceKey();
  return {
    onSuccess: (res: { workspace?: Workspace }) => {
      if (res.workspace) qc.setQueryData(key, create(GetResponseSchema, { workspace: res.workspace }));
    },
  };
}

// useWorkspaceMutations wires the write RPCs, each seeding the Get cache with the
// fresh Workspace it returns so the tree updates without a refetch round-trip.
export function useWorkspaceMutations() {
  const opts = useSeedGetCache();
  return {
    createFolder: useMutation(createFolder, opts),
    createRequest: useMutation(createRequest, opts),
    deleteRequest: useMutation(deleteRequest, opts),
    updateRequest: useMutation(updateRequest, opts),
    // updateFolder patches a folder's draft_metadata_script (the new folder-metadata editor,
    // gv-features-plan.md Feature 1). Mirrors updateRequest: it seeds the Get cache with the fresh
    // Workspace it returns, so the tree (and any inherit()-dependent request) reflects the save
    // without a refetch round-trip.
    updateFolder: useMutation(updateFolder, opts),
    // The four descriptor-source mutations. Each returns the whole re-derived
    // Workspace (sources, merged services, merged descriptor_set), so seeding the
    // Get cache with it is what makes a reorder or refresh take effect everywhere —
    // the method picker, the editor's generated types — without a refetch.
    addDescriptorSource: useMutation(addDescriptorSource, opts),
    removeDescriptorSource: useMutation(removeDescriptorSource, opts),
    refreshDescriptorSource: useMutation(refreshDescriptorSource, opts),
    reorderDescriptorSources: useMutation(reorderDescriptorSources, opts),
  };
}

// Script mutations (plan §S1). Each mirrors the workspace mutations: it calls the
// connect-query descriptor and re-seeds the Get cache from the returned Workspace,
// so the Scripts sidebar (which rides the Get snapshot) updates immediately.
// CreateScript adds an empty script of a kind; UpdateScript patches source and/or
// renames (newName); DeleteScript removes one.
export function useCreateScript() {
  return useMutation(createScript, useSeedGetCache());
}

export function useUpdateScript() {
  return useMutation(updateScript, useSeedGetCache());
}

export function useDeleteScript() {
  return useMutation(deleteScript, useSeedGetCache());
}

// useInvoke is the unary Invoke mutation. Its result is handled by the caller and
// stashed per-request in the UI store so it survives tab switches (plan §6).
export function useInvoke() {
  return useMutation(invoke);
}

// useRefreshWorkspace invalidates the Get query so the next read re-fetches the
// workspace snapshot. Invoke persists run history server-side but doesn't return
// the fresh Workspace, so the History timeline (which rides along on Get) is
// refreshed by calling this after a run completes.
export function useRefreshWorkspace() {
  const qc = useQueryClient();
  const key = useWorkspaceKey();
  return useMemo(() => () => void qc.invalidateQueries({ queryKey: key }), [qc, key]);
}

// useStreamingClient returns the raw connect client for the streaming invoke.
// connect-query has no streaming hook, so the server-streaming InvokeStreaming
// RPC is called directly on this client (it returns an AsyncIterable of frames);
// the transport is the same <TransportProvider> one connect-query uses.
export function useStreamingClient() {
  const transport = useTransport();
  return useMemo(() => createClient(WorkspaceService, transport), [transport]);
}

// useRunScript is the script test-run mutation backing the Scripts view. The
// caller passes the current editor buffer and the script's kind ({ source, kind })
// so the engine picks the matching profile/calling convention (a generator's
// `export default`, a middleware's `handle`, or — unset/scenario — last-expression
// scratchpad eval). The result (value / logs / error) is self-contained, so the
// caller keeps it in local component state rather than the workspace cache.
export function useRunScript() {
  return useMutation(runScript);
}
