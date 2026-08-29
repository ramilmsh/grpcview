# The collection tree

A hand-rolled, domain-agnostic tree — `features/workspace/` supplies the gRPC half.

- One contract, two row tiers: `TreeAdapter<T>` gives the **portable** tier (default row);
  `renderRow` opts into the **rich** tier (arbitrary React per row) — the request tree is
  rich.
- The flat visible-rows array is load-bearing: `flatten.ts` reduces roots+expanded to an
  ordered array + id→index map; every behavior (arrow keys, range select, drop targeting) is
  array arithmetic over it, never recursion. State is zustand (`treeExpanded`/
  `treeSelection`/`treeFocused`); decisions are pure functions (`keymap.ts`, `dispatch.ts`,
  `navigate.ts`, `dnd.ts`), `Tree.tsx` a thin interpreter.
- Rename is the component's — validates against visible siblings, server stays collision
  authority. Context menu is the host's. DnD is native HTML5: a folder row splits into
  quarters (outer = between-rows, middle half = into), a leaf splits in half. A multi-row
  move is **sequenced** (each call chained off the previous `onSuccess`) — order becomes
  persisted sibling order.
- **Identity hazard: `itemKey` is path+name derived** — rename/move changes an item's key
  (and descendants'). Any such mutation must call `moveSubtree(oldKey, newKey, newName)`,
  the one remapper of every keyed UI-store field.
- Not built: typeahead, compact folders, sticky scroll, virtualization, async
  `getChildren`.

Background: [`docs/design/shipped/tree-rewrite-plan.md`](../../../../docs/design/shipped/tree-rewrite-plan.md).
