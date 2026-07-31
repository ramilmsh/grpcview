# grpcview — collection tree rewrite plan

Rewrite the request-collection tree from scratch as a **standalone, data-agnostic
React component**, with interaction UX **identical to VS Code's file explorer**.

Replaces `ui/src/features/workspace/TreeView.tsx` (169 lines, naive recursion,
per-node `useState`, single-select, no keyboard support).

## Why build rather than adopt a library

Investigated and rejected (2026-07-30/31):

- **monaco's internal `vs/base/browser/ui/tree`** — the literal VS Code widget, already
  in our bundle via the Peek References contrib. Spiked and browser-verified; findings in
  `tree-spike-findings.md`. Rejected: no type contract (284-line hand-written shim),
  undocumented/unversioned internal API, find widget unreachable in the shipped build,
  and — decisively — row content lives in a *recycled DOM pool* outside React, so our
  hover-reveal gear/pencil/plus/trash affordances would have to be hand-wired per
  template. Imperative rows are the wrong substrate for this UI.
- **`react-arborist`** (751k downloads/wk, healthy) — rejected on three counts: 5 runtime
  deps into a single-file offline bundle (incl. `react-dnd@14`, two majors stale and
  *bundled* not peer; plus `redux`); `react-window@1` forces fixed row heights; and
  selection is uncontrolled by default (`First-class controlled-selection API` is still
  an open issue upstream) when we need it in `zustand`.
- **`@headless-tree/react`** (272k/wk, zero deps) — the closest fit, and the fallback if
  this plan stalls. Rejected because once virtualization is off the table (see below),
  its remaining contribution is a state machine we can own in ~300 lines.

**The fact that decides it: we do not need virtualization.** Collections are dozens to
low hundreds of requests, not the tens of thousands VS Code's Explorer targets. That is
the core value proposition of both arborist and much of headless-tree.

Precedent for hand-rolling: **VS Code** itself is vanilla TS with its own DOM helpers
(`import { $, append, clearNode } from '../../dom.js'`) — no framework. **Eclipse Theia**,
the React-based VS Code alternative, reimplemented the whole thing (`tree-widget.tsx`
is 62 KB, beside `tree-model.ts` / `tree-expansion.ts` / `tree-focus-service.ts` /
`tree-navigation.ts` / `tree-search.ts` / `tree-compression/`). **Bruno**, the closest
peer (open-source API client, React, same sidebar shape) hand-rolls its tree and imports
only `react-dnd`. Steal Theia's *decomposition*, not its code.

## Enduring decisions (do not violate)

1. **The component knows nothing about gRPC.** It takes ids, children, expandability,
   and a typeahead label. `ItemWithPath`, `Service`, method kinds, and every affordance
   stay in the caller's row renderer. Lives in `ui/src/components/tree/`, beside
   `components/ui/` and `components/shell/` — not in `features/workspace/`.
2. **Rows are React.** The caller renders row content via a render prop. This is the
   whole reason we are not using monaco's widget. Rich rows are the *optional* tier over a
   declarative default — see decision 8.
3. **Flat visible-rows model.** The tree derives an ordered array of visible rows from
   roots + expansion state; every behavior (keyboard, range-select, DnD targeting)
   operates on array indices, not recursion. This is the load-bearing decision — it is
   also what makes virtualization a cheap, orthogonal, *later* choice
   (`@tanstack/react-virtual`, 20.7M/wk, drops over a flat array).
4. **Focus ≠ selection.** Two distinct pieces of state. This is why VS Code's keyboard
   navigation feels right, and it is the thing the current implementation has no concept
   of. Neither is the same as `activeKey` (the open *tab*) — today `TreeView` conflates
   tree selection with the open tab; the rewrite separates them.
5. **State is controlled, owned by `zustand`.** Expansion/selection/focus live in the
   store, not in the component. Fixes `TreeView.tsx:45`, where per-node `useState` loses
   expansion whenever a node unmounts. The component supports uncontrolled use (for
   reuse) but grpcview drives it controlled.
