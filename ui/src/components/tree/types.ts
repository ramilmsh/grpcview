import type { Ref, ReactNode } from "react";

// The tree component's data contract (docs/design/tree-rewrite-plan.md
// §"The component contract"). Every module under components/tree/ imports from
// here, and so will any future tree caller — the request collection today, the
// descriptor explorer next (plan §"Second consumer"). This file is a straight
// transcription of that section of the plan: treat the plan as the source of truth
// for changes, not this file in isolation.

export interface TreeAdapter<T> {
  getId(node: T): string; // stable within a render pass

  // TreeDataProvider.getChildren shape: `undefined` => roots. May return a promise;
  // T0 implements the SYNCHRONOUS path only (see "Second consumer") — the promise
  // path is T8 ("Async children"). The signature has to be right from day one even
  // though nothing awaits it yet.
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

// Portability marker (plan §Risks, "Portability rot"). Declaring a provider's type as
// PortableTreeAdapter<T> instead of TreeAdapter<T> asserts it is portable BY INTENT.
// The alias is structurally identical to TreeAdapter<T> — it adds no field or method
// of its own — so it is documentation the compiler cannot enforce on its own, not a
// guardrail. Portability additionally requires, as conventions this alias cannot check:
//   (a) the provider is never handed to a tree's `renderRow` (see TreeProps below) —
//       that prop is what opts a tree into the rich, standalone-only tier;
//   (b) getTreeItem().icon only ever names an IconToken, never an ad-hoc string;
//   (c) getTreeItem().kind sticks to one enumerated, closed vocabulary of values for
//       that provider. Note TreeItemLike.kind below is typed as plain `string`, so
//       this alias cannot enforce (c) even in principle — see the comment on that
//       field.
export type PortableTreeAdapter<T> = TreeAdapter<T>;

export interface TreeItemLike {
  label: string;
  description?: string;   // dimmed trailing text, e.g. a folder's request count
  icon?: IconToken;       // abstract; each renderer maps it (codicon vs Phosphor)
  tooltip?: string;
  // Abstract node kind. VS Code maps it to package.json menu `when` clauses; our T5
  // context menu keys off the same value. NOT a free-form string — enumerate it.
  kind?: string;
}

// Fixed vocabulary, extended deliberately. Ad-hoc strings break portability silently.
export type IconToken =
  | "folder" | "file"
  | "symbol-class" | "symbol-enum" | "symbol-field" | "symbol-method";

// Imperative handle, for the things that are actions rather than state.
export interface TreeHandle<T> {
  reveal(id: string, opts?: { select?: boolean; focus?: boolean; expand?: boolean }): void;
  invalidate(node?: T): void;   // onDidChangeTreeData equivalent; no-op while sync
}

export interface TreeRowState {
  focused: boolean;
  selected: boolean;
  // True for the row matching TreeProps.activeId — e.g. VS Code's Explorer
  // highlighting the file behind the active editor tab. A fourth independent flag
  // alongside focused/selected (see the comment on activeId below for why it isn't
  // folded into either).
  active: boolean;
  expanded: boolean;
  depth: number;
  renaming: boolean;
  dropTarget: "into" | "before" | "after" | null;
}

export interface TreeProps<T> {
  adapter: TreeAdapter<T>;              // roots come from getChildren(undefined)
  handle?: Ref<TreeHandle<T>>;
  // RICH tier, optional: overrides the default declarative renderer built from
  // getTreeItem. Supplying this makes the tree standalone-only — see "Second consumer".
  renderRow?(node: T, state: TreeRowState): ReactNode;

  // Controlled state — omit any pair to fall back to internal state.
  expanded?: ReadonlySet<string>;  onExpandedChange?(next: ReadonlySet<string>): void;
  selection?: readonly string[];   onSelectionChange?(next: readonly string[]): void;
  focused?: string | null;         onFocusedChange?(next: string | null): void;

