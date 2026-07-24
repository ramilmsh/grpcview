// zustand holds UI state ONLY (plan §6): the active view, client-side open tabs,
// the active tab, in-progress editor drafts, and ephemeral Invoke results. Server
// data never lives here — that is the react-query cache (workspace-query.ts).
import { create } from "zustand";
import type { Request_Response, Server } from "@grpcview/v1/workspace_pb";
import type { ItemWithPath } from "./format";
import { itemKey } from "./format";

export type ActiveView = "workspace" | "sources" | "scripts";

// A client-side open request tab. Keyed by itemKey; name is a display copy kept
// in sync by renameItem on a successful rename. The live Request is resolved from
// the workspace tree by key so edits/updates stay in sync.
export interface OpenTab {
  key: string;
  name: string;
}

// The working editor state for a request while it is open. Seeded from the
// server Request on first open, then authoritative until the tab closes.
export interface Draft {
  body: string;
  // The request metadata as a canonical hidden-wrapper TS module string (the
  // metadata editor's model text / persisted draft_metadata_script / invoke
  // payload — all the same string), mirroring `body`.
  metadata: string;
  // Compose list for client-streaming / bidi requests. messages[0] mirrors `body`
  // (the persisted primary — see RequestWorkspace); messages[1..] are ephemeral
  // extras the user adds before sending. Unused by unary / server-streaming.
  messages?: string[];
  // Per-request invoke target override (host:port + TLS). Undefined = "follow the
  // workspace's first reflection source" (the effective default). Seeded from the
  // persisted request.target, set when the user edits the target bar, and sent as
  // InvokeRequest.target on invoke (undefined → backend defaults to reflection).
  target?: Server;
}

// One received streaming payload, kept for a response card: body is the pretty
// JSON, at is the arrival time (ms epoch) shown right-aligned on the card.
export interface StreamMessage {
  body: string;
  at: number;
}

// Ephemeral result of the last Invoke for a request. Survives tab switches; not
// persisted (history is not wired — plan §2). Serves BOTH the unary path
// (loading + response/error) and the streaming path (streaming + messages, with
// response holding the terminal result once the stream closes).
export interface InvokeState {
  loading?: boolean;
  streaming?: boolean;
  messages?: StreamMessage[];
  response?: Request_Response;
  error?: string;
}

export type RequestSubtab = "message" | "metadata" | "middleware";
export type ResponseSubtab = "messages" | "metadata" | "history";
// The Scripts view's active detail subtab (plan §S1: Code / Dependencies /
// Capabilities). Dependencies + Capabilities are the sandboxed empty states.
export type ScriptSubtab = "code" | "deps" | "caps";

interface UIState {
  activeView: ActiveView;
  openTabs: OpenTab[];
  activeKey: string | null;
  drafts: Record<string, Draft | undefined>;
  invokes: Record<string, InvokeState | undefined>;
  requestSubtab: RequestSubtab;
  responseSubtab: ResponseSubtab;

  // Scripts view. Scripts themselves are server data (ride the Get snapshot); only
  // the selection, per-script editor buffers, and the active detail subtab are UI
  // state. scriptDrafts is keyed by the script's (unique) name and seeded from the
  // server Script on first open, then authoritative — mirroring `drafts` above.
  selectedScript: string | null;
  scriptDrafts: Record<string, string | undefined>;
  scriptSubtab: ScriptSubtab;

  setView: (view: ActiveView) => void;
  openTab: (item: ItemWithPath) => void;
  closeTab: (key: string) => void;
  setActiveKey: (key: string | null) => void;
  renameItem: (oldKey: string, newKey: string, newName: string) => void;

  seedDraft: (key: string, draft: Draft) => void; // only if absent
  setDraft: (key: string, patch: Partial<Draft>) => void;

  setInvoke: (key: string, state: InvokeState) => void;

  // Streaming actions. Each patches the keyed InvokeState atomically so the async
  // invoke loop appends to (never replaces) the accumulated messages, even across
  // tab switches. startStream resets, pushStreamMessage appends, endStream sets
  // the terminal result, stopStream marks a clean close (abort), failStream sets
  // an internal error — the last three keep prior messages via a spread.
  startStream: (key: string) => void;
  pushStreamMessage: (key: string, msg: StreamMessage) => void;
  endStream: (key: string, response: Request_Response) => void;
  stopStream: (key: string) => void;
  failStream: (key: string, error: string) => void;

  setRequestSubtab: (tab: RequestSubtab) => void;
  setResponseSubtab: (tab: ResponseSubtab) => void;

  selectScript: (name: string | null) => void;
  setScriptSubtab: (tab: ScriptSubtab) => void;
  seedScriptDraft: (name: string, source: string) => void; // only if absent
  setScriptDraft: (name: string, source: string) => void;
  // A script's identity is name-derived (like a request): a rename remaps the
  // selection + open draft from the old name to the new one so they follow it.
  renameScript: (oldName: string, newName: string) => void;
  // Drop a deleted script's draft and clear the selection if it was selected.
  forgetScript: (name: string) => void;
}

