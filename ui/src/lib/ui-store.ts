// UI state only; server data lives in the react-query cache (workspace-query.ts).
import { create } from "zustand";
import type { Request_Response, Server } from "@grpcview/v1/workspace_pb";
import type { ItemWithPath } from "./format";
import { itemKey } from "./format";

export type ActiveView = "workspace" | "sources" | "scripts";

// The user's explicit collection choice, persisted so a reload restores it. Read
// through resolveActiveCollection (active-collection.ts), which drops it if the
// workspace no longer lists it. Guarded because //ui:test runs these modules under
// node, where there is no localStorage.
const COLLECTION_STORAGE_KEY = "grpcview.collection";

const readStoredCollection = (): string | null =>
  typeof localStorage === "undefined" ? null : localStorage.getItem(COLLECTION_STORAGE_KEY);

const writeStoredCollection = (id: string): void => {
  if (typeof localStorage !== "undefined") localStorage.setItem(COLLECTION_STORAGE_KEY, id);
};

export interface OpenTab {
  key: string;
  name: string;
  // The collection this request lives in, carried alongside the key rather than parsed
  // out of it: a collection id may itself contain slashes ("services/payments/requests"),
  // so key.split("/")[0] is not the collection and nothing may pretend otherwise.
  collection: string;
}

export interface Draft {
  body: string;
  metadata: string;
  // messages[0] mirrors `body`; messages[1..] are ephemeral extras. Client-streaming/bidi only.
  messages?: string[];
  // Undefined = follow the workspace's first reflection source.
  target?: Server;
}

export interface StreamMessage {
  body: string;
  at: number; // ms epoch
}

export interface InvokeState {
  loading?: boolean;
  streaming?: boolean;
  messages?: StreamMessage[];
  response?: Request_Response;
  error?: string;
}

export type RequestSubtab = "message" | "metadata" | "middleware";
export type ResponseSubtab = "messages" | "metadata" | "history";
export type ScriptSubtab = "code" | "deps" | "caps";

interface UIState {
  activeView: ActiveView;
  // The collection the scoped views address, or null when nothing has been chosen yet.
  // Every connect-query key is built from the id this resolves to, so switching
  // collections is state, not a reload.
  activeCollection: string | null;
  openTabs: OpenTab[];
  activeKey: string | null;
  drafts: Record<string, Draft | undefined>;
  invokes: Record<string, InvokeState | undefined>;
  requestSubtab: RequestSubtab;
  responseSubtab: ResponseSubtab;

  treeExpanded: ReadonlySet<string>;
  treeSelection: readonly string[];
  treeFocused: string | null;

  // Keyed by bare script name, so NOT yet collection-scoped — a later slice fixes that.
  selectedScript: string | null;
  scriptDrafts: Record<string, string | undefined>;
  scriptSubtab: ScriptSubtab;

  setView: (view: ActiveView) => void;
  setActiveCollection: (id: string) => void;
  openTab: (item: ItemWithPath) => void;
  closeTab: (key: string) => void;
  // `collection` is optional only because a caller that has no tab in hand (there is
  // one: clearing to null) cannot supply it. Pass it whenever it is known — that is
  // what stops anyone parsing a collection out of `key`.
  setActiveKey: (key: string | null, collection?: string) => void;
  // Remaps every slug-keyed slice after a real MOVE (the only thing that changes a
  // key). A move never renames, so `OpenTab.name` needs no fixing up here.
  moveSubtree: (oldKey: string, newKey: string) => void;

  setTreeExpanded: (next: ReadonlySet<string>) => void;
  setTreeSelection: (next: readonly string[]) => void;
  setTreeFocused: (next: string | null) => void;

  seedDraft: (key: string, draft: Draft) => void;
  setDraft: (key: string, patch: Partial<Draft>) => void;

  setInvoke: (key: string, state: InvokeState) => void;

  startStream: (key: string) => void;
  pushStreamMessage: (key: string, msg: StreamMessage) => void;
  endStream: (key: string, response: Request_Response) => void;
  stopStream: (key: string) => void;
  failStream: (key: string, error: string) => void;

  setRequestSubtab: (tab: RequestSubtab) => void;
  setResponseSubtab: (tab: ResponseSubtab) => void;

  selectScript: (name: string | null) => void;
  setScriptSubtab: (tab: ScriptSubtab) => void;
  seedScriptDraft: (name: string, source: string) => void;
  setScriptDraft: (name: string, source: string) => void;
  renameScript: (oldName: string, newName: string) => void;
  forgetScript: (name: string) => void;
}