6. **No new npm dependencies** through T5. `react-dnd@16` is the one permitted addition
   at T6, and only if native HTML5 DnD proves insufficient — Bruno's precedent.
   Adding it means editing `BUILD_DEPS` in `ui/BUILD.bazel`.
7. **The adapter is `TreeDataProvider`-shaped where that costs nothing.** Async-capable
   `getChildren`, `getParent`, collapsible-state, invalidation — because the descriptor
   explorer needs them and a native VS Code view could then share the provider. We do
   **not** import the rest of that API; see §"Second consumer".
8. **One provider, two renderers.** A tree's data layer is written once and rendered
   either by this component (standalone) or by VS Code's native tree (plugin mode).
   Portable providers live in `ui/src/lib/tree-providers/`, **import no React**, and
   describe rows declaratively via `getTreeItem`. `renderRow` is the opt-out that trades
   portability for rich rows — the request tree takes it knowingly. See §"Second consumer".

## The component contract

The adapter deliberately mirrors VS Code's `TreeDataProvider` signatures — see
§"Second consumer" for why, and for what we pointedly do *not* copy.

```ts
// ui/src/components/tree/types.ts
interface TreeAdapter<T> {
  getId(node: T): string;              // stable within a render pass

  // TreeDataProvider.getChildren shape: `undefined` => roots. May return a promise;
  // T0 implements the SYNCHRONOUS path only (see "Second consumer").
  getChildren(node?: T): T[] | Promise<T[]>;

  // Mirrors TreeItemCollapsibleState. Carries folder-ness AND per-node default
  // expansion, which `isExpandable` could not: a descriptor tree wants files
  // expanded and nested messages collapsed.
  getCollapsibleState(node: T): "none" | "collapsed" | "expanded";

  // Mirrors TreeDataProvider.getParent. Required for reveal().
  getParent?(node: T): T | undefined;

  // The PORTABLE row description (see "Second consumer"). Everything here must be
  // renderable by both this component's default row renderer and a VS Code TreeItem.
  // A provider that implements only this is portable by construction.
  getTreeItem(node: T): TreeItemLike;

  getTypeaheadLabel(node: T): string;
}

interface TreeItemLike {
  label: string;
  description?: string;   // dimmed trailing text, e.g. a folder's request count
  icon?: IconToken;       // abstract; each renderer maps it (codicon vs Phosphor)
  tooltip?: string;
  // Abstract node kind. VS Code maps it to package.json menu `when` clauses; our T5
  // context menu keys off the same value. NOT a free-form string — enumerate it.
  kind?: string;
}

// Fixed vocabulary, extended deliberately. Ad-hoc strings break portability silently.
type IconToken =
  | "folder" | "file"
  | "symbol-class" | "symbol-enum" | "symbol-field" | "symbol-method";

// Imperative handle, for the things that are actions rather than state.
interface TreeHandle<T> {
  reveal(id: string, opts?: { select?: boolean; focus?: boolean; expand?: boolean }): void;
  invalidate(node?: T): void;   // onDidChangeTreeData equivalent; no-op while sync
}

interface TreeRowState {
  focused: boolean;
  selected: boolean;
  expanded: boolean;
  depth: number;
  renaming: boolean;
  dropTarget: "into" | "before" | "after" | null;
}

interface TreeProps<T> {
  adapter: TreeAdapter<T>;              // roots come from getChildren(undefined)
  handle?: Ref<TreeHandle<T>>;
  // RICH tier, optional: overrides the default declarative renderer built from
  // getTreeItem. Supplying this makes the tree standalone-only — see "Second consumer".
  renderRow?(node: T, state: TreeRowState): ReactNode;

  // Controlled state — omit any pair to fall back to internal state.
  expanded?: ReadonlySet<string>;  onExpandedChange?(next: ReadonlySet<string>): void;
  selection?: readonly string[];   onSelectionChange?(next: readonly string[]): void;
  focused?: string | null;         onFocusedChange?(next: string | null): void;

  onOpen?(node: T): void;                       // Enter / click on a leaf
  onRenameCommit?(node: T, next: string): void;  // component owns the edit UI
  onDelete?(nodes: T[]): void;                   // Delete key; host confirms
  onMove?(nodes: T[], to: { parent: T | null; before?: T }): void;
  onContextMenu?(nodes: T[], ev: React.MouseEvent): void;
  canDrop?(dragged: T[], to: { parent: T | null; before?: T }): boolean;

  indent?: number;      // default 8, VS Code's workbench.tree.indent
  rowHeight?: number;   // default 22
  compactFolders?: boolean;  // T7
  "aria-label"?: string;
}
```

