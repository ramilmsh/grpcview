// Canonical wrapping for the TypeScript request METADATA, mirroring body-wrapper.ts. The object is
// typed against an ambient `type Metadata = { [k: string]: string[] }` — metadata is multi-valued.
//
// Two forms only, decided the same way as a body: first token `{` means JSON-like and gets
// wrapped, anything else is a module the author owns. The hidden prefix imports `inherit` (the
// spread nearly every metadata script opens with) and `invoke` (an auth header is routinely
// another saved request's response). `params` is deliberately absent — see body-wrapper.ts.
import type { JsonObject, JsonValue } from "@bufbuild/protobuf";
import { leadsWithBrace } from "./module-sniff";

export const META_WRAP_PREFIX =
  'import { invoke } from "grpcview:invoke";\n' +
  'import { inherit } from "grpcview:metadata";\n' +
  "\n" +
  "export default async (): Promise<Metadata> => (\n";
export const META_WRAP_SUFFIX = "\n)";
export const META_PREFIX_LINES = 4;
export const META_SUFFIX_LINES = 1;

export const wrap = (obj: string): string => META_WRAP_PREFIX + obj + META_WRAP_SUFFIX;

export const isCanonical = (text: string): boolean =>
  text.startsWith(META_WRAP_PREFIX) && text.endsWith(META_WRAP_SUFFIX);

// hostMetadataScript is the read-seam counterpart of body-wrapper.ts's migrateBodyToTs.
// RequestWorkspace.tsx reads request.draftMetadataScript raw, so this is where a hand-written
// JSON-like metadata.ts gets the wrapper. The store (service/store/codec.go's writeSourceFile)
// normalizes metadata.ts to exactly one trailing newline on write, so a persisted script arrives
// here as "<canonical>\n" — one byte isCanonical never matched. Stripping it before hosting is
// what re-recognizes the canonical wrapper; the store re-adds exactly one on the next write, so
// the round trip is byte-identical and produces no spurious git diff. An empty script stays ""
// (an absent metadata.ts is not the same as an empty object — the editor seeds that separately).
export const hostMetadataScript = (script: string): string => {
  const stripped = script.replace(/\n+$/, "");
  if (isCanonical(stripped) || !leadsWithBrace(stripped)) return stripped;
  return wrap(stripped.trim());
};

// Never `wrap("")`, which serializes to `=> ()` — a JS syntax error.
const EMPTY_METADATA = "{\n  \n}";

// `inherit` comes from the hidden prefix, so the seed reads as the plain spread an author would
// have written by hand.
const DEFAULT_METADATA = "{\n  ...inherit(),\n}";

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

export interface MetadataBounds {
  first: number;
  last: number;
  total: number;
}

// metaBounds locates the editable region of `text`: past the hidden prefix and short of the
// hidden suffix for a canonical wrapper, or the whole document for a module — text-only (no
// Monaco model needed) so MetadataEditor.tsx and this file's tests agree on the same geometry.
export const metaBounds = (text: string): MetadataBounds => {
  const total = text.split("\n").length;
  if (!isCanonical(text)) return { first: 1, last: total, total };
  const first = META_PREFIX_LINES + 1;
  const last = Math.max(first, total - META_SUFFIX_LINES);
  return { first, last, total };
};

export interface HiddenLineRange {
  startLine: number;
  endLine: number;
}

// hiddenLineRanges: the wrapper's prefix/suffix lines for a canonical script, or none for a
// module — there is nothing to hide, per the two-forms comment above.
export const hiddenLineRanges = (text: string): HiddenLineRange[] => {
  if (!isCanonical(text)) return [];
  const { last, total } = metaBounds(text);
  return [
    { startLine: 1, endLine: META_PREFIX_LINES },
    { startLine: last + 1, endLine: total },
  ];
};
