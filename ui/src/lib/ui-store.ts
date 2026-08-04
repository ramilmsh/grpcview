// UI state only; server data lives in the react-query cache (workspace-query.ts).
import { create } from "zustand";
import type { Request_Response, Server } from "@grpcview/v1/workspace_pb";
import type { ItemWithPath } from "./format";
import { itemKey } from "./format";

export type ActiveView = "workspace" | "sources" | "scripts";

export interface OpenTab {
  key: string;
  name: string;
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
  openTabs: OpenTab[];
  activeKey: string | null;
  drafts: Record<string, Draft | undefined>;
  invokes: Record<string, InvokeState | undefined>;
  requestSubtab: RequestSubtab;
  responseSubtab: ResponseSubtab;

  treeExpanded: ReadonlySet<string>;
  treeSelection: readonly string[];
  treeFocused: string | null;

  selectedScript: string | null;
  scriptDrafts: Record<string, string | undefined>;
  scriptSubtab: ScriptSubtab;

  setView: (view: ActiveView) => void;
  openTab: (item: ItemWithPath) => void;
  closeTab: (key: string) => void;
  setActiveKey: (key: string | null) => void;
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
        const next = openTabs[idx] ?? openTabs[idx - 1] ?? null;
        activeKey = next ? next.key : null;
      }
      return { openTabs, activeKey };
    }),

  setActiveKey: (activeKey) => set({ activeKey }),

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
