// zustand holds UI state ONLY (plan §6): the active view, client-side open tabs,
// the active tab, in-progress editor drafts, and ephemeral Invoke results. Server
// data never lives here — that is the react-query cache (workspace-query.ts).
import { create } from "zustand";
import type { Request_Response, Server } from "@grpcview/v1/workspace_pb";
import type { ItemWithPath } from "./format";
import { itemKey } from "./format";

export type ActiveView = "workspace" | "sources" | "scripts";

// A client-side open request tab. Keyed by itemKey; name is a display copy kept
// in sync by moveSubtree on a successful rename. The live Request is resolved from
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

  // components/tree/'s <Tree> requires expansion/selection/focus CONTROLLED
  // (tree-rewrite-plan.md "Enduring decisions" #5), so they live here rather than
  // inside the component — this is UI state same as the rest of this store.
  // Deliberately separate from activeKey (the open TAB): the plan's whole point is
  // that tree selection, tree focus, and the open tab are three independent
  // concepts (plan §"Focus ≠ selection"); today's TreeView conflated the first two
  // by using activeKey as if it were the tree's selection.
  treeExpanded: ReadonlySet<string>;
  treeSelection: readonly string[];
  treeFocused: string | null;

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
  // Remaps every name-derived key from oldKey to newKey, AND every descendant key
  // under it (T4b — see the implementation for the prefix rule). Replaced T4b's
  // `renameItem`, which handled the exact key alone.
  moveSubtree: (oldKey: string, newKey: string, newName: string) => void;

  // Each setter REPLACES the field wholesale — treeExpanded in particular is a
  // Set, and it must always be swapped for a new one, never mutated in place:
  // zustand (like React) compares by reference, so mutating the existing Set
  // would leave every selector seeing the "same" value and never re-render.
  setTreeExpanded: (next: ReadonlySet<string>) => void;
  setTreeSelection: (next: readonly string[]) => void;
  setTreeFocused: (next: string | null) => void;

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
        // fall back to the neighbour that took its place (or the new last tab)
        const next = openTabs[idx] ?? openTabs[idx - 1] ?? null;
        activeKey = next ? next.key : null;
      }
      return { openTabs, activeKey };
    }),

  setActiveKey: (activeKey) => set({ activeKey }),

  // moveSubtree remaps ALL name-derived keyed state — the item's own key and every
  // key beneath it — from oldKey to newKey after a successful rename (T4b), so the
  // open tab, its draft, its last response, and the tree's own
  // selection/focus/expansion follow the new name instead of detaching (itemKey is
  // name-derived, see format.ts). It REPLACED `renameItem`, which handled the exact
  // key alone: with folder rename shipped (T4a), a single-key remap is never the
  // right answer — renaming a folder changes the key of every descendant at once —
  // and keeping two functions where the wider one subsumes the narrower would just
  // be an invitation to call the wrong one. This is the plan's "identity hazard"
  // (docs/design/tree-rewrite-plan.md), whose failure mode is silent: a detached
  // tab quietly forgets its draft and last response, which reads as lost work
  // rather than as a bug.
  //
  // The PREFIX is `oldKey + "/"`, never bare oldKey. "/" is keyOf's join character
  // (lib/format.ts), so requiring it is what stops a sibling named "Foo2" from
  // being swept up by a rename of "Foo" — a bare startsWith would rewrite
  // "Foo2/Bar" into "Bar2/Bar" and detach an unrelated request's state.
  moveSubtree: (oldKey, newKey, newName) =>
    set((s) => {
      if (oldKey === newKey) return {};
      const prefix = `${oldKey}/`;
      // The one place the remap rule lives: the moved key itself, a descendant
      // (prefix swapped, its own tail kept verbatim), or `null` for "not ours,
      // leave it alone". Every collection below is rewritten through this, so none
      // of them can disagree about what counts as a descendant.
      const remap = (key: string): string | null => {
        if (key === oldKey) return newKey;
        // key.slice(oldKey.length) keeps the leading "/" plus the whole tail, so
        // nesting depth is irrelevant — a two-level descendant needs no extra case.
        if (key.startsWith(prefix)) return newKey + key.slice(oldKey.length);
        return null;
      };
      // Each helper preserves the "return the identical reference when nothing
      // changed" habit, per collection: zustand (like React) compares by
      // reference, so rebuilding an equivalent-looking container would re-render
      // every consumer of state the rename never touched.
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
      // treeExpanded holds bare ids in a Set, not a key -> value map, so it gets
      // its own pass rather than reusing `rekey`.
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
        // Only the EXACT match's display NAME changes. A descendant's tab shows
        // only its own last path segment, which renaming an ancestor folder does
        // not touch — rewriting it to newName would relabel every open tab under a
        // renamed folder with the FOLDER's new name.
        return t.key === oldKey ? { key: to, name: newName } : { ...t, key: to };
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
        // treeSelection/treeFocused/treeExpanded are name-derived keys too
        // (components/tree/'s <Tree> requires them controlled — see the field
        // comments above), so rows that were selected/focused/expanded need the
        // same remap activeKey gets, or their selection wash / focus ring /
        // open-ness silently detaches the moment the rename's refetch produces
        // rows under the new keys. treeExpanded is the one that only became
        // REACHABLE at T4a: a request is never expandable, so before folder rename
        // existed no renamable item could be a member.
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
