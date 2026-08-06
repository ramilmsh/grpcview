import type { TreeAdapter, TreeRowModel } from "./types";

export interface FlatTree<T> {
  rows: TreeRowModel<T>[];
  indexById: ReadonlyMap<string, number>;
  // Visible ids whose adapter state is "expanded" but that are not in the
  // caller's `expanded` set. Reported here, never applied by flatten() itself.
  defaultExpanded: string[];
}

function isThenable(value: unknown): value is PromiseLike<unknown> {
  return typeof (value as { then?: unknown }).then === "function";
}

export function flatten<T>(
  adapter: TreeAdapter<T>,
  expanded: ReadonlySet<string>
): FlatTree<T> {
  const rows: TreeRowModel<T>[] = [];
  const indexById = new Map<string, number>();
  const defaultExpanded: string[] = [];

  const visit = (parent: T | undefined, parentId: string | null, depth: number): void => {
    const children = adapter.getChildren(parent);
    if (isThenable(children)) {
      throw new Error(
        "flatten(): adapter.getChildren() returned a thenable, but flatten() " +
          "implements only the synchronous TreeDataProvider path. The promise " +
          'path is T8 ("Async children", docs/design/shipped/tree-rewrite-plan.md) and ' +
          "is not built yet — silently ignoring the promise would drop every " +
          "async node's children rather than fail loudly."
      );
    }

    for (let i = 0; i < children.length; i++) {
      const node = children[i];
      const id = adapter.getId(node);
      const seenAt = indexById.get(id);
      if (seenAt !== undefined) {
        throw new Error(
          `flatten(): duplicate tree id ${JSON.stringify(id)} — ` +
            `"${adapter.getTypeaheadLabel(rows[seenAt].node)}" and ` +
            `"${adapter.getTypeaheadLabel(node)}" both resolve to it. Ids must be ` +
            "unique across an entire pass, not just among siblings: indexById and " +
            "React keys are both flat over the whole tree."
        );
      }

      const collapsibleState = adapter.getCollapsibleState(node);
      const expandable = collapsibleState !== "none";
      const inExpandedSet = expanded.has(id);
      const isExpanded = expandable && inExpandedSet;

      if (collapsibleState === "expanded" && !inExpandedSet) {
        defaultExpanded.push(id);
      }

      indexById.set(id, rows.length);
      rows.push({
        node,
        id,
        depth,
        parentId,
        expandable,
        expanded: isExpanded,
        posInSet: i + 1,
        setSize: children.length,
      });

      if (isExpanded) {
        visit(node, id, depth + 1);
      }
    }
  };

  visit(undefined, null, 0);

  return { rows, indexById, defaultExpanded };
}

// Bounds the fold-in loop below against a malformed or cyclic adapter.
export const MAX_RESOLVE_PASSES = 1000;

// Folds defaultExpanded ids into `expanded` and re-flattens until stable, so the
// caller can paint a correct first frame. `seen` is the ids ever seeded before,
// so a later manual collapse is never forced back open.
export function resolveExpansion<T>(
  adapter: TreeAdapter<T>,
  expanded: ReadonlySet<string>,
  seen: ReadonlySet<string>
): { flat: FlatTree<T>; seeded: string[] } {
  let working = expanded;
  let flat = flatten(adapter, working);
  const seeded: string[] = [];

  for (let pass = 0; pass < MAX_RESOLVE_PASSES; pass++) {
    const toFold = flat.defaultExpanded.filter((id) => !seen.has(id));
    if (toFold.length === 0) break;
    seeded.push(...toFold);
    working = new Set([...working, ...toFold]);
    flat = flatten(adapter, working);
  }

  return { flat, seeded };
}
