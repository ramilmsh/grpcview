import { useEffect, useMemo, useRef } from "react";
import type { JsonObject } from "@bufbuild/protobuf";
import {
  useWorkspace,
  useRootItems,
  useWorkspaceMutations,
  useInvoke,
  WORKSPACE_NAME,
} from "@/lib/workspace-query";
import { useUIStore } from "@/lib/ui-store";
import {
  findByKey,
  objectToRows,
  resolveMethod,
  rowsToObject,
  type MetadataRow,
} from "@/lib/format";
import { Centered } from "@/components/ui/Centered";
import { MethodHeader } from "./MethodHeader";
import { RequestPane } from "./RequestPane";
import { ResponsePane } from "./ResponsePane";

const DEBOUNCE_MS = 400;

// RequestWorkspace is the active request area: it resolves the active request
// from the tree, owns the editor draft (seeded once, then authoritative), and
// wires debounced persistence + Invoke. The draft/response are keyed by itemKey
// so they survive tab switches (plan §6/§7).
export function RequestWorkspace() {
  const { workspace, services, reflection } = useWorkspace();
  const rootItems = useRootItems(workspace);
  const { updateRequest } = useWorkspaceMutations();
  const invokeMut = useInvoke();

  const activeKey = useUIStore((s) => s.activeKey);
  const draft = useUIStore((s) => (activeKey ? s.drafts[activeKey] : undefined));
  const invokeState = useUIStore((s) => (activeKey ? s.invokes[activeKey] : undefined));
  const seedDraft = useUIStore((s) => s.seedDraft);
  const setDraft = useUIStore((s) => s.setDraft);
  const setInvoke = useUIStore((s) => s.setInvoke);

  const activeItem = useMemo(() => findByKey(rootItems, activeKey), [rootItems, activeKey]);
  const request =
    activeItem?.item.content.case === "request" ? activeItem.item.content.value : null;

  // Seed the draft from the server Request once per request (idempotent).
  useEffect(() => {
    if (activeKey && request) {
      seedDraft(activeKey, {
        body: request.draftBody || "{}",
        metadataRows: objectToRows(request.draftMetadata),
      });
    }
  }, [activeKey, request, seedDraft]);

  // The method the request points at — one lookup feeds both the editor's JSON
  // schema and the "valid <type>" footer.
  const activeMethod = useMemo(
    () => (request ? resolveMethod(services, request.service, request.method) : undefined),
    [services, request]
  );

  // Debounce timers keyed by `${requestKey}:${slot}` so a pending save for one
  // request is never cancelled by scheduling a save for a different request
  // (which would silently drop the first request's unsaved edit).
  const timers = useRef<Record<string, number>>({});

  if (!activeItem || !request || !activeKey) {
    return <Centered>Select a request to edit and invoke.</Centered>;
  }

  const key = activeKey;
  const path = activeItem.path;
  const itemName = activeItem.item.name;
  const body = draft?.body ?? request.draftBody ?? "{}";
  const metadataRows = draft?.metadataRows ?? objectToRows(request.draftMetadata);

  // Debounced persistence, one timer per field so a body save and a metadata
  // save don't cancel each other.
  const scheduleSave = (
    slot: "body" | "meta",
    fields: { draftBody?: string; draftMetadata?: JsonObject }
  ) => {
    const timerKey = `${key}:${slot}`;
    window.clearTimeout(timers.current[timerKey]);
    timers.current[timerKey] = window.setTimeout(() => {
      updateRequest.mutate({ workspaceName: WORKSPACE_NAME, path, itemName, ...fields });
    }, DEBOUNCE_MS);
  };

  const onBodyChange = (v: string) => {
    setDraft(key, { body: v });
    scheduleSave("body", { draftBody: v });
  };

  const onMetadataChange = (rows: MetadataRow[]) => {
    setDraft(key, { metadataRows: rows });
    scheduleSave("meta", { draftMetadata: rowsToObject(rows) });
  };

  const onChangeMethod = (service: string, method: string) => {
    updateRequest.mutate({ workspaceName: WORKSPACE_NAME, path, itemName, service, method });
  };

  const onInvoke = () => {
    setInvoke(key, { loading: true });
    invokeMut.mutate(
      {
        workspaceName: WORKSPACE_NAME,
        path,
        itemName,
        service: request.service,
        method: request.method,
        body,
        metadata: rowsToObject(metadataRows),
      },
      {
        onSuccess: (res) => setInvoke(key, { response: res.response }),
        onError: (e) => setInvoke(key, { error: e instanceof Error ? e.message : String(e) }),
      }
    );
  };

  return (
    <div className="flex flex-col" style={{ flex: 1, minWidth: 0, minHeight: 0 }}>
      <MethodHeader
        request={request}
        services={services}
        reflection={reflection}
        invoking={!!invokeState?.loading}
        onChangeMethod={onChangeMethod}
        onInvoke={onInvoke}
      />
      <div className="flex" style={{ flex: 1, minHeight: 0 }}>
        <RequestPane
          schema={activeMethod?.input?.schema as object | undefined}
          body={body}
          onBodyChange={onBodyChange}
          metadataRows={metadataRows}
          onMetadataChange={onMetadataChange}
          currentMethod={{ service: request.service, method: request.method }}
          currentKey={key}
          inputTypeName={activeMethod?.input?.name}
        />
        <ResponsePane invoke={invokeState} />
      </div>
    </div>
  );
}
