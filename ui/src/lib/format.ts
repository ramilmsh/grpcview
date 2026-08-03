import type { Item, Service, Method } from "@grpcview/v1/workspace_pb";
import type { Duration, Timestamp } from "@bufbuild/protobuf/wkt";
// Type-only: avoids a runtime import cycle with the component.
import type { MethodKind } from "@/components/ui/Tag";

// An Item plus the path to its parent folder (empty at the root).
export interface ItemWithPath {
  item: Item;
  path: string[];
  children?: ItemWithPath[]; // populated for folders
}

const convertToItemWithPath = (
  protoItem: Item,
  parentPath: string[]
): ItemWithPath => {
  const result: ItemWithPath = { item: protoItem, path: parentPath };
  if (protoItem.content.case === "folder") {
    const childPath = [...parentPath, protoItem.name];
    result.children = protoItem.content.value.items.map((child) =>
      convertToItemWithPath(child, childPath)
    );
  }
  return result;
};

export const rootItemsOf = (rootItem?: Item): ItemWithPath[] => {
  if (rootItem?.content.case === "folder") {
    return rootItem.content.value.items.map((child) =>
      convertToItemWithPath(child, [])
    );
  }
  return [];
};

export const keyOf = (path: string[], name: string): string =>
  [...path, name].join("/");

// The name-derived identity of an item, so a rename changes it — callers must
// remap keyed state (see ui-store's moveSubtree).
export const itemKey = (item: ItemWithPath): string =>
  keyOf(item.path, item.item.name);

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

export const latencyLabel = (d?: Duration): string => {
  if (!d) return "";
  const ms = Number(d.seconds) * 1000 + d.nanos / 1e6;
  return ms < 1000 ? `${ms.toFixed(1)} ms` : `${(ms / 1000).toFixed(3)} s`;
};

export const timestampLabel = (t?: Timestamp): string => {
  if (!t) return "";
  return new Date(Number(t.seconds) * 1000 + t.nanos / 1e6).toLocaleTimeString();
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