export const useUIStore = create<UIState>()((set) => ({
  activeView: "workspace",
  activeCollection: readStoredCollection(),
  openTabs: [],
  activeKey: null,
  drafts: {},
  invokes: {},
  requestSubtab: "message",
  responseSubtab: "messages",
  selectedScript: null,
  scriptDrafts: {},
  scriptSubtab: "code",

  treeExpanded: new Set(),
  treeSelection: [],
  treeFocused: null,

  setView: (activeView) => set({ activeView }),

  setActiveCollection: (id) => {
    writeStoredCollection(id);
    set({ activeCollection: id });
  },

  // Opening a request in another collection makes that collection the active one: every
  // keyed slice is already collection-prefixed (itemKey), so nothing has to be cleared.
  openTab: (item) => {
    const key = itemKey(item);
    writeStoredCollection(item.collection);
    set((s) => {
      const exists = s.openTabs.some((t) => t.key === key);
      const tab: OpenTab = { key, name: item.item.name, collection: item.collection };
      return {
        openTabs: exists ? s.openTabs : [...s.openTabs, tab],
        activeKey: key,
        activeCollection: item.collection,
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
        const next = openTabs[idx] ?? openTabs[idx - 1] ?? null;
        activeKey = next ? next.key : null;
      }
      return { openTabs, activeKey };
    }),

  setActiveKey: (activeKey, collection) => {
    if (collection === undefined) {
      set({ activeKey });
      return;
    }
    writeStoredCollection(collection);
    set({ activeKey, activeCollection: collection });
  },

  moveSubtree: (oldKey, newKey) =>
    set((s) => {
      if (oldKey === newKey) return {};
      const prefix = `${oldKey}/`;
      const remap = (key: string): string | null => {
        if (key === oldKey) return newKey;
        // The trailing "/" (itemKey's slug separator) is what stops a sibling slug
        // "foo2" being swept up by a move of "foo".
        if (key.startsWith(prefix)) return newKey + key.slice(oldKey.length);
        return null;
      };
      // Each helper returns the identical reference when nothing changed, or every
      // consumer of untouched state re-renders.
      const rekey = <T,>(
        m: Record<string, T | undefined>
      ): Record<string, T | undefined> => {
        let changed = false;
        const out: Record<string, T | undefined> = {};
        for (const [key, value] of Object.entries(m)) {
          const to = remap(key);
          if (to === null) out[key] = value;
          else {
            changed = true;
            out[to] = value;
          }
        }
        return changed ? out : m;
      };
      const rekeySet = (ids: ReadonlySet<string>): ReadonlySet<string> => {
        let changed = false;
        const next = new Set<string>();
        for (const id of ids) {
          const to = remap(id);
          if (to === null) next.add(id);
          else {
            changed = true;
            next.add(to);
          }
        }
        return changed ? next : ids;
      };
      const rekeyOne = (key: string | null): string | null =>
        key === null ? null : remap(key) ?? key;

      let tabsChanged = false;
      const openTabs = s.openTabs.map((t) => {
        const to = remap(t.key);
        if (to === null) return t;
        tabsChanged = true;
        return { ...t, key: to };
      });

      let selectionChanged = false;
      const treeSelection = s.treeSelection.map((id) => {
        const to = remap(id);
        if (to === null) return id;
        selectionChanged = true;
        return to;
      });

      return {
        openTabs: tabsChanged ? openTabs : s.openTabs,
        activeKey: rekeyOne(s.activeKey),
        drafts: rekey(s.drafts),
        invokes: rekey(s.invokes),
        treeSelection: selectionChanged ? treeSelection : s.treeSelection,
        treeFocused: rekeyOne(s.treeFocused),
        treeExpanded: rekeySet(s.treeExpanded),
      };
    }),

  setTreeExpanded: (treeExpanded) => set({ treeExpanded }),
  setTreeSelection: (treeSelection) => set({ treeSelection }),
  setTreeFocused: (treeFocused) => set({ treeFocused }),

  seedDraft: (key, draft) =>
    set((s) => (s.drafts[key] ? {} : { drafts: { ...s.drafts, [key]: draft } })),

  setDraft: (key, patch) =>
    set((s) => {
      // The backend reads a blank metadata script as "no script".
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
