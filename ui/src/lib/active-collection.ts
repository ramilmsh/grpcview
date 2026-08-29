// Which collection the scoped views address. Split out of workspace-query.ts as a pure
// function of (stored choice, live listing) so it is testable without a renderer or a
// transport; useActiveCollectionId is the two-line hook that feeds it both.
//
// Deliberately does NOT write the stored value back. A stored id that has disappeared
// from the workspace stays stored, so re-adding that collection re-selects it instead
// of leaving the user parked on whatever happened to be first.
export function resolveActiveCollection(
  stored: string | null,
  collections: readonly { id: string }[],
): string | null {
  if (stored && collections.some((c) => c.id === stored)) return stored;
  return collections[0]?.id ?? null;
}
