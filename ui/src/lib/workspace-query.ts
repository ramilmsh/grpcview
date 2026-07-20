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
import { create } from "@bufbuild/protobuf";
import {
  get,
  createFolder,
  createRequest,
  deleteRequest,
  updateRequest,
  addDescriptorSource,
  invoke,
  runScript,
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

export const hostLabel = (s: Server | null): string =>
  s ? `${s.host}:${s.port}` : "";

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

// useWorkspaceMutations wires the write RPCs, each seeding the Get cache with the
// fresh Workspace it returns so the tree updates without a refetch round-trip.
export function useWorkspaceMutations() {
  const qc = useQueryClient();
  const key = useWorkspaceKey();
  const seed = (ws?: Workspace) => {
    if (ws) qc.setQueryData(key, create(GetResponseSchema, { workspace: ws }));
  };
  const opts = { onSuccess: (res: { workspace?: Workspace }) => seed(res.workspace) };

  return {
    createFolder: useMutation(createFolder, opts),
    createRequest: useMutation(createRequest, opts),
    deleteRequest: useMutation(deleteRequest, opts),
    updateRequest: useMutation(updateRequest, opts),
    addDescriptorSource: useMutation(addDescriptorSource, opts),
  };
}

// useInvoke is the unary Invoke mutation. Its result is handled by the caller and
// stashed per-request in the UI store so it survives tab switches (plan §6).
export function useInvoke() {
  return useMutation(invoke);
}

// useRunScript is the ad-hoc script-eval mutation backing the Scripts scratchpad.
// The result (value / logs / error) is self-contained, so the caller keeps it in
// local component state rather than the workspace cache.
export function useRunScript() {
  return useMutation(runScript);
}
