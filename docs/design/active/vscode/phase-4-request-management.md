# Phase 4 — request management

**Prereqs:** [phase 2](../../shipped/vscode/phase-2-body-files.md) (which forces the save-model decision).
**Unblocks:** [phase 5](./phase-5-extension.md) becomes a mapping rather than a
reimplementation. See [`README.md`](./README.md) for the track overview.

## Goal

Converge the web UI's internal model onto the concepts VS Code's workbench provides,
so the VS Code shell becomes a swappable frontend instead of a parallel
implementation. Request *management* will always differ between the two frontends —
VS Code's tabs and Explorer there, ours here — but they should differ in
implementation, not in behavior.

## Current shape

`ui/src/lib/ui-store.ts` holds a flat `openTabs: {key, name}[]` plus `activeKey`,
`drafts` and `invokes`, all keyed by `itemKey`. `RequestTabs.tsx` renders the strip
with no dirty indicator, no preview, no pinning, no reordering.

## The five changes

### 1. Key on slug, not display name

`itemKey` is a display-name path (`format.ts:52`), and its own comment admits the
flaw: a rename changes the key, so `renameItem` (`ui-store.ts:161-179`) has to re-key
`openTabs`, `activeKey`, `drafts` and `invokes` by hand.

The store already guarantees stable unique slugs (`layout.go:120`, `uniqueSlug`) and a
rename edits only `request.json` — it never moves the directory (`storage.proto:89-94`,
`ItemMeta`). Expose the slug on the wire `Item`, key everything on the slug path, and
delete `renameItem` entirely.

### 2. Documents separate from editors

A **document** is buffer + baseline + dirty flag, one per resource, living
independently of any tab. An **editor** is a view of a document. Today `drafts` is a
document registry wearing tab clothing: created and destroyed with its tab, so closing
a tab silently discards the buffer and the same request cannot be open twice.

### 3. Dirty state and explicit save

**There is none today** — `RequestWorkspace.tsx:177` debounces straight into
`updateRequest.mutate`. Autosave is a defensible design and VS Code supports it
(`files.autoSave`), but once bodies are files (phase 2), autosave means writing
`body.ts` every 300ms, which fights VS Code's save model and makes `git status` noisy.

So: dirty dot on the tab, ⌘S, close-confirm, and persistence of unsaved buffers so a
browser reload does not lose work. Decide this deliberately — it is the one change in
this phase that alters existing behavior users can feel.

### 4. Preview tabs

Single-click from the tree opens an italic, *replaceable* tab; editing it or
double-clicking pins it. One `previewKey` field and a branch in `openTab`. This is the
single reason VS Code does not accumulate forty tabs.

### 5. Command registry as data

`{id, title, keybinding, when, run}` driving ⌘P (fuzzy over request paths), ⌘⇧P
(palette), ⌘W, ⌘⇧T, ⌘↵ (invoke). This is the table phase 5 maps onto
`contributes.commands` / `contributes.keybindings` — building it as *data* rather than
as `onClick` handlers is what makes the extension a mapping.

## Out of scope

Editor groups / split view. Least valuable thing VS Code has for a request client.

## Verify

Browser, all of:

- Rename a request that has unsaved edits and a response showing: the buffer and the
  invoke result both follow it, with no remap code involved.
- Open the same request in two editors; edit in one; both views reflect it.
- Single-click three tree items in a row: one preview tab, replaced twice. Then type in
  it and confirm it pins.
- Close a dirty tab: confirm prompt. Reload the page: unsaved buffers survive.
- Drive every command from the palette, and confirm each keybinding fires the same
  command id.

## Open questions

- Autosave or explicit save (see 3)? Explicit is VS Code-native and forced for
  file-backed fields; autosave is the status quo. A middle option is explicit save for
  body/metadata and autosave for the cheap scalar fields (target, method) — which is
  roughly what VS Code itself does with settings vs. documents.
- Where do unsaved buffers persist — `localStorage`, or a gitignored scratch area under
  `.grpcview/` so the extension and the web UI share hot-exit state?
- Does the tree get multi-select and drag-to-reorder in this phase, or does that wait
  for the extension's `TreeDragAndDropController` to define the semantics?
