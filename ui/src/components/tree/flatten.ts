// roots + expansion state -> the ordered array of VISIBLE rows every later
// behavior (keyboard nav, range-select, DnD targeting) indexes instead of
// recursing (plan §"Enduring decisions", #3 — the load-bearing decision that
// also makes virtualization a later, orthogonal choice: docs/design/tree-rewrite-plan.md).
// Pure and DOM-free: only ./types crosses the import boundary, so this module
// has no opinion on React, gRPC, or the DOM — see plan §"Second consumer".

import type { TreeAdapter, TreeRowModel } from "./types";

export interface FlatTree<T> {
  rows: TreeRowModel<T>[];
  indexById: ReadonlyMap<string, number>;
  // Ids of VISIBLE nodes whose getCollapsibleState() is "expanded" but which are
  // not in the caller's `expanded` set. This is how per-node DEFAULT expansion
  // (e.g. a descriptor tree wanting files open, messages closed) reaches the
  // state layer: a node's default never self-applies inside one flatten() pass —
  // see the descent rule below — it is only ever *reported* here so the owner of
  // `expanded` (useTreeState, T1+) can fold these ids in and re-flatten. That may
  // take several passes for a deep default-expanded chain, one level revealed per
  // seed, which is why this is a flat array rather than something flatten() tries
  // to resolve to a fixed point on its own.
  defaultExpanded: string[];
}

// Structural, not `instanceof Promise`: TreeAdapter<T>["getChildren"] only
// promises a `T[] | Promise<T[]>` return, and any thenable is evidence of the
// async path (T8), not specifically the Promise constructor. Typed as a type
// predicate (not just `boolean`) so the `if` below also narrows `children` back
// to `T[]` for the loop that follows it — not merely a documentation nicety.
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

  // parent and parentId travel together rather than one being derived from the
  // other: roots need parentId `null` (per TreeRowModel), and there is no
  // adapter.getId(undefined) call that could produce it.
  const visit = (parent: T | undefined, parentId: string | null, depth: number): void => {
    const children = adapter.getChildren(parent);
    if (isThenable(children)) {
      throw new Error(
        "flatten(): adapter.getChildren() returned a thenable, but flatten() " +
          "implements only the synchronous TreeDataProvider path. The promise " +
          'path is T8 ("Async children", docs/design/tree-rewrite-plan.md) and ' +
          "is not built yet — silently ignoring the promise would drop every " +
          "async node's children rather than fail loudly."
      );
    }

    for (const node of children) {
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
      // Descent is gated on the CALLER's `expanded` set alone — a node's own
      // "expanded" default (below) is never enough by itself, or a default-open
      // subtree would materialize in full on the first render with no way for
      // the state layer to have ever recorded it as open.
      const isExpanded = expandable && inExpandedSet;

      if (collapsibleState === "expanded" && !inExpandedSet) {
        defaultExpanded.push(id);
      }

      indexById.set(id, rows.length);
      rows.push({ node, id, depth, parentId, expandable, expanded: isExpanded });

      if (isExpanded) {
        visit(node, id, depth + 1);
      }
    }
  };

  visit(undefined, null, 0);

  return { rows, indexById, defaultExpanded };
}

// Bound on resolveExpansion's fold-in loop below: guards a malformed (or
// literally unbounded/cyclic) adapter — one whose defaultExpanded keeps
// reporting NEW ids pass after pass with no end — from hanging the UI. A
// well-formed adapter never gets remotely close: a default-expanded CHAIN
// reveals exactly one more level per pass (each pass can only add ids that
// just became VISIBLE by the previous pass's fold-in), so resolution finishes
// in tree-depth passes. Matches Tree.tsx's MAX_REVEAL_DEPTH — the same kind of
// bound for the same kind of risk (a caller-supplied traversal that might not
// terminate on its own).
export const MAX_RESOLVE_PASSES = 1000;

// roots + a caller's `expanded` + previously-seeded ids -> a FULLY resolved flat
// tree, computed synchronously — no effect, no "next render" required. Exists
// because flatten() deliberately never self-applies a node's own default
// expansion (see FlatTree.defaultExpanded's comment above): something has to
// fold defaultExpanded ids into `expanded` and re-flatten, and doing that loop
// HERE, inside one plain function call, is what lets the caller (useTreeState.ts)
// paint a correct first frame instead of "collapsed for one commit, then an
// effect fixes it up" — every populated default-expanded folder used to flash
// shut before springing open on a fresh load.
//
// `seen` is the same bookkeeping useTreeState.ts's seenDefaults ref holds: ids
// ever forced open before, so a user's later manual collapse of one of them is
// never re-forced back open just because it is (once again) visible and absent
// from `expanded`. Subtracting `seen` on every internal pass — not only the
// outermost fold-in — is what keeps a multi-level chain from re-opening a
// collapsed node partway down it.
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
