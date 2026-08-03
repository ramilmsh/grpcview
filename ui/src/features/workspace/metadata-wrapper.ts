// Canonical wrapping for the TypeScript request METADATA, mirroring body-wrapper.ts. The object is
// typed against an ambient `type Metadata = { [k: string]: string[] }` — metadata is multi-valued.
import type { JsonObject, JsonValue } from "@bufbuild/protobuf";

export const META_WRAP_PREFIX = "export default async (): Promise<Metadata> => (\n";
export const META_WRAP_SUFFIX = "\n)";
export const META_PREFIX_LINES = 1;
export const META_SUFFIX_LINES = 1;

export const wrap = (obj: string): string => META_WRAP_PREFIX + obj + META_WRAP_SUFFIX;

export const isCanonical = (text: string): boolean =>
  text.startsWith(META_WRAP_PREFIX) && text.endsWith(META_WRAP_SUFFIX);

// Never `wrap("")`, which serializes to `=> ()` — a JS syntax error.
const EMPTY_METADATA = "{\n  \n}";

const DEFAULT_METADATA = "{ ...gv.metadata.inherit() }";

export const defaultMetadataModule = (): string => wrap(DEFAULT_METADATA);

// gRPC header keys routinely contain `-`, which is not a valid bare identifier.
const IDENT_RE = /^[A-Za-z_$][A-Za-z0-9_$]*$/;
const keyLiteral = (key: string): string => (IDENT_RE.test(key) ? key : JSON.stringify(key));

const renderElem = (el: JsonValue): string =>
  JSON.stringify(typeof el === "string" ? el : String(el));

const renderElems = (value: JsonValue): string => {
  const arr = Array.isArray(value) ? value : [value];
  return arr.map(renderElem).join(", ");
};

// migrateMetadataToTs builds the canonical metadata module from a JsonObject — an empty seed, or a
// History entry's resolved metadata Struct. Every value becomes a string[] literal.
export const migrateMetadataToTs = (md?: JsonObject): string => {
  const entries = md ? Object.entries(md) : [];
  if (entries.length === 0) return wrap(EMPTY_METADATA);
  const lines = entries.map(([key, value]) => `  ${keyLiteral(key)}: [${renderElems(value)}]`);
  return wrap(`{\n${lines.join(",\n")}\n}`);
};
