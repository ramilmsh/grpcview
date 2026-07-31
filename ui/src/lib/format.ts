// Pure, framework-free helpers ported from the previous UI (lib/store.ts,
// ResponsePanel.tsx, MetadataEditor.tsx). No server calls live here — reads and
// writes go through connect-query (see workspace-query.ts).

import type { Item, Service, Method } from "@grpcview/v1/workspace_pb";
import type { Duration, Timestamp } from "@bufbuild/protobuf/wkt";
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
// changes the key — callers must remap keyed state (see ui-store moveSubtree).
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

// fullPathOf is an item's own path INCLUDING its own name — the exact value
// any DIRECT CHILD of it would carry as ITS OWN `.path` (compare childPathOf
// above, which computes this same thing for a would-be NEW child rather than
// for the item itself). The one thing pruneNestedSelections below needs to
// test ancestry: item B is a descendant of item A exactly when fullPathOf(A)
// is a strict prefix of fullPathOf(B).
const fullPathOf = (item: ItemWithPath): string[] => [...item.path, item.item.name];

// isStrictPrefix reports whether `prefix` names an ANCESTOR path of `full` —
// strictly SHORTER (an item is never its own descendant) and matching
// segment-for-segment from the root. Not exported: nothing outside
// pruneNestedSelections below needs "is A an ancestor of B" as its own
// primitive yet.
const isStrictPrefix = (prefix: string[], full: string[]): boolean =>
  prefix.length < full.length && prefix.every((segment, i) => segment === full[i]);

// pruneNestedSelections keeps only the items in an arbitrary multi-selection
// that are NOT already covered by an ANCESTOR also present in that SAME
// selection — e.g. a folder plus one of its own (possibly deeply nested)
// children, both reachable in one batch via shift+click across an expanded
// folder's rows, or ctrl+click picking a folder and then one of its
// descendants individually (components/tree/'s multi-select,
// tree-rewrite-plan.md's T2). Deleting the ancestor already removes the whole
// subtree server-side — DeleteRequestRequest is a path+name-addressed "remove
// this item AND anything under it" operation (service/store/fs.go's
// Collection.Delete does `os.RemoveAll` on the item's own directory, and is
// itself idempotent: deleting an already-gone item is a documented no-op, not
// an error) — so a covered descendant is not merely REDUNDANT to also
// process, it is client-side noise a confirm dialog would otherwise miscount:
// "Delete 5 items" when only 2 are independent operations, the other 3 being
// removed as a side effect of one of those 2. CollectionPanel.tsx's
// delete-confirm flow is the one caller today, but this is pure
// ItemWithPath-tree reasoning with nothing gRPC/UI-specific about it, so it
// lives here beside childPathOf/findByKey rather than in a feature file.
//
// Checking the FULL path (not just the immediate parent) is what makes a
// selection spanning THREE levels at once (a folder, its child folder, AND
// that child's own child, all individually selected) collapse correctly to
// just the topmost ancestor in a single pass, with no separate
// transitive-closure step: the grandchild's full path already has the
// top-level folder's full path as a prefix, whether or not the MIDDLE folder
// also happens to survive the same comparison.
//
// EXACT DUPLICATES are dropped in the same pass, keeping the first occurrence.
// Ancestry alone does not cover them: isStrictPrefix requires the prefix to be
// strictly SHORTER (an item is not its own ancestor), so two entries naming the
// identical path each fail to prune the other and BOTH survive. That is
// reachable, not theoretical — ui-store.ts's moveSubtree remaps `treeSelection`
// by substituting one id for another (`s.treeSelection.map(...)`), so renaming
// a selected row onto the name of a DIFFERENT selected sibling ("Calls/bar" ->
// "Calls/foo" while "Calls/foo" is also selected) leaves the same id in the
// list twice, and both copies resolve to the one surviving row. The resulting
// double delete is harmless server-side (Collection.Delete is `os.RemoveAll`
// and idempotent) — but a dialog reading "Delete 2 requests" over ONE row is
// precisely the miscount this function exists to prevent, so it is pruning's
// job, not the caller's.
//
// O(n²) (every item compared against every other) — fine at this app's scale
// (plan: "dozens to low hundreds", the same reasoning flatten.ts/Tree.tsx's
// own reveal() already lean on for their own full-tree walks).
export const pruneNestedSelections = (items: readonly ItemWithPath[]): ItemWithPath[] => {
  const paths = items.map(fullPathOf);
  const kept = new Set<string>();
  return items.filter((_, i) => {
    if (paths.some((other, j) => i !== j && isStrictPrefix(other, paths[i]))) return false;
    // Joined on NUL rather than "/" (keyOf's convention) so that a name
    // containing a slash cannot make two genuinely different paths look equal —
    // isStrictPrefix compares segment arrays for the same reason, and this
    // second half of the pass has no business being laxer than the first.
    const identity = paths[i].join("\u0000");
    if (kept.has(identity)) return false;
    kept.add(identity);
    return true;
  });
};

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

// ── response metadata → display ──────────────────────────────────────────────
// Request metadata is now authored as a TypeScript module (see metadata-wrapper.ts /
// MetadataEditor.tsx), so the old grid helpers (MetadataRow / rowsToObject / objectToRows) are
// gone. What remains here is RESPONSE-side: rendering a received Struct's values for display.

// metadataValueToString renders a metadata value for display: list values
// (multi-valued metadata) are comma-joined; scalars are stringified. Internal to
// metadataEntries below.
const metadataValueToString = (value: unknown): string =>
  Array.isArray(value)
    ? value.map((v) => String(v)).join(", ")
    : String(value ?? "");

// metadataEntries flattens a response's Struct metadata for display.
export const metadataEntries = (
  md?: Record<string, unknown>
): Array<[string, string]> =>
  md ? Object.entries(md).map(([k, v]) => [k, metadataValueToString(v)]) : [];
