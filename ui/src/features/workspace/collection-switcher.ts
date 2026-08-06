// The TopBar collection picker's items. Its own module for the same reason collection-menu.ts
// is: a pure function is testable, and TopBar itself is not (it reads the transport).
import type { MenuItem } from "@/components/ui/Menu";
import type { CollectionSummary } from "@grpcview/v1/service_pb";

export interface CollectionSwitcherActions {
  select(id: string): void;
  createNew(): void;
}

// The name, with the id appended ONLY when another collection shares that name: a collection
// is addressed by its path and named separately, so five may all be called "requests" and a
// row labelled by name alone would be one of five identical rows. The active one is marked
// rather than omitted — VS Code's rule, so the list never changes length as you switch.
export function collectionSwitcherLabel(
  collection: CollectionSummary,
  collections: readonly CollectionSummary[],
  activeId: string | null
): string {
  const ambiguous = collections.some((o) => o.id !== collection.id && o.name === collection.name);
  const suffix = ambiguous ? ` — ${collection.id}` : "";
  return `${activeId === collection.id ? "● " : "   "}${collection.name}${suffix}`;
}

export function collectionSwitcherItems(
  collections: readonly CollectionSummary[],
  activeId: string | null,
  actions: CollectionSwitcherActions
): MenuItem[] {
  const rows: MenuItem[] = collections.map((collection) => ({
    label: collectionSwitcherLabel(collection, collections, activeId),
    onSelect: () => actions.select(collection.id),
  }));
  // Last and behind a separator: it writes to the repo, the rows above it only switch views.
  return [
    ...rows,
    { label: "New collection…", separatorBefore: rows.length > 0, onSelect: actions.createNew },
  ];
}
