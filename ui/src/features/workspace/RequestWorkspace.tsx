import { useEffect, useMemo, useRef } from "react";
import type { JsonObject } from "@bufbuild/protobuf";
import { ConnectError, Code } from "@connectrpc/connect";
import { BodyLanguage, ScriptKind } from "@grpcview/v1/workspace_pb";
import type { History } from "@grpcview/v1/workspace_pb";
import {
  useWorkspace,
  useRootItems,
  useWorkspaceMutations,
  useInvoke,
  useStreamingClient,
  useRefreshWorkspace,
  WORKSPACE_NAME,
} from "@/lib/workspace-query";
import { useUIStore } from "@/lib/ui-store";
import {
  findByKey,
  keyOf,
  methodKind,
  objectToRows,
  prettyBody,
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
  const streamClient = useStreamingClient();
  const refreshWorkspace = useRefreshWorkspace();

  const activeKey = useUIStore((s) => s.activeKey);
  const draft = useUIStore((s) => (activeKey ? s.drafts[activeKey] : undefined));
  const invokeState = useUIStore((s) => (activeKey ? s.invokes[activeKey] : undefined));
  const seedDraft = useUIStore((s) => s.seedDraft);
  const setDraft = useUIStore((s) => s.setDraft);
  const setInvoke = useUIStore((s) => s.setInvoke);
  const startStream = useUIStore((s) => s.startStream);
  const pushStreamMessage = useUIStore((s) => s.pushStreamMessage);
  const endStream = useUIStore((s) => s.endStream);
  const stopStream = useUIStore((s) => s.stopStream);
  const failStream = useUIStore((s) => s.failStream);
  const renameItem = useUIStore((s) => s.renameItem);

  const activeItem = useMemo(() => findByKey(rootItems, activeKey), [rootItems, activeKey]);
  const request =
    activeItem?.item.content.case === "request" ? activeItem.item.content.value : null;

  // Seed the draft from the server Request once per request (idempotent). The
  // compose list is seeded with the single draft body — its first entry is the
  // persisted primary; cs/bd requests grow it with ephemeral extras.
  useEffect(() => {
    if (activeKey && request) {
      const primary = request.draftBody || "{}";
      seedDraft(activeKey, {
        body: primary,
        metadataRows: objectToRows(request.draftMetadata),
        messages: [primary],
      });
    }
  }, [activeKey, request, seedDraft]);

  // The method the request points at — one lookup feeds the editor's JSON schema,
  // the "valid <type>" footer, and the method-kind branch (unary vs streaming).
  const activeMethod = useMemo(
    () => (request ? resolveMethod(services, request.service, request.method) : undefined),
    [services, request]
  );
  const kind = methodKind(activeMethod);

  // The workspace's saved GENERATOR names, threaded down to the Monaco body editor
  // for autocomplete + typing (ts-request-body-plan §T3). In TypeScript body mode the
  // backend injects each referenced generator as an ambient `globalThis.<name>` and
  // runs it, so the body calls it directly; the editor only needs the NAMES to declare
  // them ambiently. Memoized on `workspace?.scripts` for a stable array identity, so the
  // editor's ambient-decl effect re-runs only when the generator set actually changes.
  const generators = useMemo(
    () =>
      workspace?.scripts
        .filter((s) => s.kind === ScriptKind.GENERATOR)
        .map((s) => s.name) ?? [],
    [workspace?.scripts]
  );

  // Debounce timers keyed by `${requestKey}:${slot}` so a pending save for one
  // request is never cancelled by scheduling a save for a different request
  // (which would silently drop the first request's unsaved edit).
  const timers = useRef<Record<string, number>>({});

  // AbortControllers for in-flight streams, keyed by request key. Like `timers`,
  // this ref survives tab switches so a stream started on one tab can be stopped
  // after navigating away and back (plan §5).
  const aborters = useRef<Record<string, AbortController>>({});

  if (!activeItem || !request || !activeKey) {
    return <Centered>Select a request to edit and invoke.</Centered>;
  }

  const key = activeKey;
  const path = activeItem.path;
  const itemName = activeItem.item.name;
  const body = draft?.body ?? request.draftBody ?? "{}";
  const metadataRows = draft?.metadataRows ?? objectToRows(request.draftMetadata);
  // Compose list for cs/bd. The primary (index 0) is always the current body —
  // the single source of truth for the persisted message — with any ephemeral
  // extras appended, so the two can never drift.
  const messages = draft?.messages ? [body, ...draft.messages.slice(1)] : [body];

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

  // Compose-list edits (cs/bd). Only the primary (index 0) persists — it mirrors
  // the request's single draft_body via the same debounced body path; the extras
  // stay ephemeral in the draft.
  const onMessagesChange = (next: string[]) => {
    const primary = next[0] ?? "{}";
    setDraft(key, { messages: next, body: primary });
    scheduleSave("body", { draftBody: primary });
  };

  const onChangeMethod = (service: string, method: string) => {
    updateRequest.mutate({ workspaceName: WORKSPACE_NAME, path, itemName, service, method });
  };

  // Body-language toggle (JSON ⇄ TypeScript). A discrete edit like a method change:
  // persist immediately, then the re-seeded Get cache flows request.bodyLanguage back
  // down — the editor + the invoke payloads read it straight off `request`, so no
  // local draft copy is needed (ts-request-body-plan §T1/§4.6).
  const onBodyLanguageChange = (next: BodyLanguage) => {
    updateRequest.mutate({ workspaceName: WORKSPACE_NAME, path, itemName, bodyLanguage: next });
    // Seed a return-annotated template on JSON→TS when the body is trivial. TS
    // completions/errors only fire if the `RequestMessage` annotation is literally in the
    // buffer (an untyped `() => ({})` infers `{}` and offers nothing — plan §T2/§2). Only
    // for an empty/`{}` body so we never clobber a real JSON object the user is migrating.
    const trimmed = body.trim();
    if (next === BodyLanguage.TYPESCRIPT && (trimmed === "" || trimmed === "{}")) {
      onBodyChange("export default (): RequestMessage => ({\n  \n})");
    }
  };

  // Middleware attach/detach/reorder persists the whole ordered list immediately
  // (like a method change — a discrete edit, not a debounced buffer). The set-flag
  // replaces the list (empty clears all); the re-seeded Get cache flows the fresh
  // request.middleware back down, so the tab reads server state, no local copy.
  const onMiddlewareChange = (next: string[]) => {
    updateRequest.mutate({
      workspaceName: WORKSPACE_NAME,
      path,
      itemName,
      updateMiddleware: true,
      middleware: next,
    });
  };

  // Rename persists via UpdateRequest; on success we remap the client-side keyed
  // state (tab/draft/response) from the old itemKey to the new one, since itemKey
  // is name-derived (a failed rename, e.g. a name collision, leaves the UI as-is).
  const onRename = (nextName: string) => {
    const next = nextName.trim();
    if (!next || next === itemName) return;
    updateRequest.mutate(
      { workspaceName: WORKSPACE_NAME, path, itemName, name: next },
      { onSuccess: () => renameItem(key, keyOf(path, next), next) }
    );
  };

  // runInvoke fires the call with explicit body/metadata/messages so a re-run
  // from history can pass historical values directly (the draft state update is
  // async, so the current-draft closure would be stale). onInvoke passes the live
  // draft-derived values. Each completed run refreshes the workspace so the
  // just-persisted entry appears on the Timeline (history rides along on Get).
  const runInvoke = (b: string, rows: MetadataRow[], msgs: string[]) => {
    // Unary — mutation path.
    if (kind === "u") {
      setInvoke(key, { loading: true });
      invokeMut.mutate(
        {
          workspaceName: WORKSPACE_NAME,
          path,
          itemName,
          service: request.service,
          method: request.method,
          body: b,
          metadata: rowsToObject(rows),
          // Carry the editor's current toggle so a TS body evaluates on the server
          // (the invoke path reads this off the wire, not the saved Request).
          bodyLanguage: request.bodyLanguage,
        },
        {
          // The server persists history before returning, so refresh on success.
          onSuccess: (res) => {
            setInvoke(key, { response: res.response });
            refreshWorkspace();
          },
          onError: (e) => setInvoke(key, { error: e instanceof Error ? e.message : String(e) }),
        }
      );
      return;
    }

    // Streaming (ss/cs/bd) — one server-streaming RPC carrying every request
    // message up-front: the single body for server-streaming, the whole compose
    // list for client-streaming / bidi. `key` and the request fields are captured
    // here so a mid-stream tab switch keeps writing into the request the stream
    // was started for (the store actions patch by this captured key).
    const messagesToSend = kind === "ss" ? [b] : msgs;
    const req = {
      workspaceName: WORKSPACE_NAME,
      path,
      itemName,
      service: request.service,
      method: request.method,
      messages: messagesToSend,
      metadata: rowsToObject(rows),
      // Carry the editor's current toggle (mirrors the unary path) so a TS message
      // evaluates on the server.
      bodyLanguage: request.bodyLanguage,
    };

    aborters.current[key]?.abort(); // supersede any prior stream for this key
    const ac = new AbortController();
    aborters.current[key] = ac;
    startStream(key);

    void (async () => {
      try {
        for await (const frame of streamClient.invokeStreaming(req, { signal: ac.signal })) {
          if (frame.event.case === "message") {
            pushStreamMessage(key, { body: prettyBody(frame.event.value), at: Date.now() });
          } else if (frame.event.case === "result") {
            endStream(key, frame.event.value);
          }
        }
      } catch (e) {
        // Stop aborts the response stream, surfacing as a Canceled ConnectError —
        // a clean close: keep the received messages, no error banner. Anything
        // else is a real grpcview-internal failure.
        if (ac.signal.aborted || (e instanceof ConnectError && e.code === Code.Canceled)) {
          stopStream(key);
        } else {
          failStream(key, e instanceof Error ? e.message : String(e));
        }
      } finally {
        // Clear only if still ours — a superseding invoke may have replaced it.
        if (aborters.current[key] === ac) delete aborters.current[key];
        // The stream has closed, so the server handler returned and its history
        // entry is persisted — refresh so the Timeline picks it up.
        refreshWorkspace();
      }
    })();
  };

  const onInvoke = () => runInvoke(body, metadataRows, messages);

  // Stop the active request's in-flight stream (no-op if none).
  const onStop = () => aborters.current[key]?.abort();

  // historyDraft derives the editor draft a past run should repopulate: its
  // request body (bytes -> text) and metadata rows. Selecting a run loads it;
  // re-running loads it AND fires the call with those exact values.
  const historyDraft = (entry: History) => {
    const b =
      entry.request && entry.request.body.length > 0
        ? new TextDecoder().decode(entry.request.body)
        : "{}";
    const rows = objectToRows(entry.request?.metadata);
    return { b, rows };
  };
  const onSelectHistory = (entry: History) => {
    const { b, rows } = historyDraft(entry);
    setDraft(key, { body: b, metadataRows: rows, messages: [b] });
  };
  const onRerunHistory = (entry: History) => {
    const { b, rows } = historyDraft(entry);
    setDraft(key, { body: b, metadataRows: rows, messages: [b] });
    runInvoke(b, rows, [b]);
  };

  return (
    <div className="flex flex-col" style={{ flex: 1, minWidth: 0, minHeight: 0 }}>
      <MethodHeader
        request={request}
        services={services}
        kind={kind}
        reflection={reflection}
        invoking={!!invokeState?.loading || !!invokeState?.streaming}
        onChangeMethod={onChangeMethod}
        onRename={onRename}
        onInvoke={onInvoke}
      />
      <div className="flex" style={{ flex: 1, minHeight: 0 }}>
        <RequestPane
          schema={activeMethod?.input?.schema as object | undefined}
          kind={kind}
          body={body}
          onBodyChange={onBodyChange}
          messages={messages}
          onMessagesChange={onMessagesChange}
          metadataRows={metadataRows}
          onMetadataChange={onMetadataChange}
          middleware={request.middleware}
          onMiddlewareChange={onMiddlewareChange}
          currentMethod={{ service: request.service, method: request.method }}
          currentKey={key}
          inputTypeName={activeMethod?.input?.name}
          bodyLanguage={request.bodyLanguage}
          descriptorSet={workspace?.descriptorSet}
          inputPackage={activeMethod?.input?.package}
          inputFile={activeMethod?.input?.file}
          onBodyLanguageChange={onBodyLanguageChange}
          generators={generators}
        />
        <ResponsePane
          invoke={invokeState}
          kind={kind}
          onStop={onStop}
          history={request.history}
          onSelectHistory={onSelectHistory}
          onRerunHistory={onRerunHistory}
        />
      </div>
    </div>
  );
}