  // The row whose id === activeId gets TreeRowState.active, painted via the
  // existing `.treerow.on` styling (app-tokens.css). Independent of
  // selection/focus — grpcview's own "activeKey" is the open TAB, a concept this
  // component knows nothing about (enduring decision 1), but "the row for
  // whatever's active elsewhere in the app" is not gRPC-specific — VS Code's own
  // Explorer highlights the row of the active editor the same way. Purely an
  // input: unlike expanded/selection/focused there is no onActiveIdChange, since
  // the tree neither owns nor changes it.
  activeId?: string | null;

  // T0 BRIDGE, not part of the enduring contract above (same shape as activeId —
  // plain input, no onChange — and same reason): which row, if any, is mid-rename.
  // Full F2/keyboard rename is T4b, but the host's EXISTING rename affordance
  // (today: CollectionPanel's pencil -> EditableName, one row at a time) already
  // exists and must keep working, which is what T0 preserves. Without this, the
  // tree can't reproduce the pre-rewrite TreeView.tsx's row-level
  // `onClick={editing ? undefined : ...}` guard: EditableName's <input> only
  // stops propagation for clicks on itself, so anything else in the row (e.g. a
  // MethodKindTag, plain content with no handler of its own) would otherwise
  // select/focus/open the row out from under an in-progress rename. Feeds both
  // TreeRowState.renaming (for a rich renderRow that wants to vary its own look)
  // and the click guard itself (Tree.tsx's handleRowClick). The component does
  // not yet own the edit UI (onRenameCommit below says the CALLER does) — so the
  // caller is also the one tracking "which row", and hands it back in here; once
  // T4b lands and the component takes over rename, this prop disappears and folds
  // into internal state.
  renamingId?: string | null;

  // T1's half of the renamingId bridge: how a rename gets REQUESTED in the first
  // place from the keyboard (F2, or Enter on macOS — keymap.ts's "rename"
  // intent), now that there's no per-row useState/onClick for Tree.tsx's
  // handleKeyDown to reach into. Same shape as renamingId itself (plain input
  // there, plain output here — no round trip through internal state), and the
  // same T0/T4b bridge: the HOST still owns "which row, if any" and decides
  // whether a given id is even renamable (e.g. CollectionPanel says no for a
  // folder row today, since UpdateFolderRequest has no name field yet — T4a).
  // Once T4b lands and the component owns the edit UI outright, this and
  // renamingId both fold into internal state and disappear together.
  onRenamingChange?(id: string | null): void;

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

// ── the flattened row model ─────────────────────────────────────────────────────
// What flatten.ts produces — roots + expansion state reduced to an ordered array of
// visible rows — and what TreeRow.tsx consumes to render one row. Lives here, not in
// either module, because neither owns it.
export interface TreeRowModel<T> {
  node: T;
  id: string;
  depth: number;
  parentId: string | null;
  expandable: boolean;   // collapsibleState !== "none"
  expanded: boolean;     // expandable && id in the expanded set

  // ARIA position/size within this row's SET — mirrors monaco's own tree widget
  // exactly (verified in the vendored sources, ui/node_modules/monaco-editor):
  //   - listView.js:592-593 sets BOTH aria-setsize/aria-posinset on every row.
  //   - abstractTree.js:137-146 defines the semantics this implements:
  //       getSetSize(node)  => parentNode.visibleChildrenCount
  //       getPosInSet(node) => node.visibleChildIndex + 1
  //     (abstractTree.js:1173-1176 is where they get written onto the element).
  // "Set" means this row's VISIBLE siblings — the other rows sharing its
  // parentId below, in THIS flattened array — never every child
  // adapter.getChildren() could in principle return: a collapsed folder's
  // children are not part of anyone's set because they are not rows at all.
  // Our rows are a flat list of role="treeitem" divs, all siblings directly
  // under one role="tree" container, with no role="group" wrapper per expanded
  // folder — these two attributes are the flat-DOM substitute for that
  // nesting: without them, a browser synthesizing a position from flat DOM
  // order would count across the WHOLE visible tree ("5 of 8") instead of
  // within the parent's actual children ("3 of 5").
  posInSet: number;  // 1-based index among this row's visible siblings
  setSize: number;   // count of this row's visible siblings (itself included)
}
