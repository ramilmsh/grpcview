// Pure, framework-free helpers ported from the previous UI (lib/store.ts,
// ResponsePanel.tsx, MetadataEditor.tsx). No server calls live here — reads and
// writes go through connect-query (see workspace-query.ts).

import type { Item, Service, Method } from "@grpcview/v1/workspace_pb";
import type { Duration, Timestamp } from "@bufbuild/protobuf/wkt";
import type { JsonObject } from "@bufbuild/protobuf";
// Type-only import — no runtime dependency on the component, so no import cycle.
import type { MethodKind } from "@/components/ui/Tag";

// ── tree flattening ──────────────────────────────────────────────────────────

// Extended Item with the path to its parent folder (empty for root items). The
// proto Item has no path field, so the UI tracks it while flattening the tree.
export interface ItemWithPath {
  item: Item;
  path: string[];
  children?: ItemWithPath[]; // populated for folders
}

// convertToItemWithPath recursively annotates a proto Item tree with parent paths.
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

// rootItems flattens a Workspace's root folder into top-level ItemWithPath nodes.
export const rootItemsOf = (rootItem?: Item): ItemWithPath[] => {
  if (rootItem?.content.case === "folder") {
    return rootItem.content.value.items.map((child) =>
      convertToItemWithPath(child, [])
    );
  }
  return [];
};

// keyOf builds an item key from a parent path + display name — the one place the
// key convention lives, so a rename can derive the new key the same way.
export const keyOf = (path: string[], name: string): string =>
  [...path, name].join("/");

// itemKey is the (name-derived) identity of an item within the tree (parent path
// + name), used to key open tabs and per-request invocation state so a response
// survives navigating away and back. NOTE: because it is name-derived, a rename
// changes the key — callers must remap keyed state (see ui-store renameItem).
export const itemKey = (item: ItemWithPath): string =>
  keyOf(item.path, item.item.name);

// findByKey resolves the live ItemWithPath for a key from the (freshly derived)
// tree, so editors/headers read the current server Request after any update.
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

// parentPathFor returns the path a child would be created under (the item's own
// path plus, for folders, its name).
export const childPathOf = (parent: ItemWithPath | null): string[] =>
  parent ? [...parent.path, parent.item.name] : [];

// ── services ─────────────────────────────────────────────────────────────────

// serviceName is a service's fully-qualified name (package.Service), the form a
// Request stores and the picker/tree display.
export const serviceName = (s: Service): string => `${s.package}.${s.name}`;

// resolveMethod finds the Method a request points at, so callers read its input
// schema/type from one lookup instead of re-walking the services list.
export const resolveMethod = (
  services: Service[],
  service: string,
  method: string
): Method | undefined =>
  services
    .find((s) => serviceName(s) === service)
    ?.methods.find((m) => m.name === method);

// methodKind maps a Method's streaming flags to the four-way kind the UI tags
// (see components/ui/Tag). A missing/unresolved method falls back to unary — the
// neutral default the tree/tabs/header show before a method resolves.
export const methodKind = (method?: Method): MethodKind => {
  const client = method?.clientStreaming ?? false;
  const server = method?.serverStreaming ?? false;
  if (client && server) return "bd";
  if (client) return "cs";
  if (server) return "ss";
  return "u";
};

// ── gRPC status ──────────────────────────────────────────────────────────────

// gRPC status codes -> canonical names (google.rpc.Code).
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

// prettyBody decodes response bytes and pretty-prints them when they parse as
// JSON (they always do for a real response), falling back to raw text.
export const prettyBody = (bytes: Uint8Array): string => {
  const text = new TextDecoder().decode(bytes);
  try {
    return JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    return text;
  }
};

// ── metadata ⇄ Struct ────────────────────────────────────────────────────────

// A single metadata (header) entry. Rows are the editor's working
// representation: they keep insertion order, an enabled flag, and allow a
// half-typed empty key — none of which a plain object preserves.
export interface MetadataRow {
  key: string;
  value: string;
  enabled: boolean;
}

// metadataValueToString renders a metadata value for display: list values
// (multi-valued metadata) are comma-joined; scalars are stringified.
export const metadataValueToString = (value: unknown): string =>
  Array.isArray(value)
    ? value.map((v) => String(v)).join(", ")
    : String(value ?? "");

// rowsToObject drops disabled rows and rows with a blank key, producing the
// JsonObject that maps onto the request's google.protobuf.Struct metadata field.
// KNOWN ASYMMETRY (preserved): a multi-valued header is comma-joined for display
// and saved back as one string, so lists don't round-trip — see plan §7/§11.
export const rowsToObject = (rows: MetadataRow[]): JsonObject => {
  const obj: JsonObject = {};
  for (const { key, value, enabled } of rows) {
    const k = key.trim();
    if (enabled && k) obj[k] = value;
  }
  return obj;
};

// objectToRows expands persisted metadata back into editable rows (all enabled).
export const objectToRows = (obj?: JsonObject): MetadataRow[] => {
  if (!obj) return [];
  return Object.entries(obj).map(([key, value]) => ({
    key,
    value: metadataValueToString(value),
    enabled: true,
  }));
};

// metadataEntries flattens a response's Struct metadata for display.
export const metadataEntries = (
  md?: Record<string, unknown>
): Array<[string, string]> =>
  md ? Object.entries(md).map(([k, v]) => [k, metadataValueToString(v)]) : [];
