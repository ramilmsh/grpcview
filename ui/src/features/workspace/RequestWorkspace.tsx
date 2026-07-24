import { useEffect, useMemo, useRef } from "react";
import { ConnectError, Code } from "@connectrpc/connect";
import { ScriptKind } from "@grpcview/v1/workspace_pb";
import type { History, Server } from "@grpcview/v1/workspace_pb";
import {
  useWorkspace,
  useRootItems,
  useWorkspaceMutations,
  useInvoke,
  useStreamingClient,
  useRefreshWorkspace,
  sourceForService,
  WORKSPACE_NAME,
} from "@/lib/workspace-query";
import { useUIStore } from "@/lib/ui-store";
import { findByKey, keyOf, methodKind, prettyBody, resolveMethod } from "@/lib/format";
import { Centered } from "@/components/ui/Centered";
import { MethodHeader } from "./MethodHeader";
import { RequestPane } from "./RequestPane";
import { ResponsePane } from "./ResponsePane";
import { migrateBodyToTs } from "./body-wrapper";
import { migrateMetadataToTs } from "./metadata-wrapper";
import type { GeneratorDef } from "./generator-libs";

const DEBOUNCE_MS = 400;

// RequestWorkspace is the active request area: it resolves the active request
// from the tree, owns the editor draft (seeded once, then authoritative), and
// wires debounced persistence + Invoke. The draft/response are keyed by itemKey
// so they survive tab switches (plan §6/§7).
export function RequestWorkspace() {
  const { workspace, services } = useWorkspace();
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

  // The reflection source backing THIS request — the origin of its service's schema
  // (Service.source), else the workspace's first reflection source. This is the live
  // default invoke target the header/target-bar display and the backend dials when the
  // request has no explicit target override, so a request against a method from the 2nd+
  // source no longer mis-defaults to the first (mirrors resolveTarget's server-side default).
  const requestSource = useMemo(
    () => (request ? sourceForService(workspace, request.service) : null),
    [workspace, request]
  );

  // Seed the draft from the server Request once per request (idempotent). The
  // compose list is seeded with the single draft body — its first entry is the
  // persisted primary; cs/bd requests grow it with ephemeral extras.
  useEffect(() => {
    if (activeKey && request) {
      // Every body is authored as TypeScript now, and the editor can only host a canonical
      // hidden-wrapper module — so migrate a legacy JSON / token / old-TS body to canonical on
      // first seed (idempotent; a canonical body passes through). Lazy: the migrated form is only
      // persisted once the user edits (onBodyChange), mirroring the old seed-on-toggle.
      const primary = migrateBodyToTs(request.draftBody || "{}");
      // Metadata is authored as a canonical TS module now. Seed from the persisted
      // draft_metadata_script if present; otherwise seed an empty canonical module.
      const metadata = request.draftMetadataScript || migrateMetadataToTs();
      seedDraft(activeKey, {
        body: primary,
        metadata,
        messages: [primary],
        // Per-request invoke target override; undefined follows the reflection source.
        target: request.target,
      });
    }
  }, [activeKey, request, seedDraft]);

  // The method the request points at — one lookup feeds the editor's T2 typed body (input
  // package/name/file), the "valid <type>" footer, and the method-kind branch (unary vs streaming).
  const activeMethod = useMemo(
    () => (request ? resolveMethod(services, request.service, request.method) : undefined),
    [services, request]
  );
  const kind = methodKind(activeMethod);

  // The workspace's saved GENERATORS (name + source), threaded down to the Monaco body + metadata
  // editors for autocomplete + typing (ts-request-body-plan §T3, §P5). In TypeScript body mode the
  // backend injects each referenced generator as an ambient `globalThis.<name>` and runs it, so the
  // body/metadata calls it directly; §P5 also carries each generator's SOURCE so the editor can
  // infer and surface its real signature (params + return), not just the name. Memoized on
  // `workspace?.scripts`: including source means the identity changes whenever any generator's
  // source changes (so the editors re-infer), but stays stable across unrelated re-renders — the
  // ambient-decl effect still re-runs only when the generator set actually changes.
  const generators = useMemo<GeneratorDef[]>(
    () =>
      workspace?.scripts
        .filter((s) => s.kind === ScriptKind.GENERATOR)
        .map((s) => ({ name: s.name, source: s.source })) ?? [],
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
  // Before the seed effect commits (first render), fall back to the migrated server body so the
  // editor's very first load is already canonical (its reload keys on currentKey, not on `body`).
  const body = draft?.body ?? migrateBodyToTs(request.draftBody || "{}");
  // Before the seed effect commits (first render), fall back to the persisted script or an
  // empty canonical module so the editor's very first load is already canonical.
  const metadata =
    draft?.metadata ?? (request.draftMetadataScript || migrateMetadataToTs());
  // Compose list for cs/bd. The primary (index 0) is always the current body —
  // the single source of truth for the persisted message — with any ephemeral
  // extras appended, so the two can never drift.
  const messages = draft?.messages ? [body, ...draft.messages.slice(1)] : [body];
  // The invoke target displayed + sent: the per-request override (draft, then the
  // persisted request.target before the seed effect commits) with a live fallback
  // to the reflection source handled downstream. Undefined here means "follow the
  // reflection source" — protobuf-es omits it, so the backend defaults it.
  const targetOverride = draft?.target ?? request.target;

  // Debounced persistence, one timer per field (distinct slot -> distinct timerKey)
  // so a body, metadata, and target save never cancel each other.
  const scheduleSave = (
    slot: "body" | "meta" | "target",
    fields: {
      draftBody?: string;
      draftMetadataScript?: string;
      updateTarget?: boolean;
      target?: Server;
    }
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

  // Metadata is a canonical TS module string now (like the body), persisted as
  // draft_metadata_script.
  const onMetadataChange = (v: string) => {
    setDraft(key, { metadata: v });
    scheduleSave("meta", { draftMetadataScript: v });
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

  // Editing the target bar sets a full Server override on the draft and debounces
  // its persistence (updateTarget set-flag; its own timer slot so it doesn't cancel
  // a pending body/meta save). Clearing back to the reflection default is a future
  // affordance — an edit always produces a concrete override.
  const onTargetChange = (t: Server) => {
    setDraft(key, { target: t });
    scheduleSave("target", { updateTarget: true, target: t });
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
  // draft-derived values. `md` is the canonical metadata TS module (sent as
  // metadataScript, evaluated backend-side). Each completed run refreshes the
  // workspace so the just-persisted entry appears on the Timeline.
  const runInvoke = (b: string, md: string, msgs: string[]) => {
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
          // Metadata is a canonical TS module too, sent as metadataScript for the server to
          // eval into the outgoing metadata (superseding the old metadata Struct, which we no
          // longer send for requests). Read off the wire, like body.
          metadataScript: md,
          // Per-request target override; undefined when none, which protobuf-es omits so the
          // backend defaults to the workspace's first reflection source (resolveTarget).
          target: targetOverride,
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
    // Migrate every outgoing message to a canonical TS module: the primary (index 0) is already
    // canonical (migrated on load), but the cs/bd compose extras are authored as raw JSON in the
    // plain-textarea MessagesTab, and a bare JSON object sent as TypeScript would misparse on the
    // last-expression path. migrateBodyToTs is idempotent, so the already-canonical primary is
    // untouched.
    const messagesToSend = (kind === "ss" ? [b] : msgs).map(migrateBodyToTs);
    const req = {
      workspaceName: WORKSPACE_NAME,
      path,
      itemName,
      service: request.service,
      method: request.method,
      messages: messagesToSend,
      // Metadata as a canonical TS module, sent as metadataScript (mirrors the unary path).
      metadataScript: md,
      // Per-request target override (mirrors the unary path); undefined → backend default.
      target: targetOverride,
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

  const onInvoke = () => runInvoke(body, metadata, messages);

  // Stop the active request's in-flight stream (no-op if none).
  const onStop = () => aborters.current[key]?.abort();

  // historyDraft derives the editor draft a past run should repopulate: its
  // request body (bytes -> text) and metadata. Selecting a run loads it;
  // re-running loads it AND fires the call with those exact values.
  const historyDraft = (entry: History) => {
    // History bodies are the bytes sent on that run (often legacy JSON). Migrate to canonical TS
    // so selecting/re-running loads a body the always-TS editor can host and the server can eval.
    const raw =
      entry.request && entry.request.body.length > 0
        ? new TextDecoder().decode(entry.request.body)
        : "{}";
    const b = migrateBodyToTs(raw);
    // History metadata is the resolved Struct that was sent; reconstruct a canonical metadata
    // module from it (each value → a string[] literal) so re-running sends those exact values.
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
    <div className="flex flex-col" style={{ flex: 1, minWidth: 0, minHeight: 0 }}>
      <MethodHeader
        request={request}
        services={services}
        kind={kind}
        reflection={requestSource}
        targetOverride={targetOverride}
        invoking={!!invokeState?.loading || !!invokeState?.streaming}
        onChangeMethod={onChangeMethod}
        onRename={onRename}
        onInvoke={onInvoke}
        onTargetChange={onTargetChange}
      />
      <div className="flex" style={{ flex: 1, minHeight: 0 }}>
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
