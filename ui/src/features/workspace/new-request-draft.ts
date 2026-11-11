// The pure half of the "New request" draft (its tree row and its page): what it is
// called, where it lands, whether it collides, and when it may be created. Kept apart
// from the components so it is testable without a transport.

export const DRAFT_TITLE = "New request";

// The tab label and page title: the typed name, or the placeholder while there is none.
export const draftTitle = (name: string): string => name.trim() || DRAFT_TITLE;

// Picking a method names the draft after it only while the name is still empty.
export const nameAfterPick = (name: string, method: string): string =>
  name.trim() === "" ? method : name;

interface ItemLike {
  item: { name: string; content: { case: string | undefined } };
  children?: ItemLike[];
}

// The names already taken in the folder at display-name `path` ([] = the collection
// root), or null when that folder is not in `roots` (deleted or renamed meanwhile).
export function siblingNames(
  roots: readonly ItemLike[],
  path: readonly string[],
): string[] | null {
  let items: readonly ItemLike[] = roots;
  for (const segment of path) {
    const folder = items.find(
      (it) => it.item.name === segment && it.item.content.case === "folder",
    );
    if (!folder) return null;
    items = folder.children ?? [];
  }
  return items.map((it) => it.item.name);
}

// Presentation only, off the already-loaded tree: the server's AlreadyExists stays the
// authority. Same rule as the store's findByName — an exact match on the trimmed name.
export const nameCollides = (
  siblings: readonly string[],
  name: string,
): boolean => {
  const n = name.trim();
  return n !== "" && siblings.includes(n);
};

// The folder the draft sits in, by name; the collection's own name at its root.
export const folderLabel = (
  collectionName: string,
  path: readonly string[],
): string => path[path.length - 1] ?? collectionName;

export const collisionMessage = (name: string, folder: string): string =>
  `A request named ${name.trim()} already exists in ${folder}.`;

export function canCreate(d: {
  name: string;
  method: unknown;
  collides: boolean;
  pending: boolean;
}): boolean {
  return d.name.trim() !== "" && d.method !== null && !d.collides && !d.pending;
}