**Two props T0 added that the block above did not anticipate.** Both exist because the
component owns the row element's `className`, so a host can no longer decorate a row itself
the way `TreeView.tsx` did:

- `activeId?: string | null` → `TreeRowState.active`, painted with the existing
  `.treerow.on`. This is the **open tab**, the third thing that is neither focus nor
  selection (decision 4 named it and then gave the host no way to express it). Not a
  grpcview-ism: VS Code's Explorer highlights the row of the active editor the same way.
- `renamingId?: string | null` → makes `TreeRowState.renaming` honest. It exists because the
  old tree disabled a row's whole click handler while that row was being renamed
  (`onClick={editing ? undefined : …}`), and losing that guard meant a click on a renaming
  row committed the rename *and* opened the request. This one is a **T0 bridge**: at T4b the
  component owns the edit UI, and it becomes internal state rather than a prop.

Anything further has to clear the bar in §Risks ("Over-fitting"): name the consumer that
wants it. A third addition (`liveAdapter`, an unfiltered adapter for expansion bookkeeping)
was built during T0 and **reverted** for failing exactly that test — see the note under
"Known limitations" below.

Module layout, one concern each:

| file | responsibility |
|---|---|
| `types.ts` | the contract above |
| `flatten.ts` | roots + expansion → ordered visible rows (+ index lookup) |
| `useTreeState.ts` | controlled/uncontrolled expansion, selection, focus, anchor |
| `selection.ts` | range/toggle/select-all semantics against the flat array |
| `keymap.ts` | key event → intent, one table, no DOM |
| `typeahead.ts` | keystroke buffer, 1s debounce, wrap-around match |
| `dnd.ts` | pointer position → `into`/`before`/`after`, validity, autoscroll |
| `Tree.tsx` | the component: roving tabindex, aria, event wiring |
| `TreeRow.tsx` | indent, indent guides, twistie, drop indicator; content = render prop |

## Second consumer: the descriptor explorer (and VS Code alignment)

The component's generality is not speculative — `descriptor-explorer-plan.md` needs a
second tree: the `ProtoFile.path` list grouped by package directory, beside a read-only
Monaco pane. **Treat "the descriptor explorer's file tree drops into this component
unchanged" as the acceptance test for the contract.** Two real consumers is what makes an
API general; one real plus one hypothetical is how abstractions get over-designed.

Concrete evidence the contract is right: the descriptor explorer is a *path* tree
(`google/protobuf/...`), where folder-chain compaction is genuinely correct — unlike
request folders, which are logical groupings. Same `compactFolders` flag, default **on**
there and **off** here.

### The requirement: one provider, two renderers

