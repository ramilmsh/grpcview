// zustand holds UI state ONLY (plan §6): the active view, client-side open tabs,
// the active tab, in-progress editor drafts, and ephemeral Invoke results. Server
// data never lives here — that is the react-query cache (workspace-query.ts).
import { create } from "zustand";
import type { Request_Response } from "@grpcview/v1/workspace_pb";
import type { ItemWithPath, MetadataRow } from "./format";
import { itemKey } from "./format";

export type ActiveView = "workspace" | "sources";

// A client-side open request tab. Keyed by itemKey; name is a display copy (safe
// in Phase 1 since rename is unsupported). The live Request is resolved from the
// workspace tree by key so edits/updates stay in sync.
export interface OpenTab {
  key: string;
  name: string;
}

// The working editor state for a request while it is open. Seeded from the
// server Request on first open, then authoritative until the tab closes.
export interface Draft {
  body: string;
  metadataRows: MetadataRow[];
}

// Ephemeral result of the last Invoke for a request. Survives tab switches; not
// persisted (history is not wired — plan §2).
export interface InvokeState {
  loading?: boolean;
  response?: Request_Response;
  error?: string;
}

export type RequestSubtab = "message" | "metadata";
export type ResponseSubtab = "messages" | "metadata";

interface UIState {
  activeView: ActiveView;
  openTabs: OpenTab[];
  activeKey: string | null;
  drafts: Record<string, Draft | undefined>;
  invokes: Record<string, InvokeState | undefined>;
  requestSubtab: RequestSubtab;
  responseSubtab: ResponseSubtab;

  setView: (view: ActiveView) => void;
  openTab: (item: ItemWithPath) => void;
  closeTab: (key: string) => void;
  setActiveKey: (key: string | null) => void;

  seedDraft: (key: string, draft: Draft) => void; // only if absent
  setDraft: (key: string, patch: Partial<Draft>) => void;

  setInvoke: (key: string, state: InvokeState) => void;

  setRequestSubtab: (tab: RequestSubtab) => void;
  setResponseSubtab: (tab: ResponseSubtab) => void;
}

export const useUIStore = create<UIState>()((set) => ({
  activeView: "workspace",
  openTabs: [],
  activeKey: null,
  drafts: {},
  invokes: {},
  requestSubtab: "message",
  responseSubtab: "messages",

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

  seedDraft: (key, draft) =>
    set((s) => (s.drafts[key] ? {} : { drafts: { ...s.drafts, [key]: draft } })),

  setDraft: (key, patch) =>
    set((s) => {
      const prev = s.drafts[key] ?? { body: "{}", metadataRows: [] };
      return { drafts: { ...s.drafts, [key]: { ...prev, ...patch } } };
    }),

  setInvoke: (key, state) =>
    set((s) => ({ invokes: { ...s.invokes, [key]: state } })),

  setRequestSubtab: (requestSubtab) => set({ requestSubtab }),
  setResponseSubtab: (responseSubtab) => set({ responseSubtab }),
}));
