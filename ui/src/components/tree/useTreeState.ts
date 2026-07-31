import { useEffect, useMemo, useRef, useState } from "react";
import type { TreeProps } from "./types";
import { resolveExpansion, type FlatTree } from "./flatten";

// The controlled/uncontrolled seam (tree-rewrite-plan.md's "Enduring decisions",
// #5: state is controlled, owned by zustand in grpcview, but the component also
// supports uncontrolled use for reuse — e.g. a standalone descriptor-explorer
// preview with no store wired up). expanded/selection/focused are each controlled
// independently of one another; `anchor` has no prop pair at all — see below.
export interface TreeState<T> {
  flat: FlatTree<T>;
  expanded: ReadonlySet<string>;
  setExpanded(next: ReadonlySet<string>): void;
  selection: readonly string[];
  setSelection(next: readonly string[]): void;
  focused: string | null;
  setFocused(next: string | null): void;
  anchor: string | null;
  setAnchor(next: string | null): void;
}

// Shared, never mutated in place — every update below builds a NEW Set/array
// (`new Set([...])`, `[...current, id]`, …), so one empty singleton is safe as
// the initial value across every uncontrolled hook instance.
const EMPTY_EXPANDED: ReadonlySet<string> = new Set();
const EMPTY_SELECTION: readonly string[] = [];

// seenDefaults is deliberately never pruned. See the KNOWN LIMITATION note at the
// effect below for the one behavior that costs, and why paying it beats the fix.

