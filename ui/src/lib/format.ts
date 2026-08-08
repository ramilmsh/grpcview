import type { Item, Service, Method } from "@grpcview/v1/workspace_pb";
import type { Duration, Timestamp } from "@bufbuild/protobuf/wkt";
// Type-only: avoids a runtime import cycle with the component.
import type { MethodKind } from "@/components/ui/Tag";

// An Item plus both ways of addressing it: `path` is the DISPLAY-NAME path to its
// parent folder (empty at the root), which is what every RPC takes; `slugPath` is
// the same walk in on-disk slugs, which is what `itemKey` is built from.
export interface ItemWithPath {
  item: Item;
  collection: string;
  path: string[];
  slugPath: string[];
  children?: ItemWithPath[]; // populated for folders
}

const convertToItemWithPath = (
  protoItem: Item,
  collection: string,
  parentPath: string[],
  parentSlugPath: string[]
): ItemWithPath => {
  const result: ItemWithPath = {
    item: protoItem,
    collection,
    path: parentPath,
    slugPath: parentSlugPath,
  };
  if (protoItem.content.case === "folder") {
    const childPath = [...parentPath, protoItem.name];
    const childSlugPath = [...parentSlugPath, protoItem.slug];
    result.children = protoItem.content.value.items.map((child) =>
      convertToItemWithPath(child, collection, childPath, childSlugPath)
    );
  }
  return result;
};

export const rootItemsOf = (
  rootItem: Item | undefined,
  collection: string
): ItemWithPath[] => {
  if (rootItem?.content.case === "folder") {
    return rootItem.content.value.items.map((child) =>
      convertToItemWithPath(child, collection, [], [])
    );
  }
  return [];
};

// The identity of an item: its collection id followed by its slug path. Slugs are
// on-disk directory names, so a RENAME never changes a key and no keyed state has
// to be remapped for one. Only a real MOVE changes it — and the new key must be
// read back off the server's response (see slugKeyIn), because a move re-slugs on
// a destination collision. A plain "/" separator is unambiguous: no collection id
// can be a path prefix of another, and slugs never contain a slash.
export const itemKey = (item: ItemWithPath): string =>
  [item.collection, ...item.slugPath, item.item.slug].join("/");

// The slug key of the item that display-name path `path` + `name` now addresses in
// `root`, or null if it is not there. Read back off a mutation response because a
// move may re-slug on a destination collision (store.Move's uniqueSlug).
export const slugKeyIn = (
  collection: string,
  root: Item | undefined,
  path: string[],
  name: string
): string | null => {
  let items = root?.content.case === "folder" ? root.content.value.items : null;
  const slugs: string[] = [];
  for (const segment of path) {
    const folder = items?.find(
      (it) => it.name === segment && it.content.case === "folder"
    );
    if (!folder || folder.content.case !== "folder") return null;
    slugs.push(folder.slug);
    items = folder.content.value.items;
  }
  const found = items?.find((it) => it.name === name);
  if (!found) return null;
  return [collection, ...slugs, found.slug].join("/");
};

export const findByKey = (
  items: ItemWithPath[],
  key: string | null
): ItemWithPath | null => {
  if (!key) return null;
  for (const it of items) {
    if (itemKey(it) === key) return it;
    if (it.children) {
      const found = findByKey(it.children, key);
      if (found) return found;
    }
  }
  return null;
};

// The path a new child of `parent` would carry.
export const childPathOf = (parent: ItemWithPath | null): string[] =>
  parent ? [...parent.path, parent.item.name] : [];

const fullPathOf = (item: ItemWithPath): string[] => [...item.path, item.item.name];

const isStrictPrefix = (prefix: string[], full: string[]): boolean =>
  prefix.length < full.length && prefix.every((segment, i) => segment === full[i]);

// Drops items already covered by an ancestor in the same selection, plus exact
// duplicates — so a delete's count matches the number of independent operations.
export const pruneNestedSelections = (items: readonly ItemWithPath[]): ItemWithPath[] => {
  const paths = items.map(fullPathOf);
  const kept = new Set<string>();
  return items.filter((_, i) => {
    if (paths.some((other, j) => i !== j && isStrictPrefix(other, paths[i]))) return false;
    // Joined on NUL, not "/", so a name containing a slash cannot make two
    // different paths compare equal.
    const identity = paths[i].join("\u0000");
    if (kept.has(identity)) return false;
    kept.add(identity);
    return true;
  });
};

// Matches service/cli's serviceFullName: a service in the EMPTY proto package has
// no dot to join on, so prefixing one yields ".EchoService" and never compares
// equal to the name the rest of the app carries.
export const serviceName = (s: Service): string =>
  s.package ? `${s.package}.${s.name}` : s.name;

export const resolveMethod = (
  services: Service[],
  service: string,
  method: string
): Method | undefined =>
  services
    .find((s) => serviceName(s) === service)
    ?.methods.find((m) => m.name === method);

export const methodKind = (method?: Method): MethodKind => {
  const client = method?.clientStreaming ?? false;
  const server = method?.serverStreaming ?? false;
  if (client && server) return "bd";
  if (client) return "cs";
  if (server) return "ss";
  return "u";
};

const CODE_NAMES: Record<number, string> = {
  0: "OK",
  1: "CANCELLED",
  2: "UNKNOWN",
  3: "INVALID_ARGUMENT",
  4: "DEADLINE_EXCEEDED",
  5: "NOT_FOUND",
  6: "ALREADY_EXISTS",
  7: "PERMISSION_DENIED",
  8: "RESOURCE_EXHAUSTED",
  9: "FAILED_PRECONDITION",
  10: "ABORTED",
  11: "OUT_OF_RANGE",
  12: "UNIMPLEMENTED",
  13: "INTERNAL",
  14: "UNAVAILABLE",
  15: "DATA_LOSS",
  16: "UNAUTHENTICATED",
};

export const codeName = (code: number): string =>
  CODE_NAMES[code] ?? `CODE_${code}`;

export const durationMs = (d?: Duration | Timestamp): number =>
  d ? Number(d.seconds) * 1000 + d.nanos / 1e6 : 0;

export const latencyLabel = (d?: Duration): string => {
  if (!d) return "";
  const ms = durationMs(d);
  return ms < 1000 ? `${ms.toFixed(1)} ms` : `${(ms / 1000).toFixed(3)} s`;
};

export const timestampLabel = (t?: Timestamp): string => {
  if (!t) return "";
  return new Date(durationMs(t)).toLocaleTimeString();
};

// Shortens from the MIDDLE, because both ends of an id carry meaning a head or tail
// ellipsis would eat: a bazel source id is scheme-then-target, and it is the target
// half a user recognises. Callers pair it with a title holding the full string.
export const middleEllipsis = (s: string, max: number): string => {
  if (s.length <= max) return s;
  const head = Math.ceil((max - 1) / 2);
  const tail = max - 1 - head;
  return `${s.slice(0, head)}…${s.slice(s.length - tail)}`;
};

export const prettyBody = (bytes: Uint8Array): string => {
  const text = new TextDecoder().decode(bytes);
  try {
    return JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    return text;
  }
};

const metadataValueToString = (value: unknown): string =>
  Array.isArray(value)
    ? value.map((v) => String(v)).join(", ")
    : String(value ?? "");

export const metadataEntries = (
  md?: Record<string, unknown>
): Array<[string, string]> =>
  md ? Object.entries(md).map(([k, v]) => [k, metadataValueToString(v)]) : [];
