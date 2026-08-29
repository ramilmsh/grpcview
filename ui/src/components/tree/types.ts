import type { Ref, ReactNode } from "react";

// The tree component's data contract, shared by every module under components/tree/.

export interface TreeAdapter<T> {
  getId(node: T): string;

  // `undefined` => roots. The promise path is not implemented yet.
  getChildren(node?: T): T[] | Promise<T[]>;

  getCollapsibleState(node: T): "none" | "collapsed" | "expanded";

  getParent?(node: T): T | undefined;

  // Renderable by both this component's default renderer and a VS Code TreeItem.
  getTreeItem(node: T): TreeItemLike;

  getTypeaheadLabel(node: T): string;
}

// Structurally identical to TreeAdapter<T>; using it asserts portability by intent.
export type PortableTreeAdapter<T> = TreeAdapter<T>;

export interface TreeItemLike {
  label: string;
  description?: string; // dimmed trailing text
  icon?: IconToken;
  tooltip?: string;
  kind?: string;
}

// Names match VS Code's codicons, which is the point of the token layer: a portable
// provider picks a token here and a VS Code TreeItem resolves the same name.
export type IconToken =
  | "folder"
  | "root-folder"
  | "file"
  | "symbol-class"
  | "symbol-enum"
  | "symbol-field"
  | "symbol-method";

export interface TreeHandle<T> {
  reveal(
    id: string,
    opts?: { select?: boolean; focus?: boolean; expand?: boolean },
  ): void;
  invalidate(node?: T): void;
  // No-op for an id that names no current row.
  startRename(id: string): void;
}

export interface TreeRowState {
  focused: boolean;
  selected: boolean;
  active: boolean; // the row matching TreeProps.activeId
  expanded: boolean;
  depth: number;
  renaming: boolean;
  dropTarget: "into" | "before" | "after" | null;
}

export interface TreeProps<T> {
  adapter: TreeAdapter<T>;
  handle?: Ref<TreeHandle<T>>;
  // Rich tier: overrides getTreeItem per row, and makes the tree standalone-only.
  // Returning null/undefined DECLINES that row back to the portable getTreeItem
  // renderer — which is how a mixed tree keeps one tier portable (and so usable by a
  // VS Code TreeProvider) while another tier renders arbitrary React.
  renderRow?(node: T, state: TreeRowState): ReactNode;

  // Controlled state — omit any pair to fall back to internal state.
  expanded?: ReadonlySet<string>;
  onExpandedChange?(next: ReadonlySet<string>): void;
  selection?: readonly string[];
  onSelectionChange?(next: readonly string[]): void;
  focused?: string | null;
  onFocusedChange?(next: string | null): void;

  // Independent of selection/focus, and purely an input: the tree never changes it.
  activeId?: string | null;

  onOpen?(node: T): void;
  // Fires only for a name that is non-blank, changed, and collision-free.
  onRenameCommit?(node: T, next: string): void;
  onDelete?(nodes: T[]): void;
  onMove?(nodes: T[], to: { parent: T | null; before?: T }): void;
  onContextMenu?(nodes: T[], ev: React.MouseEvent): void;
  canDrop?(dragged: T[], to: { parent: T | null; before?: T }): boolean;

  indent?: number; // default 8
  rowHeight?: number; // default 22
  compactFolders?: boolean;
  "aria-label"?: string;
}

export interface TreeRowModel<T> {
  node: T;
  id: string;
  depth: number;
  parentId: string | null;
  expandable: boolean;
  expanded: boolean;
  // ARIA position within this row's VISIBLE siblings — the flat-DOM substitute
  // for a role="group" per expanded folder.
  posInSet: number;
  setSize: number;
}
