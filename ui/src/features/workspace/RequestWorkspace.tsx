import { useEffect, useMemo, useRef, useState } from "react";
import { ConnectError, Code } from "@connectrpc/connect";
import type { History, Server } from "@grpcview/v1/workspace_pb";
import {
  useActiveWorkspace,
  useRootItems,
  useWorkspaceMutations,
  useInvoke,
  useStreamingClient,
  useRefreshWorkspace,
  sourceForService,
  schemaSourceFor,
} from "@/lib/workspace-query";
import { useUIStore } from "@/lib/ui-store";
import { findByKey, methodKind, prettyBody, resolveMethod } from "@/lib/format";
import { Centered } from "@/components/ui/Centered";
import { MethodHeader } from "./MethodHeader";
import { RequestPane } from "./RequestPane";
import { ResponsePane } from "./ResponsePane";
import { TypesModal } from "./TypesModal";
import { migrateBodyToTs } from "./body-wrapper";
import {
  defaultMetadataModule,
  hostMetadataScript,
  migrateMetadataToTs,
} from "./metadata-wrapper";

const DEBOUNCE_MS = 400;

export function RequestWorkspace() {
  const {
    collection: activeCollection,
    workspace,
    services,
  } = useActiveWorkspace();
  // Non-null everywhere this view renders (App gates on the collection listing).
  const collection = activeCollection ?? "";
  const rootItems = useRootItems(workspace);
  const { updateRequest } = useWorkspaceMutations();
  const invokeMut = useInvoke();
  const streamClient = useStreamingClient();
  const refreshWorkspace = useRefreshWorkspace(activeCollection);

  const activeKey = useUIStore((s) => s.activeKey);
  const draft = useUIStore((s) =>
    activeKey ? s.drafts[activeKey] : undefined,
  );
  const invokeState = useUIStore((s) =>
    activeKey ? s.invokes[activeKey] : undefined,
  );
  const seedDraft = useUIStore((s) => s.seedDraft);
  const setDraft = useUIStore((s) => s.setDraft);
  const setInvoke = useUIStore((s) => s.setInvoke);
  const startStream = useUIStore((s) => s.startStream);
  const pushStreamMessage = useUIStore((s) => s.pushStreamMessage);
  const endStream = useUIStore((s) => s.endStream);
  const stopStream = useUIStore((s) => s.stopStream);
  const failStream = useUIStore((s) => s.failStream);
  const [typesOpen, setTypesOpen] = useState(false);

  const activeItem = useMemo(
    () => findByKey(rootItems, activeKey),
    [rootItems, activeKey],
  );
  const request =
    activeItem?.item.content.case === "request"
      ? activeItem.item.content.value
      : null;

  // The live default invoke target: the origin of this service's schema, else the
  // workspace's first reflection source (mirrors resolveTarget server-side).
  const requestSource = useMemo(
    () => (request ? sourceForService(workspace, request.service) : null),
    [workspace, request],
  );

  // Where the SCHEMA came from — a different question from where the request is sent.
  const schemaSource = useMemo(
    () => (request ? schemaSourceFor(workspace, request.service) : null),
    [workspace, request],
  );

  useEffect(() => {
    if (activeKey && request) {
      // Migrate a legacy JSON / token / old-TS body to the canonical hidden-wrapper
      // module the editor can host. Idempotent; persisted lazily, on the next edit.
      const primary = migrateBodyToTs(request.draftBody || "{}");
      const metadata =
        hostMetadataScript(request.draftMetadataScript) ||
        defaultMetadataModule();
      seedDraft(activeKey, {
        body: primary,
        metadata,
        messages: [primary],
        target: request.target,
      });
    }
  }, [activeKey, request, seedDraft]);

  const activeMethod = useMemo(
    () =>
      request
        ? resolveMethod(services, request.service, request.method)
        : undefined,
    [services, request],
  );
  const kind = methodKind(activeMethod);

  // Keyed by `${requestKey}:${slot}` so a pending save is never cancelled by a save
  // scheduled for a different request or field.
  const timers = useRef<Record<string, number>>({});

  // Survives tab switches, so a stream started on one tab can be stopped later.
  const aborters = useRef<Record<string, AbortController>>({});

  if (!activeItem || !request || !activeKey) {
    return <Centered>Select a request to edit and invoke.</Centered>;
  }

  const key = activeKey;
  const path = activeItem.path;
  const itemName = activeItem.item.name;
  // Fallbacks cover the first render, before the seed effect commits.
  const body = draft?.body ?? migrateBodyToTs(request.draftBody || "{}");
  const metadata =
    draft?.metadata ??
    (hostMetadataScript(request.draftMetadataScript) ||
      defaultMetadataModule());
  // Index 0 is always the current body, so the persisted message can't drift from it.
  const messages = draft?.messages
    ? [body, ...draft.messages.slice(1)]
    : [body];
  // Undefined means "follow the reflection source" — protobuf-es omits it and the
  // backend defaults it.
  const targetOverride = draft?.target ?? request.target;

  const scheduleSave = (
    slot: "body" | "meta" | "target",
    fields: {
      draftBody?: string;
      draftMetadataScript?: string;
      updateTarget?: boolean;
      target?: Server;
    },
  ) => {
    const timerKey = `${key}:${slot}`;
    window.clearTimeout(timers.current[timerKey]);
    timers.current[timerKey] = window.setTimeout(() => {
      updateRequest.mutate({ collection, path, itemName, ...fields });
    }, DEBOUNCE_MS);
  };

  const onBodyChange = (v: string) => {
    setDraft(key, { body: v });
    scheduleSave("body", { draftBody: v });
  };

  const onMetadataChange = (v: string) => {
    setDraft(key, { metadata: v });
    scheduleSave("meta", { draftMetadataScript: v });
  };

  // Only the primary (index 0) persists; the cs/bd extras stay ephemeral.
  const onMessagesChange = (next: string[]) => {
    const primary = next[0] ?? "{}";
    setDraft(key, { messages: next, body: primary });
    scheduleSave("body", { draftBody: primary });
  };

  const onChangeMethod = (service: string, method: string) => {
    updateRequest.mutate({ collection, path, itemName, service, method });
  };

  const onTargetChange = (t: Server) => {
    setDraft(key, { target: t });
    scheduleSave("target", { updateTarget: true, target: t });
  };

  // updateMiddleware replaces the whole list (empty clears it); the re-seeded Get
  // cache flows the fresh request.middleware back down, so there is no local copy.
  const onMiddlewareChange = (next: string[]) => {
    updateRequest.mutate({
      collection,
      path,
      itemName,
      updateMiddleware: true,
      middleware: next,
    });
  };

  const onRename = (nextName: string) => {
    const next = nextName.trim();
    if (!next || next === itemName) return;
    // No key remap: keys are slug-based, and a rename leaves the slug alone.
    updateRequest.mutate({ collection, path, itemName, name: next });
  };

  // Takes body/metadata/messages explicitly so a history re-run can pass historical
  // values (setDraft is async, so a current-draft closure would be stale).
  const runInvoke = (b: string, md: string, msgs: string[]) => {
    if (kind === "u") {
      setInvoke(key, { loading: true });
      invokeMut.mutate(
        {
          spec: {
            collection,
            path,
            itemName,
            service: request.service,
            method: request.method,
            metadataScript: md,
            target: targetOverride,
          },
          body: b,
        },
        {
          // The server persists history before returning, so refresh on success.
          onSuccess: (res) => {
            setInvoke(key, { response: res.response });
            refreshWorkspace();
          },
          onError: (e) =>
            setInvoke(key, {
              error: e instanceof Error ? e.message : String(e),
            }),
        },
      );
      return;
    }

    // `key` and the request fields are captured here so a mid-stream tab switch keeps
    // writing into the request the stream was started for.
    // The cs/bd compose extras are authored as raw JSON, which would misparse on the
    // last-expression path; migrateBodyToTs is idempotent for the primary.
    const messagesToSend = (kind === "ss" ? [b] : msgs).map(migrateBodyToTs);
    const req = {
      spec: {
        collection,
        path,
        itemName,
        service: request.service,
        method: request.method,
        metadataScript: md,
        target: targetOverride,
      },
      messages: messagesToSend,
    };

    aborters.current[key]?.abort(); // supersede any prior stream for this key
    const ac = new AbortController();
    aborters.current[key] = ac;
    startStream(key);

    void (async () => {
      try {
        for await (const frame of streamClient.invokeStreaming(req, {
          signal: ac.signal,
        })) {
          if (frame.event.case === "message") {
            pushStreamMessage(key, {
              body: prettyBody(frame.event.value),
              at: Date.now(),
            });
          } else if (frame.event.case === "result") {
            endStream(key, frame.event.value);
          }
        }
      } catch (e) {
        // Stop surfaces as a Canceled ConnectError: a clean close, not a failure.
        if (
          ac.signal.aborted ||
          (e instanceof ConnectError && e.code === Code.Canceled)
        ) {
          stopStream(key);
        } else {
          failStream(key, e instanceof Error ? e.message : String(e));
        }
      } finally {
        // Clear only if still ours — a superseding invoke may have replaced it.
        if (aborters.current[key] === ac) delete aborters.current[key];
        refreshWorkspace();
      }
    })();
  };

  const onInvoke = () => runInvoke(body, metadata, messages);

  const onStop = () => aborters.current[key]?.abort();

  const historyDraft = (entry: History) => {
    const raw =
      entry.request && entry.request.body.length > 0
        ? new TextDecoder().decode(entry.request.body)
        : "{}";
    const b = migrateBodyToTs(raw);
    const md = migrateMetadataToTs(entry.request?.metadata);
    return { b, md };
  };
  const onSelectHistory = (entry: History) => {
    const { b, md } = historyDraft(entry);
    setDraft(key, { body: b, metadata: md, messages: [b] });
  };
  const onRerunHistory = (entry: History) => {
    const { b, md } = historyDraft(entry);
    setDraft(key, { body: b, metadata: md, messages: [b] });
    runInvoke(b, md, [b]);
  };

  return (
    <div
      className="flex flex-col"
      style={{ flex: 1, minWidth: 0, minHeight: 0 }}
    >
      <MethodHeader
        request={request}
        services={services}
        kind={kind}
        reflection={requestSource}
        schemaSource={schemaSource}
        targetOverride={targetOverride}
        invoking={!!invokeState?.loading || !!invokeState?.streaming}
        onChangeMethod={onChangeMethod}
        onRename={onRename}
        onInvoke={onInvoke}
        onTargetChange={onTargetChange}
        onShowTypes={() => setTypesOpen(true)}
      />
      <TypesModal
        open={typesOpen}
        onClose={() => setTypesOpen(false)}
        descriptorSet={workspace?.descriptorSet}
        input={activeMethod?.input}
        output={activeMethod?.output}
      />
      <div className="flex" style={{ flex: 1, minWidth: 0, minHeight: 0 }}>
        <RequestPane
          kind={kind}
          body={body}
          onBodyChange={onBodyChange}
          messages={messages}
          onMessagesChange={onMessagesChange}
          metadata={metadata}
          onMetadataChange={onMetadataChange}
          middleware={request.middleware}
          onMiddlewareChange={onMiddlewareChange}
          currentKey={key}
          inputTypeName={activeMethod?.input?.name}
          descriptorSet={workspace?.descriptorSet}
          inputPackage={activeMethod?.input?.package}
          inputFile={activeMethod?.input?.file}
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
