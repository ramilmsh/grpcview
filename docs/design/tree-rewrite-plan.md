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
  component owns the edit UI, and it becomes internal state rather than a prop. *(Done — both
  it and `onRenamingChange` are deleted from `TreeProps`; `TreeHandle.startRename` is how the
  host's pencil gets in now.)*

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
| `platform.ts` | `IS_MAC`, resolved once (added T1 — see below) |
| `navigate.ts` | a move intent → a row index; parent / first-child lookup (added T1) |
| `typeahead.ts` | keystroke buffer, 1s debounce, wrap-around match |
| `dnd.ts` | pointer position → `into`/`before`/`after`, validity, autoscroll |
| `Tree.tsx` | the component: focus container, aria, event wiring |
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
5. **The rename box replaces the whole row content** (T4b). VS Code keeps the file icon
   visible beside its edit box; we yield the entire content area to the input, keeping only
   the row *shell* (indent guides, twistie column). Our rich request rows show a
   `MethodKindTag` (`U`, `S←`, `B⇄`) where VS Code shows a file icon, and swapping that tag
   for the portable tier's generic `"file"` icon mid-edit would be a stranger visual
   substitution than simply yielding the row. The shell stays because it is the tree's own
   chrome, not the content's — dropping it would make the edited row jump out of the indent
   staircase every other row aligns to. Consequence to expect in the browser: the row's
   pencil/trash buttons and the method-kind tag disappear while the box is open.

## What T1 settled (implemented 2026-07-31)

The key table above is implemented as specified, including the platform split. Four things
the T1 line didn't spell out, recorded here so they aren't re-derived or "fixed" later:

1. **One focus container, not a roving tabindex.** T1's line says "roving tabindex"; what
   shipped is a single `tabIndex={0}` on `.tree` plus `aria-activedescendant` naming the
   focused row, with `.treerow.foc` as the visible logical-focus marker. This is what VS
   Code's list widget actually does, and what T0's committed CSS already assumed — so it is
   the *less* deviant option despite contradicting the phrase. One `Tab` reaches the tree, a
   second leaves it; DOM focus never moves between rows.
2. **`aria-posinset` / `aria-setsize` on every row**, beyond T1's enumerated
   `tree`/`treeitem`/`aria-expanded`/`aria-level`. Our rows are a *flat* list of siblings
   with no `role="group"` per folder, so `aria-level` alone leaves sibling position
   unstated — and a browser synthesising it from flat DOM order would count across the whole
   visible tree ("5 of 8") instead of within the parent ("3 of 5"). VS Code sets both on
   every row (`list/listView.js:592-593`) with the semantics `flatten.ts` now reproduces:
   set size = the parent's visible-child count, position = 1-based index among visible
   siblings (`tree/abstractTree.js:137-146`). Computed in `flatten.ts` during the existing
   walk, which also makes it unit-testable with no DOM.
3. **A third T0-bridge prop: `onRenamingChange`.** `renamingId` (T0) let the host say which
   row is mid-rename; this lets the tree *request* one, so `F2`/macOS-`Enter` reach the
   existing pencil affordance. Like `renamingId` it folds into internal state at T4b. The
   host is what decides a row is unrenamable: `CollectionPanel` refuses folder ids, because
   `UpdateFolderRequest` has no `name` field until T4a — and a `renamingId` pointing at a
   folder would silently swallow that row's clicks forever, since the tree has no notion of
   "folder" to guard on.
4. **Paging measures the scroll ancestor, not `.tree`.** `.tree` has no bounded height, so
   its `clientHeight` is its full *content* height; measuring it would make `PageDown`
   identical to `End` for any collection that overflows. `Tree.tsx` walks up to the nearest
   ancestor whose computed `overflow-y` is `auto`/`scroll`. Also: a `move` intent with
   nothing focused starts from a virtual position just outside the array, which the plan
   only specified for `up`/`down` — `pageUp`/`pageDown` inherit the same rule, so they land
   one stride in from an end rather than collapsing to the first/last row.

**For T2:** the intent → action dispatch is a `switch` inside `Tree.tsx`'s `handleKeyDown`.
`keyToIntent`, the `navigate.ts` lookups, and the editable-target guard are each unit-tested,
but that glue is not — it needs component state, and this suite has no DOM. T2 has to edit
that same switch (`shift`+arrow, `cmd+A`, `Escape`); extract it into a pure
`applyIntent(intent, ctx) → actions` then, rather than now and again immediately after.
*(Done — `dispatch.ts`.)*

## What T2 settled (implemented 2026-07-31)

1. **The dispatch extraction happened, and it is where the tests went.** `dispatch.ts`
   exports plain-data `TreeAction[]` plus `applyIntent<T>(intent, ctx)`; `Tree.tsx` is now a
   thin interpreter over the returned actions. `setExpanded` carries a *desired final state*
   rather than a toggle, so an action list stays meaningful independent of when it is applied.
   `TreeAction` deliberately lives in `dispatch.ts`, not `types.ts` — `types.ts` is the public
   adapter/props contract, and this is private wiring between two modules. This is what T1's
   untested glue is finally covered by: `dispatch.test.ts` is 65 of the suite's 228 tests.
2. **The anchor gets no controlled pair, and a rename can leave it stale.** `ui-store.ts`'s
   `renameItem` rekeys `treeSelection`/`treeFocused`/`treeExpanded` because all three are
   store-owned; the anchor is neither controlled nor store-owned, so after a rename it holds
   an id that no longer exists. Left that way on purpose: `TreeProps` has no consumer that
   wants to read or drive an anchor (the over-fitting test — "name the consumer" — has no
   answer), and the failure already degrades gracefully. `rangeSelection` treats a missing
   anchor id as "no anchor" and falls back to a single-row selection — the same branch the
   filter box already exercises — and `applyIntent`'s `move` case resets the anchor on the
   next plain arrow key. Worst case is one shift+arrow immediately after a rename extending
   from nowhere.
3. **Nested selections are pruned once, at the point delete is requested.** A batch can hold
   a folder *and* its own descendant (shift+click across an expanded folder, or ctrl+click
   picking both). `pruneNestedSelections` (`lib/format.ts`) drops any item that has a strict
   path-prefix ancestor in the same batch, which also collapses a three-level selection to its
   topmost ancestor in one pass with no separate transitive-closure step. Pruning is not about
   correctness of the deletes — `Collection.Delete` is `os.RemoveAll` and idempotent, so a
   redundant delete is a no-op — it is about the confirm dialog's *count* being honest:
   otherwise it offers "Delete 5 items" when only 2 are independent operations.
4. **A row's own trash button stays single-item.** It acts on that row regardless of what the
   broader selection is, matching how a file manager's per-row delete icon behaves; only the
   keyboard path is selection-wide. Both feed one `confirm` state (a list), so there is exactly
   one confirm flow. `deleteConfirmCopy` (`delete-confirm.ts`) keeps the single-item wording
   byte-identical to T1's and pluralizes only above one, picking "folders"/"requests"/"items"
   by composition.
5. **Batch deletes fire unawaited, like every other mutation here.** Considered and rejected
   sequencing them with `mutateAsync` — nothing else in the app awaits a mutation, the failure
   mode that would justify it is already ruled out by pruning, and `Collection.Delete` takes a
   per-call mutex server-side.

### The review T2 was owed (done 2026-08-01)

T2 first shipped **unreviewed** — its adversarial pass was terminated without emitting
findings. That pass has now run (two reviewers, split by file), every claim was checked in a
real browser before being acted on, and six defects were fixed. Suite: **253 tests**, up from
T2's 228.

What review caught, all six reproduced in-browser first:

1. **Escape cancelled the delete dialog *and* destroyed the selection it was about to
   delete**, so cancel-then-retry was impossible. `Dialog` declared `aria-modal="true"` while
   delivering none of it: DOM focus stayed on `.tree`, so one Escape hit two independent
   listeners — the tree's `onKeyDown` (clear selection) and `Backdrop`'s *window* keydown
   (close). The tree's `preventDefault` cannot stop a window listener. Fixed generically in
   `Dialog`/`Backdrop` (focus into the card on open, restore to the opener on unmount), not by
   special-casing the tree. Same fix stops a second `Delete` keypress re-firing the host handler
   underneath an open dialog, and leaves the tree keyboard-drivable after any modal closes
   (focus used to land on `<body>`).
2. **A twistie collapse stranded the focused row.** Collapsing a folder whose descendant was
   focused left `aria-activedescendant` pointing at nothing, and the next `↓` jumped to **row
   0** — `targetIndex` reads an unknown `fromId` as -1. Now a collapse rebases focus *and* any
   selected descendants onto the folder itself (VS Code's behavior). The selection half matters
   because selection of hidden rows is invisible, and `resolveDeleteIds` would otherwise act on
   rows the user cannot see. New pure helper `descendantIds` in `navigate.ts`.
3. **A mouse click on a partially-clipped row scrolled the list out from under the cursor**
   (measured: 12px clip → `scrollTop` 111→99). T2 had routed the mouse path through the
   keyboard's `focusRow`, whose comment claimed `block:"nearest"` is a no-op for a visible
   target — true, but *partially* visible is exactly the case it isn't. The `focus` action now
   carries an explicit `scroll` flag: keyboard `true`, mouse `false`. Still one focus code path.
4. **`role="tree"` had no `aria-multiselectable`**, so per ARIA the whole T2 feature was
   single-select to assistive tech.
5. **shift+click painted a native text selection over the row labels.** `.treerow` now sets
   `user-select: none`, with the rename input exempted.
6. **`Delete` was a silent no-op when a selection existed but nothing was focused** (Tab in →
   `cmd+A`, which deliberately never sets focus → `Delete`). `resolveDeleteIds`' null-focus
   branch now falls back to the selection.

Two proposed findings were **rejected** — do not re-open them:

- *"A plain `↑`/`↓` should collapse the selection to the focused row."* It should not. The key
  table above says focus-only, and the vendored `listWidget.js:281-296` confirms it:
  `onUpArrow`/`onDownArrow` call `focusPrevious`/`focusNext` + `setAnchor` and never
  `setSelection`. This finding was an artifact of a review brief that added "(and select it,
  single)" to the spec row on its own authority.
- *"macOS ctrl+click falls into the plain-click branch and opens the row."* Not reproducible:
  Chrome/macOS fires no `click` for ctrl+click. It is a real gap in Firefox, and VS Code's
  `MouseController` does guard it (`isMouseRightClick` → skip `setSelection`) — add that guard
  at **T5**, when there is a context menu for it to interact with. *(Done at T5, as a
  deliberate superset of VS Code's own check — see §"What T5 settled" #2 for what the vendored
  `isMouseRightClick` actually guards and why one term had to be added to it.)*

Two nits also fixed: `pruneNestedSelections` now de-duplicates exact-equal entries (a rename
collision can produce two identical keys, which made the dialog count read 2 for one row), and
`deleteConfirmCopy([])` no longer returns "Delete 0 folders" (unreachable — `Dialog` returns
null when closed — but an odd branch to leave behind).

Still open, deliberately: `Dialog` has **no Tab trap**, so `Tab`-then-`Escape` still reaches
the tree. A naive trap would break `Tab`-to-indent inside the Monaco editors that two dialogs
host.

## What T4b settled (implemented 2026-08-01)

The tree owns rename end to end now: `renamingId` / `onRenamingChange` are **deleted** from
`TreeProps` (T0/T1 built them as bridges precisely so this phase could remove them), the
component holds `renamingId` as internal `useState`, and the host hears about a rename exactly
once — `onRenameCommit(node, next)`, only ever for a value that is non-blank, actually changed,
and free of a visible-sibling collision. Six calls worth recording:

1. **The input replaces the whole row content**, both tiers, `renderRow` included — see
   §"Deliberate deviations from VS Code" #5 for the reasoning and the visual consequence. The
   branch is checked *before* `renderRow`, in `TreeRow.tsx`, so a rich renderer cannot
   accidentally win over it.
2. **`adapter.getTreeItem(node).label` is the rename's source of truth, even for a rich
   adapter** — both the box's initial value and the "unchanged" comparison, and the sibling
   labels the collision check compares against. `TreeRow.tsx`'s standing rule is that
   `getTreeItem` must not be called for *content* on a rich adapter (`renderRow` overrides it,
   so such an adapter never promised the field means anything), and this is a deliberate
   narrow exception noted at that comment: a *label* is the one `TreeItemLike` field that
   cannot be meaningless, `getTypeaheadLabel` is documented as a search key rather than a
   display name, and `getTreeItem` is non-optional on `TreeAdapter<T>`. `request-tree.tsx`
   returns `node.item.name` there, which is exactly what a rename edits.
3. **`renameItem` was *replaced by* `moveSubtree`, not joined by it** — contradicting this
   phase's own line ("`moveSubtree(oldPrefix, newPrefix)` **alongside** `renameItem`") and the
   T0–T3 scope note's "No `moveSubtree` yet. `renameItem`'s single-key remap stays as is."
   With folder rename shipped (T4a), a single-key remap is never the correct call — renaming a
   folder changes the key of every descendant at once — so keeping the narrower function
   beside the wider one that subsumes it would only be an invitation to call the wrong one.
   `RequestWorkspace.tsx`'s request rename now goes through `moveSubtree` too, where the
   prefix half is simply inert. The prefix is tested as `oldKey + "/"` (`keyOf`'s join
   character), never bare `oldKey`, which is what stops a rename of `Foo` from sweeping up
   `Foo2/x`; a descendant's tab keeps its OWN display name, since a tab shows only the item's
   last path segment. `ui-store.test.ts` covers all of it — exact match on every keyed field,
   one- and two-level descendants, the string-prefix sibling, unrelated keys, `oldKey ===
   newKey`, and reference-identity preservation per collection.
4. **`startRename(id)` joined `TreeHandle`, rather than a prop coming back.** The host still
   needs a way in (its row pencil), but "which row is mid-rename" is now state the component
   owns end to end — and *starting* one is an action, not a value the host holds, which is
   exactly what the handle is for (contract: "the things that are actions rather than state").
   It is a no-op for an id that names no current row, gated on `flat.indexById` rather than on
   `reveal`'s `findNode` walk: `reveal` walks the whole adapter because its job is reaching a
   node hidden behind a collapsed ancestor, whereas a rename has nowhere to render an input
   unless a row already exists, and accepting a hidden id would leave the tree in a rename
   with no visible box to commit or escape from.
5. **Commit/cancel/refuse is one pure function**, `renameVerdict(value, current, siblings)` in
   `RenameInput.tsx`, exported for unit test the same way `Tree.tsx` exports
   `isEditableTarget` and for the same reason (no jsdom). Enter and blur consume the *same*
   verdict and differ only on `"collision"`: Enter stays in the box so the name can be fixed,
   blur has nowhere to stay and cancels — a blur must never commit a value the server would
   reject. Blank or unchanged cancels silently; `Escape` cancels. Neither key calls
   `stopPropagation`, which is load-bearing rather than an omission: `Tree.tsx`'s
   `isEditableTarget` guard already keeps every other key typed in the box (Space, Delete,
   arrows, Home/End, F2) from being reinterpreted as a tree intent. The collision check is a
   **UX affordance only** — a red inset ring (`.rename-invalid`, `--err`) plus a refused
   Enter, no tooltip and no popover — since T4a's store rejects a colliding rename
   server-side regardless, and the in-tree check cannot see siblings the filter box hid.
6. **The rename box focuses via a plain `useEffect`, never `requestAnimationFrame`** — the
   trap documented under §"Verification recipe" (the harness tab runs `visibilityState:
   "hidden"`, where rAF never fires, making an rAF focus both unverifiable *and* genuinely
   absent). `components/ui/EditableName` is **not** reused: it stays in the repo for
   `MethodHeader` and `ScriptsView`, keeping its rAF focus and gaining no collision
   validation, because bolting both onto a component two unrelated call sites depend on is
   worse than the purpose-built `RenameInput.tsx` the tree now has.

## What T5 settled (implemented 2026-08-01)

Right-click works, on rows and on the panel's empty space, driven off the selection.
`TreeProps.onContextMenu` is unchanged — the tree still does not render a menu — and no
dependency was added (enduring decision 6's cap holds). Eight calls worth recording:

1. **A right-click now moves FOCUS, and sets the ANCHOR only when it starts a new
   selection.** T2 left both undone and cited `listWidget.js:583-589`; that method was
   re-read rather than trusted, and it does exactly one thing —
   `this.list.setFocus(focus, e.browserEvent)`, with no `setSelection` and no `setAnchor`
   anywhere in it. So moving focus is the faithful behavior *and* the useful one: it is what
   makes the next keystroke after a right-click (Escape, an arrow, F2) act on the row the
   user pointed at. `scroll: false`, like every other mouse-driven focus in the file (§"The
   review T2 was owed" #3). The stale anchor is fixed **only in the branch that has one**:
   when the right-click *replaces* the selection, the anchor is left naming a row that is no
   longer selected at all, so it is repointed at the clicked row — matching
   `listWidget.js:611-612`, where any pointer landing on a row sets focus *and* anchor. When
   the row was already selected the anchor is left alone: that selection and its anchor are
   already coherent, and silently re-anchoring a multi-row selection onto whichever of its
   rows happened to be right-clicked would change what a later shift+click extends —
   a real behavior change the widget's own `onContextMenu` does not make. One thing that
   method does which is deliberately *not* reproduced: it computes `focus` as `[]` when
   `e.index` is undefined, i.e. an empty-space right-click *clears* focus. Our tree only ever
   sees right-clicks on rows; empty space is the host's handler (#5) and leaves tree state
   untouched.
2. **The macOS ctrl+click guard is a deliberate SUPERSET of VS Code's.**
   `isRightClickGesture(ev, isMac)` (exported from `Tree.tsx` beside `isEditableTarget`, for
   the same no-jsdom reason) feeds `ClickMods.rightButton`, and `applyRowClick` checks it
   **before** shift and modKey. Two findings from reading the vendored source.
   `isMouseRightClick` is `isMouseEvent(event) && event.button === 2`
   (`listWidget.js:526-528`), consumed at `:613-615` where it gates *only* `setSelection` —
   `setFocus`/`setAnchor` run regardless. That `button === 2` half is close to dead code in a
   browser, because `click` is a primary-button event by spec (right/middle produce
   `auxclick`); it is mirrored anyway, since it costs a comparison and the widget's event
   stream is not exactly ours. The term that actually *fires* is `isMac && ctrlKey`, and **VS
   Code does not have it** — Firefox delivers a macOS ctrl+click as a `click` with button 0
   and `ctrlKey` true, which a `button === 2` test misses. It never manifests in VS Code
   because Electron is Chromium-only and Chromium fires no `click` for that gesture (which is
   exactly why the finding was originally filed "not reproducible" and deferred here). Gated
   on `isMac` because ctrl is the multi-select modKey everywhere else. `applyRowClick` returns
   **no actions at all** for the gesture rather than mirroring the widget's focus+anchor: the
   same physical gesture also fires `contextmenu`, and `handleContextMenu` already owns
   focus/anchor/selection for it, so two handlers emitting actions for one gesture would only
   race.
3. **The menu is `components/ui/Menu.tsx`, not `components/tree/`.** Enduring decision 1 says
   the tree knows nothing about gRPC; a *menu* is not tree-specific either, and this menu's
   items (`New request`, `Folder metadata`) are entirely gRPC-shaped. So it is a design-system
   primitive beside `Dialog`/`Backdrop`, the tree keeps handing over `(nodes, ev)`, and
   `CollectionPanel` renders it — which is §Risks' "Scope creep into the row renderer"
   boundary honoured at the phase that predicted it would be tested. Keyboard model: `↑`/`↓`
   step (skipping disabled, wrapping), `Home`/`End`, `Enter`/`Space` invoke, `Escape` closes.
   `stepMenuIndex` and the flip math (`menuPosition`) are exported pure functions —
   `Home`/`End` are *the same stepper* from a virtual out-of-array position, the rule
   §"What T1 settled" #4 already established for an unfocused tree, so there is no separate
   edge-finder that could disagree about what "enabled" means.
4. **Escape's double-handling trap was avoided by taking DOM focus, not by special-casing.**
   Verbatim the defect review caught for `Dialog` (§"The review T2 was owed" #1):
   `Backdrop`'s Escape listener is on `window` and the tree's own `onKeyDown` also binds
   Escape, so a menu that did not hold focus would let one keypress close the menu *and* wipe
   the selection the menu was opened to act on. The card is `tabIndex={-1}` and focuses itself
   in a **plain effect, never rAF** (the hidden-tab trap under §"Verification recipe"), so the
   keydown's bubble path never includes `.tree`. The menu's own Escape handler
   `stopPropagation`s so `Backdrop`'s window listener does not also fire; that listener stays
   as the fallback for focus landing outside the card. `Backdrop` gained a **`transparent`
   prop** (`.dialog-backdrop.clear` — no dimming wash, no centering grid) rather than a fork:
   a context menu that dimmed the window would read as a modal, but the focus save/restore is
   subtle enough that two copies would drift. ARIA follows the tree's model — `role="menu"`
   with `aria-activedescendant` on the card rather than per-item DOM focus — chosen partly for
   consistency and partly because the card must *keep* focus for the Escape isolation above to
   hold. `aria-disabled`, not the native `disabled` attribute, because a disabled `<button>`
   is removed from the hit-test model and would lose the `onMouseEnter` that drives the
   highlight. **`Backdrop`'s focus restore also gained a guard**, and this one is not
   cosmetic: it now restores to the opener only when focus is *stray* (activeElement is
   `<body>`), because a menu item that opens a dialog unmounts the menu's backdrop and mounts
   the dialog in ONE commit, and React applies the dialog input's `autoFocus` during the
   **layout** phase — before any passive-effect cleanup. Without the guard every "New
   folder"/"New request" opened from the context menu would focus its name field and then
   immediately un-focus it. The mirror case (a menu item that starts an inline rename) works
   either way, since `RenameInput` focuses in a passive effect *create* and React runs every
   destroy before any create.
5. **A multi-row selection offers Delete and OMITS the rest.** Delete is the one genuinely
   batch-capable action (`doDelete` already loops, `deleteConfirmCopy` already pluralizes);
   rename, both creates and folder metadata are single-target and would need an arbitrary rule
   for which selected row they meant. Omitted rather than greyed because a menu whose every
   row but one is greyed is noise — the greyed affordance is reserved for the one case where
   the action *is* available in principle and blocked by a fixable condition ("New request"
   with no definition source yet, mirroring the header + button's own `disabled`). Narrowing
   is decided on the **raw** selection length, not the pruned batch: the user sees N rows
   highlighted, so the menu describes N rows. The Delete **label** is
   `deleteConfirmCopy(pruned).title` — the confirm dialog's own pluralizer, reused rather than
   reimplemented, and run against the pruned batch so the label cannot disagree with the
   dialog it opens ("Delete folder", not "Delete 4 items", when three of four rows are inside
   the fourth). That pruned list is also what the action fires on.
6. **`submitFolder`'s hardcoded `path: []` is gone.** `showNewFolder: boolean` became
   `newFolderParent: ItemWithPath | null | undefined`, the same closed/root/folder shape
   `pickerParent` already used, and the path comes from `childPathOf`. The parent is
   force-expanded on open, the way `onNewRequestUnder` always did (extracted as
   `expandFolder`, now that two paths need it), and the dialog title names the destination
   ("New folder in Alpha") because a menu item invoked three levels down has to say where the
   folder will land.
7. **A request row gets Rename + Delete only — no sibling-creating actions.** A knowing
   narrowing of VS Code, whose explorer offers New File/New Folder on a *file* row (creating
   in that file's parent). A create action whose destination is the clicked row's invisible
   parent is the one item here whose target the user cannot see, and the parent folder is one
   right-click away. Recorded rather than silently done, per the "VS Code familiarity over
   optimality" rule's own requirement that deviations be written down.
8. **Empty-space right-click is `CollectionPanel`'s handler, guarded on `defaultPrevented`.**
   The tree only sees right-clicks that land on rows, so the panel's scroll container carries
   its own `onContextMenu` offering the root's two creation actions. A row right-click is
   handled by the tree first, which `preventDefault()`s it, and React's synthetic event
   bubbles up carrying that flag — so `ev.defaultPrevented` is what stops every row menu from
   being immediately overwritten by the root's. Both handlers also bail on
   `isEditableTarget`: a right-click inside an open rename box goes to the browser, because
   the native menu is the only way to paste into it, and monaco's controller guards the
   identical case identically (`listWidget.js:584`'s `isInputElement` early return). The row
   buttons (gear/plus/pencil/trash) are untouched — the menu is an additional path to the same
   handlers, exactly as F2 and the pencil are two paths to one rename.

## What T6b settled (implemented 2026-08-01)

Drag and drop works: into a folder, between rows, multi-row, with autoscroll and the
prefix remap. Ten calls worth recording.

1. **Native HTML5 sufficed; `react-dnd` was NOT added.** Enduring decision 6's one
   permitted dependency stays unspent, and `ui/BUILD.bazel`'s `BUILD_DEPS` is
   untouched. Nothing in this phase wanted a backend abstraction, a drag layer, or a
   custom drag preview — the four things react-dnd exists to provide. What the native
   API cost was one non-obvious workaround (#4) and one structural choice (#3);
   neither is worth a runtime dep in a single-file offline bundle.
2. **The zone geometry is monaco's quartering, read from source.**
   `listView.js:888-892`'s `getTargetSector` is
   `clamp(Math.floor((offsetY / size) / 0.25), 0, 3)` — four quarters, floored (an
   exact quarter boundary belongs to the *lower* sector), clamped for a pointer
   outside the row's box. The widget stops there: the sector is handed to the
   caller's `dnd.onDragOver` delegate (`abstractTree.js:63-64`) and the
   sector→before/into/after mapping lives in the workbench's file-explorer delegate,
   which is **not vendored**. So the quartering is verified and the mapping (outer
   quarters between-rows, middle half into) is our reading of it. A **leaf splits in
   half instead**, written as its own branch rather than by folding sectors 1/2 into
   before/after: there is no inside of a request, and "the whole box is a
   between-rows target" is what makes dropping between two requests easy to hit.
   `zoneForOffset` is pure, and `dnd.test.ts` pins the exact midpoint and both
   quarter boundaries.
3. **`after` an EXPANDED folder resolves to position 0 INSIDE it**, not to the
   folder's next sibling. The indicator is drawn at a y-position, and at that
   boundary the next row on screen *is* the folder's first child — a line there,
   indented one level in, unambiguously means "ahead of that child", while the
   folder's real next sibling may be many rows further down with a whole subtree in
   between. It also keeps the bottom quarter useful rather than a duplicate of
   `into`: the two adjacent bands give "first child" and "last child". An expanded
   folder with no visible children degrades to append-inside. A **collapsed** folder
   keeps the ordinary sibling reading.
4. **The drag payload does NOT round-trip through `dataTransfer`.** `getData` is
   unreadable during `dragover` in every browser (the drag data store is in
   protected mode until drop), which is exactly when the tree must know what is
   being dragged in order to decide whether the drop is legal. So the dragged ids
   live in a ref for the drag's whole life and `setData("text/plain", …)` exists
   only to make the browser treat the gesture as a real drag — it is written with
   the dragged rows' labels (so dropping outside the app pastes something a human
   recognises) and never read back. This is flagged in the code as a
   do-not-"clean-up", because turning it into a JSON payload read at drop time
   reintroduces the dragover blindness the ref exists to avoid.
5. **Drag events are DELEGATED to the container; only `dragstart` is per-row.** This
   is monaco's own structure (`listView.js` binds the dnd stream to one dom node and
   recovers the row by walking up to a `data-index` attribute, `:893+`, which
   `TreeRow` now emits) and it removes a whole class of flicker: with per-row
   handlers, moving the pointer between two rows fires `dragleave` on the old row
   *before* `dragover` on the new one, so any per-row clear blanks the indicator
   between every pair of rows. Delegated, `dragleave` fires only on leaving the tree,
   and `relatedTarget` distinguishes that from crossing our own descendants.
   Monaco's 100ms debounced clear (`onDragLeaveTimeout`) is therefore not needed and
   not copied.
6. **Autoscroll is dragover-driven, not rAF and not an interval.** Same shape as
   `animateDragAndDropScrollTop` (`listView.js:865-876`) — a proportional step,
   faster deeper into the edge band, clamped — with two deliberate differences. The
   widget calls it from an rAF loop at ~60fps, hence its small per-call step (0.3
   slope, 14px cap); ours runs once per `dragover`, which the HTML drag-and-drop
   processing model fires on movement and at least every 350ms even for a stationary
   pointer, so the per-call step is "however far the pointer pushed into the band",
   capped at 28px. rAF was rejected because it **never fires in the automated
   browser harness** (§"Verification recipe"), and an interval because an
   event-driven step has nothing to leak if a drop, a dragend or an unmount is
   missed. The band is 24px rather than 35 because a 278px sidebar of 22px rows would
   otherwise arm autoscroll a row and a half in from each edge. Measured against
   `findScrollport`, never `.tree` (no bounded height of its own — the same trap
   §"What T1 settled" #4 records for paging).
7. **Validity is split, and the host's half is real.** The tree rejects what is
   structural: into a leaf, into or around a row inside the dragged set's own
   subtree (via `navigate.ts`'s existing `descendantIds` — no second walk), and a
   no-op move. The host's `canDrop` covers exactly one thing the tree cannot know:
   `Collection.Move` refuses a **reparent** onto an existing display name
   (`ErrAlreadyExists` — a move never silently renames what it moves), and the
   destination's children may be collapsed (not rows at all) or hidden by the filter
   box. It resolves them out of the **unfiltered** `rootItems`, not off
   `to.parent.children`, because the adapter is built over `filtered` and every node
   it hands back carries pruned children. A pure reorder inside the item's own
   parent is exempt (the colliding name there is the item itself, and the server
   skips the check for that branch). So `canDrop` is passed, and it is not a
   `() => true`.
8. **A no-op drop is rejected rather than fired**, and it is only defined for a
   single dragged row: insert-before-itself, insert-before-its-own-next-sibling, or
   append-when-already-last. For a multi-row drag there is no position that leaves
   every member unchanged (the set is reinserted contiguously), so no rule is
   invented — those drops go through.
9. **Multi-item moves are SEQUENCED — the one place this app does not fire a batch
   concurrently.** One `MoveItem` per pruned node, each fired from the previous
   one's `onSuccess`. Every call in a batch carries the *same* `before`, so each
   insertion lands immediately ahead of that one sibling and the order the server
   **processes** them in becomes the resulting sibling order. Fired concurrently
   that order is whatever the transport and the store's per-call mutex happen to
   produce: correct data in a permuted order — and unlike a stale cache, the
   permutation is written to disk. `Tree.tsx` sorts the dragged set into row order
   so that "visual order in, visual order out" holds; sequencing is the other half
   of that promise, without which the sort is necessary but not sufficient.

   This is deliberately *not* the reasoning `doDelete` documents for batch delete,
   and the difference is the point: there, pruning already rules the failure mode
   out, so concurrency costs nothing. Chaining through `onSuccess` rather than
   awaiting `mutateAsync` is what keeps this from becoming the app's only
   awaited-mutation path — `onSuccess` is the callback every mutation here already
   uses, and each link does exactly what a single-item move does. A failed call
   stops the chain, leaving a *prefix* of the batch moved in the right order rather
   than an arbitrary subset. Verified on disk: dragging three rows into a folder
   leaves `folder.json`'s `items` in visual order.
10. **`moveSubtree` was reused verbatim; the destination is force-expanded.** The
    move's `onSuccess` calls `moveSubtree(itemKey(node), keyOf(newPath, name), name)`
    — the identity hazard, discharged by the remapper T4b already built and tested.
    The destination folder is force-expanded through the same `expandFolder` helper
    T5 extracted, unconditionally rather than gated on the drop being `into`: `onMove`
    is deliberately not told the zone (the destination is the whole contract), and for
    a between-rows drop the parent is necessarily expanded already, so it is a no-op
    there. `dispatch.ts` was **not** extended — its header names T6's drag-drop as a
    likely emitter of "collapse the source, expand the target" in one action list, but
    a drop changes neither focus nor selection nor expansion *inside* the tree
    (selection follows the moved rows for free, since `moveSubtree` remaps
    `treeSelection`), so there was no `TreeAction` to emit. The fold in `applyActions`
    stays correct and unexercised by this phase.

Two things deliberately NOT built, both VS Code behaviors: **auto-expand on
hover** (`abstractTree.js:71-81` opens a collapsed folder after a 500ms
`disposableTimeout` while the pointer rests on it) — it is a timer to leak and the
folder is one twistie click away mid-drag; and **a custom drag preview**, i.e. the
native single-row ghost is used even for a multi-row drag, where VS Code draws a
badge with the count.

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

**Done at T4b, and `renameItem` is gone** — `moveSubtree(oldKey, newKey, newName)` *replaced*
it rather than joining it (§"What T4b settled" #3), and covers `treeSelection` / `treeFocused`
/ `treeExpanded` as well as the three listed above. T6b's move reuses it as-is; only the
caller changes.

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
- **T1 — Keyboard + a11y.** *(Landed 2026-07-31 — see §"What T1 settled" for the four calls
  it forced, including a focus container in place of a roving tabindex.)* The full key table
  above, `role="tree"` / `treeitem` / `aria-expanded` / `aria-level` (plus
  `aria-posinset`/`aria-setsize`), focus ring distinct from selection.
  *Verify:* tab into the tree, drive it entirely by keyboard.
- **T2 — Multi-select.** Anchor semantics, `shift`/`cmd` click and `shift+arrow`,
  `cmd+A`, `Escape`. Make delete multi-aware (confirm dialog pluralizes).
  *(Implemented 2026-07-31; reviewed, fixed and browser-verified 2026-08-01 — see
  §"What T2 settled" for the five calls it forced and the six defects review caught.)*
- **T3 — Typeahead.** 1s buffer, wrap-around, match highlight. *After this lands, delete
  the monaco spike's leftovers.* Verified 2026-07-31: the spike code was **never
  committed** (no `features/tree-spike/`, no `ActiveView` case, no Rail entry in git
  history), so this reduces to deleting `tree-spike-findings.md` — it has served its
  purpose as a behavioral reference.
- **T4a — proto: folder rename.** Add `optional string name` to `UpdateFolderRequest` +
  handler + collision check mirroring `UpdateRequestRequest`.
- **T4b — Rename in-tree.** `F2` and the pencil, inline input, collision validation,
  commit on Enter / cancel on Escape, blur-commits. Prefix remap for folder renames (see
  hazard above). *(Implemented 2026-08-01 — see §"What T4b settled" for the six calls it
  forced, including `moveSubtree` **replacing** `renameItem` rather than joining it, which
  contradicts this line.)*
- **T5 — Context menu.** Right-click → New Request / New Folder / Rename / Delete /
  Folder metadata, driven off the current selection. Keyboard-dismissable, no new deps.
  *(Implemented 2026-08-01 — see §"What T5 settled" for the eight calls it forced, including
  the right-click focus/anchor decision, the macOS ctrl+click guard this phase was handed by
  T2's review, and `submitFolder` losing its hardcoded root path.)*
- **T6a — proto: move.** `MoveItem` RPC (or `new_path`), with reject-into-own-descendant
  enforced server-side too.
- **T6b — Drag and drop.** Native HTML5 first; `react-dnd@16` only if it falls short.
  Into-folder + between-rows, multi-drag, autoscroll, prefix remap on move.
  *(Implemented 2026-08-01 — see §"What T6b settled" for the ten calls it forced,
  including the after-an-expanded-folder resolution, the `dataTransfer`
  non-round-trip, and why `react-dnd` was not added.)*
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

*(Both bullets are spent as of 2026-08-01: T4a/T4b landed, `moveSubtree` replaced `renameItem`,
and folder rename works. Kept as the record of what that pass deliberately deferred.)*

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
- **Renaming a folder springs its collapsed descendant folders back open, once** (T4b). Same
  cause as the recreated-folder bullet above, reached a different way: a rename changes the id
  of the folder *and every descendant folder*, so any of them the user had manually collapsed
  is unknown to `seenDefaults` under its new id. `resolveExpansion` (`flatten.ts`) filters
  `defaultExpanded` by `seen` alone — confirmed by reading it, not assumed — so each such
  folder is force-expanded exactly once and then behaves normally. `moveSubtree` cannot fix
  this: it faithfully remaps `treeExpanded`, and a *collapsed* folder is precisely the one that
  is absent from that set, so there is nothing there to carry over. The renamed folder itself
  is affected the same way if it was collapsed when renamed. One click each; the clean fix is
  the same server-assigned stable ids, still out of scope.

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
bazel test //ui:test --nocache_test_results                   # vitest; 369 tests as of T6b
bazel build //ui:ui                                           # the real release bundle
```

Run all three. The first one is not optional — it is the gate that catches an unused import or
a wrong generic, and this rewrite hit both. `AGENTS.md`'s claim that the build "catches TS
errors" is true only for syntax and unresolved imports. Pass `--nocache_test_results` when
checking someone else's claimed pass: a cached `PASSED` proves only that the inputs hash-match
an earlier run.

**What the three gates cannot reach.** `vitest.config.ts` runs `environment: "node"` with no
jsdom, deliberately — so nothing here dispatches a real event, computes layout, or reads
`getComputedStyle`. Pure modules (`keymap`, `navigate`, `flatten`, `selection`, the
editable-target guard) are covered directly; row markup is covered structurally via
`renderToStaticMarkup`. These behaviors are therefore not reachable by the suite and need a browser pass. **All of the
below were discharged on 2026-08-01** against a throwaway `HOME` workspace (43 requests, nested
folders) on the production binary; they are kept here as the recipe to re-run, not as debt:

- **`PageUp`/`PageDown` page by one real screen**, not the whole tree (needs a collection tall
  enough to overflow the 278px panel).
- **A rename box keeps its own keystrokes** — `F2`, type a two-word name (the space is the
  tell), reposition with `Home`/arrows, then `Delete`/`cmd+Backspace` mid-edit must edit text
  rather than pop the delete dialog, and commit must not immediately re-enter rename on macOS.
- **`scrollIntoView` keeps the focused row visible** when arrowing past either viewport edge.
- **Modifier clicks hit the right branch.** `cmd`/`ctrl`+click toggles one row without opening
  it or toggling a folder; `shift`+click extends from the anchor. Neither is reachable without
  a real `MouseEvent` carrying modifier state.
- **`cmd+A` does not reach the browser's own Select All**, and `Escape` clears the selection
  without also closing whatever dialog happens to be open.
- **A multi-row delete confirms once and removes every item.** Select a folder plus a sibling
  request, `Delete`, and check the dialog counts 2 (not 5, if the folder has children) and that
  both are gone afterwards. This is the one owed check with a destructive failure mode, so do
  it against a throwaway `HOME` workspace.

Owed for **T5** (right-click needs a real `MouseEvent` with `button`/`ctrlKey`, and the flip
math needs real layout — neither is reachable from a node-environment suite):

- **The menu appears at the pointer and flips.** Right-click a row near the bottom of the
  sidebar; the card must open upward rather than clip. Right-click near the top; it opens
  downward. No flash at the unflipped position (the placement runs in a `useLayoutEffect`).
- **The item list narrows with the selection.** A folder row → New request / New folder /
  Folder metadata / Rename / Delete folder. A request row → Rename / Delete request. Two rows
  (`cmd`+click) → *only* "Delete 2 requests". Empty space below the last row → New request /
  New folder, acting on the root. With no definition source, "New request" is greyed and does
  nothing when clicked.
- **The keyboard drives it, and Escape does not eat the selection.** Right-click a row, then
  `↓`/`↑` to move the highlight (a disabled item must be skipped), `Home`/`End` to jump,
  `Enter` to invoke; `Escape` must close the menu and **leave the selection intact** (the trap
  in §"What T5 settled" #4). After `Escape`, the tree must still be keyboard-drivable —
  arrow keys move the cursor without a fresh click.
- **Menu → Rename does not immediately blur-cancel.** The rename box mounts in the same commit
  the menu unmounts in, and this relies on React running every effect *cleanup*
  (Backdrop's focus-restore-to-`.tree`) before any effect *create* (the box focusing itself).
  If the ordering ever inverts, the box appears and instantly closes.
- **macOS ctrl+click does not open the row.** In Chrome it should behave exactly like a
  right-click (menu, no tab opened). Worth repeating in Firefox if available, since that is the
  browser where the guard actually fires.
- **New folder respects the clicked folder.** Right-click `Alpha` → New folder → the dialog
  title reads "New folder in Alpha", the folder is force-expanded, and the new folder appears
  *inside* `Alpha`, not at the root. The header's own folder button must still create at the
  root.
- **Right-click inside an open rename box shows the NATIVE menu**, not ours, and does not open
  the root menu either.
- **A dialog opened FROM the menu keeps its `autoFocus`.** Right-click a folder → New folder →
  type immediately, without clicking the field. This is the `Backdrop` guard in §"What T5
  settled" #4, and it turns on React's layout-vs-passive effect ordering, which no test here
  can observe. Known nit, not fixed: after a menu-driven **delete** is confirmed, focus lands
  on `<body>` rather than back on the tree — the confirm dialog's captured opener is the
  (long-gone) menu card, so `Backdrop`'s pre-existing `isConnected` guard correctly declines to
  restore it. One click on the tree fixes it.

Owed for **T6b** — a real drag is the single least testable thing in this rewrite. Two
corrections to the assumption this list was written under, both established on 2026-08-01:

1. **`left_click_drag` does NOT start an HTML5 drag.** CDP-synthesised mouse events never
   engage the browser's own gesture recognition, so the harness cannot perform a drag the way
   a user does. The row merely gets selected, which looks exactly like a broken feature.
2. **But the harness CAN drive the handlers.** `new DataTransfer()` is constructible in the
   page, and `new DragEvent(type, {dataTransfer, clientX, clientY})` dispatched on a row
   reaches React's synthetic system. That exercises everything in `Tree.tsx` and `dnd.ts`
   against real layout — `getBoundingClientRect`, the delegated `data-index` lookup, the
   indicator markup, the mutations. **Read the DOM one macrotask later**, not synchronously:
   React 18 batches, so a same-tick read sees the pre-dispatch DOM and every assertion
   silently reports "nothing happened".

Everything below marked ✅ was discharged that way against the production binary on a
throwaway `HOME`. What a synthetic `DragEvent` still cannot reach — and so genuinely needs a
human hand on a real mouse — is only the browser's own gesture layer: the drag ghost, the
no-drop cursor, and **autoscroll** (which needs sustained real pointer movement near an edge).

- ✅ **Into a folder.** Drag a root request onto the MIDDLE of a folder row. The whole row must
  wash accent with a 2px ring (distinguishable from the 1px focus ring), and on release the
  request appears inside the folder — force-expanded if it was collapsed.
- ✅ **Between two rows, and the indent is the point.** Drag a request to the boundary between
  the last child of a folder and the next root row. Aim at the bottom quarter of the child:
  the line must be indented one level (lands *inside* the folder). Aim at the top quarter of
  the root row below: the line must be at the root's indent (lands *outside*). Both are the
  same pixel boundary; only the indent distinguishes them.
- ✅ **The bottom quarter of an EXPANDED folder means "first child".** Drag onto it and check the
  item lands ahead of the folder's existing first child, not after the folder.
- ✅ **A leaf has no middle.** Drag over a request row from top to bottom: only a line at the top
  edge then a line at the bottom edge, never a row wash.
- **Reorder within one folder** (drag a request above its sibling) and confirm the order sticks
  across a reload — this is the pure-reorder path, which touches only the recorded order.
- ✅ **Invalid drops are refused** (no indicator; the native no-drop cursor itself is gesture-layer). Drag a folder onto itself, onto one of its
  own children, and onto its own current position (before itself / before its next sibling):
  no indicator, no cursor change to "move", and nothing happens on release.
- ✅ **Name collision is refused before the RPC.** Two folders each containing a request called
  the same thing; drag one onto the other folder. Must read as invalid rather than appearing to
  work. Repeat with the filter box hiding the colliding sibling — this is the case that
  motivated resolving destination children out of the unfiltered tree.
- ✅ **Multi-row drag.** `cmd`+click three requests, drag from one of them into a folder: all
  three move, in their original top-to-bottom order — and check `folder.json`'s `items` on
  disk, not just the rendered tree, since the order is what §"What T6b settled" #9 sequences
  the calls to guarantee. Then drag from an UNSELECTED row: the selection must collapse to
  that one row and only it moves.
- **Autoscroll.** With a collection tall enough to overflow the 278px panel, drag to within
  ~24px of the panel's top and bottom edges: the list must scroll, and it must **stop** as
  soon as the pointer comes back inside.
- ✅ **Open tabs survive the move.** Open a request, type into its body, invoke it, then drag it
  into another folder. The tab must stay open, keep the draft and the last response, and stay
  active. Repeat by dragging a FOLDER that contains an open request — same expectation for
  every descendant. This is the identity hazard, and its failure mode is silent.
- ✅ **A dragged row reads as in-flight** (dimmed) while the drag is live, and everything is clean
  after a CANCELLED drag (Escape mid-drag, or release outside the panel): no lingering
  indicator, no dimmed rows, and the tree is still keyboard-drivable.

**Two traps in the browser harness itself**, both of which cost time on 2026-08-01 before being
identified — neither is an app bug:

- **The automated tab runs `visibilityState: "hidden"`, so `requestAnimationFrame` never
  fires.** Anything deferred to rAF simply does not happen there. This is why the inline rename
  box appears never to focus (`EditableName.tsx:41` focuses via rAF) and why `Dialog`'s initial
  focus deliberately uses a plain effect instead. To test an rAF-focused input, focus it
  yourself first and then drive the keystrokes. Check `document.visibilityState` before
  concluding a focus bug is real. *(T4b moved the tree's rename box into the component
  (`RenameInput.tsx`), focused by a plain effect, so the TREE is no longer subject to this.
  `EditableName`'s rAF remains a latent issue for its two other callers, `MethodHeader` and
  `ScriptsView` — deliberately left alone rather than changed from inside a tree phase.)*
- **Synthetic keystrokes stop reaching the page** in some states (notably straight after a
  reload, or when the tree was focused by a scripted `.focus()` rather than a real click or
  `Tab`). Symptom: zero `keydown` events even at a `window` capture listener, so the tree looks
  inert. Arm a `window` keydown listener and confirm events arrive before believing a keyboard
  finding; a real mouse click somewhere in the page revives it.

Fold the shipped behavior into `AGENTS.md` and delete this doc when T6 lands, per the
convention used by the request-body and definition-sources tracks.

**Half done, 2026-08-01.** `AGENTS.md` gained a §"The collection tree" describing the
component as it now is — the contract, the two tiers, the flat-array decision, controlled
state, the pure-decision/thin-interpreter split, the keyboard/mouse model, rename, the
context menu, drag and drop, and the `moveSubtree` identity hazard — plus what is still
unbuilt. **This doc is deliberately NOT deleted yet**, for two reasons: T6b needs its
browser pass first (the owed list above), and this file remains the only record of **T3
(typeahead), which was never implemented** — letter keys are unclaimed by `keymap.ts`,
there is no `typeahead.ts`, and the intended behavior (1s buffer, wrap-around, composing
with the filter box rather than replacing it, per §"Deliberate deviations" #3) exists
nowhere else. Whoever deletes this doc must carry T3 — and T7's compact-folders/sticky-
scroll/virtualization notes and T8's async-children rationale — somewhere that survives.