**Goal (user's framing, 2026-07-31):** a tree's *data* is written once and rendered either
by this component (standalone web app) or by **VS Code's native tree view** (plugin mode),
chosen at runtime. The provider is the shared unit; the renderer is swapped.

That is a stronger constraint than "our API happens to resemble theirs", and it has two
consequences that override choices made earlier in this doc.

**Consequence 1 — providers must be framework-free.** A shared provider cannot import
React, our components, or anything in `ui/src/components/`. Portable providers live in
`ui/src/lib/tree-providers/`, depend only on generated proto types and plain TS, and are
consumed by either renderer. (Enduring decision 1 says the component knows nothing about
gRPC; this is the inverse and equally binding.)

**Consequence 2 — the shared contract is the *intersection* of both APIs.** Anything a
portable provider expresses must be renderable by VS Code's `TreeItem`, which cannot host
arbitrary content. So row description splits in two tiers:

| tier | row API | runs in | used by |
|---|---|---|---|
| **portable** | declarative `getTreeItem(node) → TreeItemLike` | both renderers | descriptor explorer, and any future shared tree |
| **rich** | `renderRow(node, state) → ReactNode` | this component only | the request tree |

`renderRow` overrides `getTreeItem` when both are supplied. A provider that only
implements `getTreeItem` is portable by construction — which makes portability a property
you can check, not a convention to remember.

The request tree stays deliberately **rich/standalone-only**: in plugin mode the
collection is a directory of files (`docs/design/vscode/phase-1-collection-dir.md`), so
VS Code's *file explorer* is the request tree and there is nothing to port. That
assumption is load-bearing for this split — if the plugin ever needs a custom request
tree, the hover-reveal gear/pencil/plus/trash affordances would have to collapse into
declared menu items.

### What this reverses

Two things this doc previously rejected are now **required**:

- **`getTreeItem`** — was "rejected as the sole row description". It is now the portable
  tier, and this component needs a **default declarative row renderer** that draws from it
  (label, description, icon, collapsible state). That renderer is T0 work, not a later
  nicety.
- **`contextValue`** — was rejected as extension-host plumbing. It comes back as an
  abstract **node kind** string: VS Code maps it to `package.json` menu `when` clauses,
  and our T5 context menu keys off the same value. One vocabulary, two menu systems.

Still rejected: `iconPath` as `Uri`, `Command`, `DataTransfer` + drag mime types,
`checkboxState`. Those are transport details with no shared meaning.

**Icons need an abstract vocabulary.** VS Code renders `ThemeIcon` codicon ids; we render
Phosphor. A portable provider therefore names icons from a small fixed set
(`folder`, `file`, `symbol-class`, `symbol-enum`, `symbol-method`, …) that each renderer
maps — codicons for free on the VS Code side, a lookup table on ours. Ad-hoc icon strings
are how a "portable" provider quietly stops being portable.

**The honest cost:** shared trees are capped at VS Code's expressiveness. Custom badges
like the request tree's method-kind tags (`U`, `S←`, `B⇄`) are not expressible in a
`TreeItem` — they become `description` text or an icon. Any tree we want portable must be
designed within that ceiling from the start; retrofitting is a redesign.

### Async

Under the shared-provider requirement, async is on the critical path rather than optional.
VS Code's `getChildren` is `ProviderResult<T[]>` — a *synchronous* return satisfies it, so
a sync provider is portable today. But the descriptor explorer will want laziness (resolve
per source on demand rather than materializing every symbol of every file), and the moment
its provider returns promises, **this component must implement the promise path** or the
provider stops being shareable. So T8 is not optional polish; it is the gate on the first
genuinely portable tree.

T0 still implements only the synchronous path — the request tree's data is a synchronous
react-query cache, and it is the rich tier anyway. The signature is what has to be right
from day one.

## VS Code UX spec

Keyboard, verified against `abstractTree.js` / `listWidget.js` in
`ui/node_modules/monaco-editor/esm/vs/base/browser/ui/`:

| key | behavior |
|---|---|
| `↑` `↓` | move focus one visible row |
| `shift+↑` `shift+↓` | extend selection from the anchor |
| `←` | expanded folder → collapse; otherwise → focus parent |
| `→` | collapsed folder → expand; otherwise → focus first child |
| `Space` | toggle expand/collapse |
| `Enter` | **platform-split** — macOS: rename; Windows/Linux: open |
| `cmd+↓` | open (macOS only) |
| `PgUp` `PgDn` | move focus one viewport |
| `Home` `End` | first / last visible row |
| `cmd/ctrl+A` | select all visible |
| `Escape` | clear selection; cancel typeahead; cancel rename |
| `F2` | rename focused row (every platform) |
| `Delete` / `cmd+Backspace` | delete selection (host confirms) |
| letters | typeahead — jump focus to next match, 1s buffer, wraps |

**The platform split is deliberate (decided 2026-07-31).** An earlier draft of this doc
made `Enter`-opens the one knowing deviation from VS Code; that was reversed in favour of
being faithful per platform, consistent with the enduring
"VS Code familiarity over optimality" rule. So `keymap.ts` takes the platform as an input
(one `isMac` boolean, resolved once) rather than hard-coding one table.

Note `Home`/`End` are **absent from the widget layer** — VS Code binds them above it, at
the workbench keybinding layer (`list.focusFirst` / `list.focusLast`). We implement them
directly; this explains their absence in the spike.

Mouse:

- click a leaf → select + focus + `onOpen`
- click a folder row → toggle expand (VS Code's single-click behavior)
- click the twistie → toggle **without** changing selection
- `cmd/ctrl+click` → toggle that row in the selection
- `shift+click` → extend selection from the anchor
- right-click → if the row is not already selected, select it, then `onContextMenu`

Visual: 8px indent per level; indent guides; caret twistie (folders only, hidden for
leaves); selection and hover read from Nocturne tokens (reuse `.treerow` / `.on`).

Drag and drop (T6): drop **into** a folder (reparent) or **between** rows (reorder);
drop indicator line for between, row highlight for into; multi-row drag; autoscroll near
the viewport edges; reject drops into a dragged node's own descendant.

## Deliberate deviations from VS Code

1. ~~**`Enter` opens; `F2` renames — on every platform.**~~ **Withdrawn 2026-07-31.** This
   was the one place the plan knowingly broke "identical"; it was put to the user and
   reversed. We now bind exactly what VS Code binds on each platform — macOS `Enter`
   renames and `cmd+↓` opens, Windows/Linux `Enter` opens — so `keymap.ts` is
   platform-parameterized. See the key table above. Nothing else in this doc changes.
2. **No preview/pinned-tab distinction.** VS Code single-click previews (italic tab) and
   double-click pins. grpcview's tabs have no preview concept; single click opens, double
   click is a no-op.
3. **Filter box stays.** VS Code's explorer defaults to typeahead *highlight* mode with
   find-as-filter behind `cmd+F`. We keep the existing header filter box (a grpcview
   affordance) *and* add typeahead. They compose: typeahead navigates what the filter left
   visible.
4. **Compact folders default off** (T7). VS Code defaults `explorer.compactFolders: true`,
   which is why `src/vs/base` renders as one row. Our folders are logical groupings, not
   filesystem dirs, and compression materially complicates rename and drop targeting
   (which segment of a compressed row is the target?). Implemented as a flag so it can be
   flipped after seeing it on real data.

## Backend gaps (block T4 and T6)

Verified in `proto/grpcview/v1/service.proto`:

- **No reparent.** `UpdateRequestRequest` has `path` + `item_name` as *addressing* fields
  and `name` for rename — there is no `new_path`. Drag-to-reparent needs a new
  `MoveItem` RPC (or a `new_path` field on both update RPCs). **T6 is blocked on this.**
- **No folder rename.** `UpdateFolderRequest` carries only `draft_metadata_script`; no
  `name`. This is why `TreeView.tsx:34` says folder rename is a follow-up. **T4's folder
  half is blocked on adding it.**

Both are small, additive proto changes plus service handlers — but they are Go work
inside a Bazel build, not frontend work, and each needs its own phase.

## The identity hazard (read before T4/T6)

`itemKey` is **path+name derived** (`keyOf(path, name)` in `lib/format.ts`). So a rename
or a move changes an item's key — and for a **folder**, it changes the key of every
descendant. `ui-store.ts`'s `renameItem` already remaps `openTabs` / `drafts` / `invokes`
for a single key; it has no prefix-remap.

This has not bitten yet only because folders can currently be neither renamed nor moved.
T4 and T6 both make it reachable. Each needs a **prefix remap** over all keyed state
(`openTabs`, `drafts`, `invokes`) — `moveSubtree(oldPrefix, newPrefix)` alongside
`renameItem`. Getting this wrong silently detaches a user's open tab, unsaved draft, and
last response from the request they belong to.

The alternative — server-assigned stable item ids — removes the whole class of bug but is
a storage-format change touching every RPC. Out of scope here; worth its own doc if
prefix remapping proves fragile in practice.

## Phases

Each phase is one commit on `trunk`, browser-verified before the next starts.

- **T0 — Component skeleton, behavior parity.** `components/tree/` with the contract,
  `flatten.ts`, controlled state, mouse select/expand, indent guides, **both row tiers**
  (the default renderer driven by `getTreeItem`, plus the `renderRow` override). Swap into
  `CollectionPanel` behind the same callbacks. *Verify:* everything that works today still
  works — open a request, folder counts, gear/pencil/plus/trash, filter box, empty states —
  expansion survives a re-render, and a throwaway declarative provider renders a readable
  tree with no `renderRow` at all (proves the portable tier before anything depends on it).
- **T1 — Keyboard + a11y.** The full key table above, roving tabindex, `role="tree"` /
  `treeitem` / `aria-expanded` / `aria-level`, focus ring distinct from selection.
  *Verify:* tab into the tree, drive it entirely by keyboard.
- **T2 — Multi-select.** Anchor semantics, `shift`/`cmd` click and `shift+arrow`,
  `cmd+A`, `Escape`. Make delete multi-aware (confirm dialog pluralizes).
- **T3 — Typeahead.** 1s buffer, wrap-around, match highlight. *After this lands, delete
  the monaco spike's leftovers.* Verified 2026-07-31: the spike code was **never
  committed** (no `features/tree-spike/`, no `ActiveView` case, no Rail entry in git
  history), so this reduces to deleting `tree-spike-findings.md` — it has served its
  purpose as a behavioral reference.
- **T4a — proto: folder rename.** Add `optional string name` to `UpdateFolderRequest` +
  handler + collision check mirroring `UpdateRequestRequest`.
- **T4b — Rename in-tree.** `F2` and the pencil, inline input, collision validation,
  commit on Enter / cancel on Escape, blur-commits. Prefix remap for folder renames (see
  hazard above).
- **T5 — Context menu.** Right-click → New Request / New Folder / Rename / Delete /
  Folder metadata, driven off the current selection. Keyboard-dismissable, no new deps.
- **T6a — proto: move.** `MoveItem` RPC (or `new_path`), with reject-into-own-descendant
  enforced server-side too.
- **T6b — Drag and drop.** Native HTML5 first; `react-dnd@16` only if it falls short.
  Into-folder + between-rows, multi-drag, autoscroll, prefix remap on move.
- **T7 — Optional polish.** Compact folders (flag), sticky scroll, and virtualization via
  `@tanstack/react-virtual` *only if* a real collection ever makes it necessary.
- **T8 — Async children** (*owned by the descriptor-explorer track, but on its critical
  path*). Turn on the `getChildren` promise path: children cache, loading placeholders,
  stale-response guards, `invalidate`. Not optional polish — the first lazy portable
  provider stops being shareable without it. Do not build it speculatively here either;
  build it when that provider exists.

T0–T3 are pure frontend and unblocked. T4a and T6a are the only Go/proto work. T8 lands
with the descriptor explorer.

**Committed scope for this implementation pass (decided 2026-07-31): T0–T3 only.** The
proto-dependent phases (T4a/T4b rename, T6a/T6b move) and T7's polish are deliberately not
started, which keeps the identity hazard (§above) out of reach for now — folders remain
neither renamable nor movable, exactly as today. Two consequences the implementer must
respect rather than "helpfully" fix:

- **No `moveSubtree` yet.** It is T6b work. `renameItem`'s single-key remap stays as is.
- **`F2`/`Enter`-rename reaches only what already exists**: the request row's inline
  rename (today's pencil + `EditableName`). On a folder row the rename intent is a no-op
  until T4a adds `UpdateFolderRequest.name`. The keymap still *produces* the intent, so
  T4b is a wiring change, not a keyboard change.

## Known limitations (accepted, not oversights)

- **A folder deleted and recreated with the same name at the same path is born collapsed.**
  `useTreeState` remembers which ids it has already force-expanded (`seenDefaults`) so that a
  user's manual collapse is never sprung back open; it never prunes that memory, and `itemKey`
  is path+name derived, so a recreated folder is the *literal same id* as the dead one. One
  click fixes it. A reconciliation pass was implemented and reverted: it needed the **whole**
  tree while a caller may legitimately narrow what it passes (this app's filter box does), so
  it required a second "unfiltered" adapter as a contract prop the descriptor explorer would
  inherit. Reconciling against the *narrowed* adapter instead is actively worse — it reads
  "hidden by the filter" as "deleted", so filtering forgets a collapse and clearing the filter
  springs the folder open. Adversarial review caught precisely that in the implementation. The
  clean fix is server-assigned stable ids (see §"The identity hazard"), which stays out of scope.
- **Clicking a non-input part of the row that is *currently* being renamed still commits.**
  The blur fires before the click handler, so `renamingId` is already cleared by then. Verified
  that the pre-rewrite tree had the identical characteristic — this is parity, not a new gap.

## Risks

- **Prefix remap correctness** (above) — the highest-consequence risk in the plan, since
  failures are silent and cost user work. Deserves unit tests around `moveSubtree`.
  **Decided 2026-07-31:** `vitest` is added to `ui/` (one devDependency, `bazel test
  //ui:test`) rather than leaning on browser checks. It costs one dep because the pure
  modules under test — `flatten.ts`, `selection.ts`, `keymap.ts`, `typeahead.ts`, and later
  `moveSubtree` — are DOM-free, so no `jsdom`/`happy-dom` is needed. This is the one
  exception to "no new npm dependencies through T5"; it is a devDependency and never
  reaches the shipped single-file bundle.
- **Scope creep into the row renderer.** The temptation at T5/T6 will be to move
  affordances into the tree component "so it can handle selection". Don't: the contract
  survives only if rows stay the caller's.
- ~~**`Enter` semantics** (deviation 1)~~ — settled 2026-07-31: match VS Code per
  platform. No longer open.
- **Compact folders** may look wrong on request collections; that is why it is a flag at
  T7 rather than a T0 assumption.
- **Over-fitting to VS Code's API.** The adopted signatures earn their place by serving
  the descriptor explorer. If a later "for VS Code compatibility" addition cannot name a
  grpcview consumer that wants it, it does not belong — that path ends in reimplementing
  an extension-host protocol inside a React app.
- **Portability rot.** A provider stays portable only while it avoids `renderRow`, keeps to
  the `IconToken` vocabulary, and enumerates its `kind` values. All three are easy to
  violate accidentally and the breakage is invisible until someone tries the VS Code
  renderer. Worth a lint or a type-level guard (e.g. a `PortableTreeAdapter<T>` alias that
  omits nothing but is used as the declared type of every shared provider).
- **The expressiveness ceiling is one-way.** Designing a tree rich-first and porting it
  later is a redesign, not a refactor, because `TreeItem` cannot host custom content.
  Decide portability per tree up front.

## Verification recipe

```
bazel run //service/cmd/dev     # backend on :10000
bazel run //ui:dev              # vite on :5173
```

Add `env HOME=<tmpdir>` to the backend for a throwaway workspace. Browser-verify each
phase per `AGENTS.md` § "Browser verification hook".

**Three gates, and the trap in the obvious one.** `bazel build //ui:ui` does **NOT** typecheck
— proven twice during T0 by injecting a hard type error into `Tree.tsx` and watching the build
pass. `vite build` transpiles with esbuild, which strips types per-file without ever checking
them, and no `tsc` step is wired into `ui/BUILD.bazel`. `bazel test //ui:test` doesn't either
(vitest's `typecheck` option is off). So:

```
cd ui && ./node_modules/.bin/tsc --noEmit -p tsconfig.json   # the only real typecheck
bazel test //ui:test                                          # vitest, 60 tests
bazel build //ui:ui                                           # the real release bundle
```

Run all three. The first one is not optional — it is the gate that catches an unused import or
a wrong generic, and this rewrite hit both. `AGENTS.md`'s claim that the build "catches TS
errors" is true only for syntax and unresolved imports.

Fold the shipped behavior into `AGENTS.md` and delete this doc when T6 lands, per the
convention used by the request-body and definition-sources tracks.
