import type { Method, Service } from "@grpcview/v1/workspace_pb";
import {
  useCollections,
  useRootItems,
  useWorkspace,
  useWorkspaceMutations,
} from "@/lib/workspace-query";
import { NEW_REQUEST_KEY, useUIStore } from "@/lib/ui-store";
import {
  findByKey,
  methodKind,
  resolveMethod,
  rootItemsOf,
  serviceName,
  slugKeyIn,
} from "@/lib/format";
import {
  canCreate,
  folderLabel,
  nameAfterPick,
  nameCollides,
  siblingNames,
} from "./new-request-draft";

// Everything the draft's two halves — its tree row and its page — derive from the one
// requestDraft slice. Scoped to the draft's OWN collection, not the active one: focusing
// another collection's row in the tier must not swap the services under a half-filled draft.
export function useRequestDraft() {
  const draft = useUIStore((s) => s.requestDraft);
  const setRequestDraft = useUIStore((s) => s.setRequestDraft);
  const commitRequestDraft = useUIStore((s) => s.commitRequestDraft);
  const closeTab = useUIStore((s) => s.closeTab);
  const { workspace, services } = useWorkspace(draft?.collection ?? null);
  const rootItems = useRootItems(workspace);
  const { collections } = useCollections();
  const { createRequest } = useWorkspaceMutations();

  const collectionName =
    collections.find((c) => c.id === draft?.collection)?.name ||
    (draft?.collection ?? "");
  const folder = folderLabel(collectionName, draft?.folder ?? []);
  const siblings = draft ? (siblingNames(rootItems, draft.folder) ?? []) : [];
  const pending = draft?.pending ?? false;
  // Not while pending: the fresh snapshot lands in the cache a moment before the draft is
  // committed, and the new request would otherwise "collide" with its own placeholder.
  const collides = !pending && nameCollides(siblings, draft?.name ?? "");
  const kind = draft?.method
    ? methodKind(
        resolveMethod(services, draft.method.service, draft.method.method),
      )
    : null;
  const ready =
    draft !== null &&
    canCreate({ name: draft.name, method: draft.method, collides, pending });

  const cancel = () => closeTab(NEW_REQUEST_KEY);

  const setName = (name: string) => setRequestDraft({ name, error: null });

  const pick = (service: Service, method: Method) =>
    setRequestDraft({
      method: { service: serviceName(service), method: method.name },
      name: nameAfterPick(draft?.name ?? "", method.name),
      error: null,
    });

  // mutateAsync, not mutate's per-call callbacks: those are dropped if the caller unmounts
  // (the row does when its folder collapses), and this must still land.
  const submit = async () => {
    if (!ready || !draft?.method) return;
    const name = draft.name.trim();
    const { collection, folder: path } = draft;
    setRequestDraft({ pending: true, error: null });
    try {
      const res = await createRequest.mutateAsync({
        collection,
        path,
        itemName: name,
        service: draft.method.service,
        method: draft.method.method,
      });
      const id = res.collection?.id || collection;
      const key = slugKeyIn(id, res.collection?.item, path, name);
      const item = findByKey(rootItemsOf(res.collection?.item, id), key);
      if (item) commitRequestDraft(item);
      else cancel();
    } catch (e) {
      setRequestDraft({
        pending: false,
        error: e instanceof Error ? e.message : "Could not create the request",
      });
    }
  };

  return {
    draft,
    services,
    folder,
    collides,
    kind,
    ready,
    cancel,
    setName,
    pick,
    submit,
  };
}