export const useUIStore = create<UIState>()((set) => ({
  activeView: "workspace",
  openTabs: [],
  activeKey: null,
  drafts: {},
  invokes: {},
  requestSubtab: "message",
  responseSubtab: "messages",
  selectedScript: null,
  scriptDrafts: {},
  scriptSubtab: "code",

  setView: (activeView) => set({ activeView }),

  openTab: (item) => {
    const key = itemKey(item);
    set((s) => {
      const exists = s.openTabs.some((t) => t.key === key);
      const tab: OpenTab = { key, name: item.item.name };
      return {
        openTabs: exists ? s.openTabs : [...s.openTabs, tab],
        activeKey: key,
        activeView: "workspace",
      };
    });
  },

  closeTab: (key) =>
    set((s) => {
      const idx = s.openTabs.findIndex((t) => t.key === key);
      const openTabs = s.openTabs.filter((t) => t.key !== key);
      let activeKey = s.activeKey;
      if (s.activeKey === key) {
        // fall back to the neighbour that took its place (or the new last tab)
        const next = openTabs[idx] ?? openTabs[idx - 1] ?? null;
        activeKey = next ? next.key : null;
      }
      return { openTabs, activeKey };
    }),

  setActiveKey: (activeKey) => set({ activeKey }),

  // renameItem remaps all name-derived keyed state from oldKey to newKey after a
  // successful rename, so the open tab, its draft, and its last response follow
  // the new name instead of detaching (itemKey is name-derived — see format.ts).
  renameItem: (oldKey, newKey, newName) =>
    set((s) => {
      if (oldKey === newKey) return {};
      const rekey = <T,>(
        m: Record<string, T | undefined>
      ): Record<string, T | undefined> => {
        if (!(oldKey in m)) return m;
        const { [oldKey]: moved, ...rest } = m;
        return moved === undefined ? rest : { ...rest, [newKey]: moved };
      };
      return {
        openTabs: s.openTabs.map((t) =>
          t.key === oldKey ? { key: newKey, name: newName } : t
        ),
        activeKey: s.activeKey === oldKey ? newKey : s.activeKey,
        drafts: rekey(s.drafts),
        invokes: rekey(s.invokes),
      };
    }),

  seedDraft: (key, draft) =>
    set((s) => (s.drafts[key] ? {} : { drafts: { ...s.drafts, [key]: draft } })),

  setDraft: (key, patch) =>
    set((s) => {
      // Fallback for the pathological "setDraft before seedDraft" case; seedDraft always runs
      // first in practice. An empty metadata string is the "unset" sentinel (backend treats a
      // blank metadata_script as "no script").
      const prev = s.drafts[key] ?? { body: "{}", metadata: "" };
      return { drafts: { ...s.drafts, [key]: { ...prev, ...patch } } };
    }),

  setInvoke: (key, state) =>
    set((s) => ({ invokes: { ...s.invokes, [key]: state } })),

  startStream: (key) =>
    set((s) => ({
      invokes: { ...s.invokes, [key]: { streaming: true, messages: [] } },
    })),

  pushStreamMessage: (key, msg) =>
    set((s) => {
      const prev = s.invokes[key];
      return {
        invokes: {
          ...s.invokes,
          [key]: { ...prev, streaming: true, messages: [...(prev?.messages ?? []), msg] },
        },
      };
    }),

  endStream: (key, response) =>
    set((s) => ({
      invokes: { ...s.invokes, [key]: { ...s.invokes[key], streaming: false, response } },
    })),

  stopStream: (key) =>
    set((s) => ({
      invokes: { ...s.invokes, [key]: { ...s.invokes[key], streaming: false } },
    })),

  failStream: (key, error) =>
    set((s) => ({
      invokes: { ...s.invokes, [key]: { ...s.invokes[key], streaming: false, error } },
    })),

  setRequestSubtab: (requestSubtab) => set({ requestSubtab }),
  setResponseSubtab: (responseSubtab) => set({ responseSubtab }),

  selectScript: (selectedScript) => set({ selectedScript }),
  setScriptSubtab: (scriptSubtab) => set({ scriptSubtab }),

  seedScriptDraft: (name, source) =>
    set((s) =>
      s.scriptDrafts[name] !== undefined
        ? {}
        : { scriptDrafts: { ...s.scriptDrafts, [name]: source } }
    ),

  setScriptDraft: (name, source) =>
    set((s) => ({ scriptDrafts: { ...s.scriptDrafts, [name]: source } })),

  renameScript: (oldName, newName) =>
    set((s) => {
      if (oldName === newName) return {};
      const scriptDrafts = { ...s.scriptDrafts };
      if (oldName in scriptDrafts) {
        const moved = scriptDrafts[oldName];
        delete scriptDrafts[oldName];
        if (moved !== undefined) scriptDrafts[newName] = moved;
      }
      return {
        selectedScript: s.selectedScript === oldName ? newName : s.selectedScript,
        scriptDrafts,
      };
    }),

  forgetScript: (name) =>
    set((s) => {
      const scriptDrafts = { ...s.scriptDrafts };
      delete scriptDrafts[name];
      return {
        scriptDrafts,
        selectedScript: s.selectedScript === name ? null : s.selectedScript,
      };
    }),
}));
