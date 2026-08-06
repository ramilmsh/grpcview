// The TopBar collection picker's items. Its own module for the same reason collection-menu.ts
// is: a pure function is testable, and TopBar itself is not (it reads the transport).
import type { MenuItem } from "@/components/ui/Menu";
import type { CollectionSummary } from "@grpcview/v1/service_pb";

export interface CollectionSwitcherActions {
  select(id: string): void;
  rename(): void;
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
  // Last and behind one separator: these write to the repo, the rows above them only switch
  // views. Rename comes first because it acts on the active collection, whose row it sits
  // under — and it is absent without one, since there would be nothing to rename.
  const writes: MenuItem[] = [];
  if (activeId !== null) writes.push({ label: "Rename collection…", onSelect: actions.rename });
  writes.push({ label: "New collection…", onSelect: actions.createNew });
  return [
    ...rows,
    ...writes.map((item, i) => ({ ...item, separatorBefore: i === 0 && rows.length > 0 })),
  ];
}
