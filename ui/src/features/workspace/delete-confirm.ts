// Wording for CollectionPanel's delete-confirm dialog. Its own module so it is testable:
// CollectionPanel pulls in `monaco-editor`, unresolvable under the sandboxed test run.
import type { ItemWithPath } from "@/lib/format";

export interface DeleteConfirmCopy {
  title: string;
  emphasis: string;
  suffix: string;
}

// `items` must be the ALREADY-PRUNED selection (lib/format.ts's pruneNestedSelections), so the
// count reported matches the mutations doDelete will actually fire.
export function deleteConfirmCopy(
  items: readonly ItemWithPath[],
): DeleteConfirmCopy {
  if (items.length === 0) {
    return { title: "Delete", emphasis: "nothing", suffix: "?" };
  }

  if (items.length === 1) {
    const [item] = items;
    const folder = item.item.content.case === "folder";
    return {
      title: folder ? "Delete folder" : "Delete request",
      emphasis: item.item.name,
      suffix: folder ? " and everything inside it?" : "?",
    };
  }

  const folderCount = items.filter(
    (it) => it.item.content.case === "folder",
  ).length;
  const allFolders = folderCount === items.length;
  const noFolders = folderCount === 0;
  const noun = allFolders ? "folders" : noFolders ? "requests" : "items";
  const nounPhrase = `${items.length} ${noun}`;
  return {
    title: `Delete ${nounPhrase}`,
    emphasis: nounPhrase,
    suffix: allFolders
      ? " and everything inside them?"
      : noFolders
        ? "?"
        : "? Folders in the selection will be deleted along with everything inside them.",
  };
}
