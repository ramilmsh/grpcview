// The TypeScript request METADATA, mirroring body-wrapper.ts. The object is typed against an
// ambient `type Metadata = { [k: string]: string[] }` — metadata is multi-valued.
//
// Two forms only, decided the same way as a body: first token `{` means JSON-like and gets
// wrapped, anything else is a module the author owns. Per D2 (docs/design/planned/
// script-region.md) the skeleton itself carries no standard imports — `invoke` and `inherit` are
// ordinary auto-import candidates. The one exception is defaultMetadataModule's seed, which
// spells out `...inherit()` and so ships its own import as derived content (D2's own example of
// what "derived" means).
import type { JsonObject, JsonValue } from "@bufbuild/protobuf";
import { buildWrapped, findRegion, normalizeSkeleton, regionHiddenRanges, regionBounds } from "./script-region";
import { leadsWithBrace } from "./module-sniff";

export const META_SKELETON = "export default async (): Promise<Metadata> => (";

export const isWrapped = (text: string): boolean => findRegion(text) !== undefined;

export const wrap = (obj: string): string => buildWrapped({ skeleton: META_SKELETON, region: obj });

// hostMetadataScript is the read-seam counterpart of body-wrapper.ts's migrateBodyToTs.
// RequestWorkspace.tsx reads request.draftMetadataScript raw, so this is where a hand-written
// JSON-like metadata.ts gets the wrapper, and where an already-wrapped one gets its skeleton
// repaired (D10). The store (service/store/codec.go's writeSourceFile) normalizes metadata.ts to
// exactly one trailing newline on write, so a persisted script arrives here as "<content>\n" —
// stripped before the region scan so the round trip stays byte-identical. An empty script stays
// "" (an absent metadata.ts is not the same as an empty object — the editor seeds that
// separately).
export const hostMetadataScript = (script: string): string => {
  const stripped = script.replace(/\n+$/, "");
  if (findRegion(stripped)) return normalizeSkeleton(stripped, META_SKELETON);
  if (!leadsWithBrace(stripped)) return stripped;
  return wrap(stripped.trim());
};

// Never `wrap("")`, which serializes to `=> ()` — a JS syntax error.
const EMPTY_METADATA = "{\n  \n}";

// `inherit` is spelled out as a derived import here, because the seed itself references it — the
// one place a standard import survives (D2).
export const defaultMetadataModule = (): string =>
  buildWrapped({
    imports: ['import { inherit } from "grpcview:metadata";'],
    skeleton: META_SKELETON,
    region: "{\n  ...inherit(),\n}",
  });

// gRPC header keys routinely contain `-`, which is not a valid bare identifier.
const IDENT_RE = /^[A-Za-z_$][A-Za-z0-9_$]*$/;
const keyLiteral = (key: string): string => (IDENT_RE.test(key) ? key : JSON.stringify(key));

const renderElem = (el: JsonValue): string =>
  JSON.stringify(typeof el === "string" ? el : String(el));

const renderElems = (value: JsonValue): string => {
  const arr = Array.isArray(value) ? value : [value];
  return arr.map(renderElem).join(", ");
};

// migrateMetadataToTs builds the wrapped metadata module from a JsonObject — an empty seed, or a
// History entry's resolved metadata Struct. Every value becomes a string[] literal.
export const migrateMetadataToTs = (md?: JsonObject): string => {
  const entries = md ? Object.entries(md) : [];
  if (entries.length === 0) return wrap(EMPTY_METADATA);
  const lines = entries.map(([key, value]) => `  ${keyLiteral(key)}: [${renderElems(value)}]`);
  return wrap(`{\n${lines.join(",\n")}\n}`);
};

export const metaBounds = regionBounds;
export const hiddenLineRanges = regionHiddenRanges;
