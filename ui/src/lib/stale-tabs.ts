// Which open tabs name an item its collection no longer holds — deleted behind the UI's back
// (CLI, MCP, another browser tab). Pure, so the "only once it has loaded" rule is testable.
import { findByKey, type ItemWithPath } from "./format";
import type { OpenTab } from "./ui-store";

// `loaded` holds ONLY collections whose items have actually landed: a collection absent from
// it (not fetched, loading, errored) says nothing, so its tabs stay. Only item-backed tabs
// (request, folder) can go stale; the "New request" draft names no item.
export function staleTabKeys(
  tabs: readonly OpenTab[],
  loaded: ReadonlyMap<string, ItemWithPath[]>,
): string[] {
  return tabs
    .filter((t) => {
      if (t.kind !== "request" && t.kind !== "folder") return false;
      const items = loaded.get(t.collection);
      return items !== undefined && findByKey(items, t.key) === null;
    })
    .map((t) => t.key);
}