export function useTreeState<T>(props: TreeProps<T>): TreeState<T> {
  const {
    adapter,
    expanded: expandedProp,
    onExpandedChange,
    selection: selectionProp,
    onSelectionChange,
    focused: focusedProp,
    onFocusedChange,
  } = props;

  // The usual React convention: a pair is "controlled" when its value prop is
  // !== undefined, independent of whether the on*Change callback is also wired.
  // This matters more than usual for `focused`, which is `string | null` rather
  // than `string`: `null` is a legitimate CONTROLLED value ("host says nothing is
  // focused"), so testing truthiness — or using `??`, which treats null and
  // undefined alike — would wrongly fall back to internal state whenever a
  // controlled host explicitly focused nothing. `!== undefined` is the only check
  // that keeps "omitted" and "controlled-to-null" apart.
  const expandedControlled = expandedProp !== undefined;
  const selectionControlled = selectionProp !== undefined;
  const focusedControlled = focusedProp !== undefined;

  const [internalExpanded, setInternalExpanded] = useState<ReadonlySet<string>>(EMPTY_EXPANDED);
  const [internalSelection, setInternalSelection] = useState<readonly string[]>(EMPTY_SELECTION);
  const [internalFocused, setInternalFocused] = useState<string | null>(null);
  // anchor is the range-select pivot (selection.ts's rangeSelection, wired at T2)
  // — an implementation detail of THIS component, never something a host reads or
  // drives. TreeProps has no anchor/onAnchorChange pair, so unlike the three
  // above it is unconditionally internal: no controlled branch, ever.
  //
  // RENAME SURVIVAL (checked explicitly, T2): unlike selection/focused/expanded,
  // this state has NO rekeying path at all when a row's id changes out from
  // under it — ui-store.ts's moveSubtree remaps treeSelection/treeFocused/
  // treeExpanded (all controlled, all store-owned), but the anchor is neither
  // controlled nor store-owned, so a rename leaves it holding the OLD
  // (now-nonexistent) id with nothing to notice or fix it. Deliberately NOT
  // given a controlled pair to fix this: TreeProps has no consumer that has
  // ever needed to read or drive the anchor (the plan's own "over-fitting"
  // risk — "name the consumer that wants it" — has no answer here), so adding
  // one now would grow the public contract for a problem that already
  // degrades gracefully on its own. The actual consequence of a stale anchor
  // is mild by construction, not a new gap this phase introduces: rangeSelection
  // (selection.ts) already treats "anchor id not found in the current rows" as
  // "no anchor" and degrades to a single-row selection — the EXACT same branch
  // a row hidden by CollectionPanel's filter box already exercises today
  // (selection.test.ts's "degenerates to just the focus row when the anchor id
  // is missing from rows") — and it self-heals on the very next PLAIN move
  // regardless, since applyIntent's "move" case (dispatch.ts) unconditionally
  // resets the anchor to wherever focus lands. Net effect of a rename: at most
  // one shift+arrow/shift+click, immediately after, extends from "nowhere"
  // instead of the old pivot — never a crash, a phantom row, or a wrong
  // selection.
  const [anchor, setAnchor] = useState<string | null>(null);

  const expanded = expandedControlled ? expandedProp : internalExpanded;
  const selection = selectionControlled ? selectionProp : internalSelection;
  const focused = focusedControlled ? focusedProp : internalFocused;

  // Each setter writes to exactly one place — the callback when controlled,
  // internal state otherwise — never both. Writing both would mean a controlled
  // host's internal state silently drifts from what it's actually rendering, and
  // an uncontrolled caller would have a stray callback invoked that it never
  // opted into by supplying one.
  const setExpanded = (next: ReadonlySet<string>): void => {
    if (expandedControlled) onExpandedChange?.(next);
    else setInternalExpanded(next);
  };
  const setSelection = (next: readonly string[]): void => {
    if (selectionControlled) onSelectionChange?.(next);
    else setInternalSelection(next);
  };
  const setFocused = (next: string | null): void => {
    if (focusedControlled) onFocusedChange?.(next);
    else setInternalFocused(next);
  };

  // DEFAULT-EXPANSION SEEDING, resolved SYNCHRONOUSLY. flatten() *reports*
  // defaultExpanded — ids of visible nodes whose adapter says "expanded" but that
  // aren't in `expanded` yet — without ever applying it: descent inside flatten()
  // is gated on the caller's `expanded` set alone (see flatten.ts), so something
  // above it has to fold these ids in. resolveExpansion is that something, and it
  // does the whole fold-in loop — however many levels a default-expanded chain
  // has — inside ONE synchronous call, right here in the render body. That is
  // what fixes the first-paint flash the old (effect-based) design had: on a
  // fresh load `expanded` starts empty, so an effect-driven seed would paint one
  // all-collapsed frame before ever running, and every populated folder would
  // visibly flash shut and spring open. Computing the resolved tree during render
  // instead means the FIRST frame already reflects it — there is no "before".
  //
  // The guard is a REF of ids ever seeded (`seenDefaults`), not membership in the
  // `expanded` set itself. Those sound interchangeable ("is this id currently
  // expanded?") but they are not: flatten() keeps reporting an id in
  // `defaultExpanded` for as long as it is visible and absent from `expanded` —
  // and a user COLLAPSING a default-expanded node does exactly that, removing it
  // from `expanded` while leaving it visible. Guarding on `expanded` membership
  // would see that id "missing" again on the very next render and force it
  // straight back open — the collapse could never stick, fighting the user
  // forever. Guarding on "has this id ever been seeded" instead means each id is
  // forced open AT MOST ONCE, full stop; a later manual collapse is never
  // second-guessed, because the id is already marked seen and gets filtered out
  // of every future resolution regardless of whether `expanded` currently
  // contains it.
  //
  // This is also precisely what fixes the bug the plan cites at TreeView.tsx:45:
  // that component's `useState(true)` lives PER NODE, so a node that unmounts
  // (its parent folder collapses, unrendering it) and later remounts (the parent
  // re-expands) runs a fresh `useState(true)` and has no memory of any collapse
  // the user made before it disappeared. Lifting `expanded` up here already fixes
  // that — it isn't tied to a row's mount lifetime — but only as long as nothing
  // immediately undoes the fix by re-forcing every "default-expanded but not
  // currently expanded" id back open, which would just be the unmount bug again
  // wearing a different hat. The seen-ref is what actually breaks that cycle.
  //
  // Reading seenDefaults.current DURING RENDER (via useMemo below) is the one
  // unusual part: React's own guidance is to avoid reading a ref in the render
  // body. It's safe here specifically because this render never WRITES the ref —
  // only the effect further down does, strictly after commit — so within any
  // single render this value cannot change out from under it; resolveExpansion
  // is a pure function of whatever seenDefaults.current happens to hold at the
  // moment it's called, and the effect's job is solely to keep that ref (and the
  // controlled `expanded` store) converged with what this render already decided.
  const seenDefaults = useRef<Set<string>>(new Set());
  const { flat, seeded } = useMemo(
    () => resolveExpansion(adapter, expanded, seenDefaults.current),
    [adapter, expanded]
  );

  useEffect(() => {
    // KNOWN LIMITATION, accepted deliberately: seenDefaults is never pruned, so an
    // id stays "already seeded" for this Tree's whole mounted life. Because itemKey
    // is path+name derived (format.ts's keyOf), a folder that is collapsed, deleted,
    // and later RECREATED with the same name at the same path is the literal same
    // id — so it is born collapsed rather than expanded like every other new folder.
    // One click fixes it.
    //
    // T4b adds a second face of the SAME limitation, now that folders can be
    // renamed: a rename changes the id of the folder and of every descendant
    // folder, so any of them the user had manually COLLAPSED is unknown to
    // seenDefaults under its new id, and resolveExpansion (flatten.ts — it filters
    // defaultExpanded by `seen` alone) force-expands it exactly once. Net effect:
    // rename a folder, and collapsed folders inside it spring back open one time.
    // Also one click each, same cause, same clean fix below.
    //
    // A reconciliation pass (walk the adapter, forget ids that no longer exist) was
    // built and then reverted, because it cost far more than the bug: it needed the
    // FULL tree, while a caller may legitimately narrow what it passes (this app's
    // filter box does), so it required a second "unfiltered" adapter threaded
    // through the shared contract as an extra prop — a prop the descriptor explorer
    // would then also have to understand, which the plan's own "over-fitting" risk
    // warns against. Reconciling against the NARROWED adapter instead is actively
    // wrong: it mistakes "hidden by the filter" for "deleted", so filtering would
    // forget a manually-collapsed folder and clearing the filter would spring it
    // back open — a worse bug than the one being fixed, and one that adversarial
    // review actually caught in that implementation.
    //
    // The clean fix is server-assigned stable item ids, which the plan discusses
    // under "The identity hazard" and puts out of scope.

    // `seeded` is exactly what this render's resolveExpansion call already folded
    // into the `flat` above — painting already happened. This effect's only
    // remaining job is bookkeeping: record the ids as seen (so a later manual
    // collapse of any of them is never re-forced open) and push the same union
    // into the STORE's `expanded`, so a controlled host (treeExpanded in
    // ui-store.ts) converges to match what the screen already shows. Nothing
    // here changes what was already rendered.
    if (seeded.length === 0) return;
    for (const id of seeded) seenDefaults.current.add(id);
    setExpanded(new Set([...expanded, ...seeded]));
    // `expanded` (read here to build the merged set) and the setters (recreated
    // every render as plain closures, never memoized) are deliberately left out of
    // the dependency list: correctness comes from seenDefaults + `seeded` itself,
    // not from how often this effect re-runs — an extra run finds nothing left to
    // seed and returns. `seeded` (via the useMemo above, which recomputes whenever
    // `adapter` or `expanded` changes) is the one value that changes whenever there
    // is new seeding work to do.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [seeded]);

  return { flat, expanded, setExpanded, selection, setSelection, focused, setFocused, anchor, setAnchor };
}
