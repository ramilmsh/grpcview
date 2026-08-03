import { useEffect, useMemo, useRef, useState } from "react";
import type { TreeProps } from "./types";
import { resolveExpansion, type FlatTree } from "./flatten";

// The controlled/uncontrolled seam. expanded/selection/focused are each
// controlled independently; `anchor` is always internal.
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

const EMPTY_EXPANDED: ReadonlySet<string> = new Set();
const EMPTY_SELECTION: readonly string[] = [];

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

  // `!== undefined`, not truthiness and not `??`: `null` is a legitimate
  // controlled value for `focused` ("the host says nothing is focused").
  const expandedControlled = expandedProp !== undefined;
  const selectionControlled = selectionProp !== undefined;
  const focusedControlled = focusedProp !== undefined;

  const [internalExpanded, setInternalExpanded] = useState<ReadonlySet<string>>(EMPTY_EXPANDED);
  const [internalSelection, setInternalSelection] = useState<readonly string[]>(EMPTY_SELECTION);
  const [internalFocused, setInternalFocused] = useState<string | null>(null);
  const [anchor, setAnchor] = useState<string | null>(null);

  const expanded = expandedControlled ? expandedProp : internalExpanded;
  const selection = selectionControlled ? selectionProp : internalSelection;
  const focused = focusedControlled ? focusedProp : internalFocused;

  // Each setter writes to exactly one place — the callback when controlled,
  // internal state otherwise — never both.
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

  // Default expansion is resolved synchronously here rather than in an effect, so
  // the first frame already reflects it instead of flashing collapsed. Guarding on
  // "ever seeded" rather than on `expanded` membership is what lets a manual
  // collapse of a default-expanded node stick. Safe to read the ref during render
  // because only the effect below ever writes it.
  const seenDefaults = useRef<Set<string>>(new Set());
  const { flat, seeded } = useMemo(
    () => resolveExpansion(adapter, expanded, seenDefaults.current),
    [adapter, expanded]
  );

  useEffect(() => {
    // Bookkeeping only — `seeded` is already folded into `flat` above. seenDefaults
    // is never pruned, so a folder recreated (or renamed) into an id that was
    // collapsed before is born expanded once.
    if (seeded.length === 0) return;
    for (const id of seeded) seenDefaults.current.add(id);
    setExpanded(new Set([...expanded, ...seeded]));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [seeded]);

  return { flat, expanded, setExpanded, selection, setSelection, focused, setFocused, anchor, setAnchor };
}
