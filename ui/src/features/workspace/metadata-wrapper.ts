// Canonical wrapping for the TypeScript request METADATA (the all-JS phase — metadata is now
// authored as JavaScript, replacing the key/value grid, ts-request-body-plan §0 P4). This
// MIRRORS body-wrapper.ts: the Monaco model text, the persisted draftMetadataScript, and the
// invoke payload are ALL the same string:
//
//     export default (): Metadata => (
//     <object>
//     )
//
// The editor HIDES the first (prefix) and last (suffix) lines via setHiddenAreas so the user
// sees only the bare <object>. gRPC metadata is multi-valued, so the object is typed against an
// injected ambient `type Metadata = { [key: string]: string[] }` (MetadataEditor). Evaluated on
// the backend in QuickJS (same machinery as the body, incl. generator ambient globals), the
// returned {[key]: string[]} becomes the outgoing metadata — so a user can write
// `{ authorization: ["Bearer " + apiToken()], "x-request-id": [uuid()] }`.
//
// Why this exact shape (identical rationale to the body wrapper): `=> (` … `)` puts <object> in
// EXPRESSION position so an object literal type-checks (and excess-property-checks) against the
// Metadata return annotation; `export default` keeps the sent source on the backend's
// entry-point eval path. The trivial wrap/unwrap/isCanonical helpers are duplicated here (rather
// than shared with body-wrapper) so the body editor stays untouched; only migrateMetadataToTs is
// metadata-specific.
import type { JsonObject, JsonValue } from "@bufbuild/protobuf";
import { wholeToken } from "./tokens";

export const META_WRAP_PREFIX = "export default (): Metadata => (\n";
export const META_WRAP_SUFFIX = "\n)";
export const META_PREFIX_LINES = 1; // model lines occupied by META_WRAP_PREFIX (the `=> (` line)
export const META_SUFFIX_LINES = 1; // model lines occupied by META_WRAP_SUFFIX (the `)` line)

// wrap turns a bare object into the canonical module. Used when seeding / migrating metadata.
export const wrap = (obj: string): string => META_WRAP_PREFIX + obj + META_WRAP_SUFFIX;

// isCanonical discriminates "is this model text in canonical hidden-wrapper form" — an EXACT
// prefix/suffix match, so only a freshly-migrated module hides cleanly (mirrors body-wrapper).
export const isCanonical = (text: string): boolean =>
  text.startsWith(META_WRAP_PREFIX) && text.endsWith(META_WRAP_SUFFIX);

// unwrap recovers the bare <object> from a canonical module (identity for non-canonical text).
export const unwrap = (text: string): string =>
  isCanonical(text)
    ? text.slice(META_WRAP_PREFIX.length, text.length - META_WRAP_SUFFIX.length)
    : text;

// The seed / empty-metadata shape: `{ }` with a blank middle line, so emptied metadata opens on
// an editable object literal that evaluates to `{}` (no metadata). NEVER `wrap("")` (which
// serializes to `=> ()`, a JS syntax error).
const EMPTY_METADATA = "{\n  \n}";

// A metadata key is emitted bare when it is a plain JS identifier, else as a quoted string
// literal (gRPC header keys routinely contain `-`, e.g. `x-request-id`, which is not an ident).
const IDENT_RE = /^[A-Za-z_$][A-Za-z0-9_$]*$/;
const keyLiteral = (key: string): string => (IDENT_RE.test(key) ? key : JSON.stringify(key));

// renderElem turns one metadata element into its TS source expression:
//   • a string that is a WHOLE `{{ name(args) }}` token → the bare `name(args)` call (UNquoted),
//     so a persisted metadata token migrates to a direct generator call (like the body did);
//   • any other string → its JSON string literal;
//   • a non-string scalar (defensive — persisted metadata is always string-valued) → coerced to
//     a JSON string literal.
const renderElem = (el: JsonValue): string => {
  if (typeof el === "string") {
    const tok = wholeToken(el);
    return tok ? tok.inner : JSON.stringify(el);
  }
  return JSON.stringify(String(el));
};

// renderElems produces the comma-joined element expressions inside a key's `[ ... ]`: an array
// value maps each element, a scalar value becomes a single-element list.
const renderElems = (value: JsonValue): string => {
  const arr = Array.isArray(value) ? value : [value];
  return arr.map(renderElem).join(", ");
};

// migrateMetadataToTs upgrades persisted metadata (the draft_metadata Struct as a JsonObject, or
// a History request's metadata Struct) to the canonical hidden-wrapper module. Every value
// becomes a string[] literal (gRPC metadata is multi-valued). Worked examples:
//
//   • undefined | {}                              → wrap("{\n  \n}")            (empty → evals {})
//   • { authorization: "Bearer x" }               → wrap('{\n  authorization: ["Bearer x"]\n}')
//   • { "x-request-id": "{{ uuid() }}" }           → wrap('{\n  "x-request-id": [uuid()]\n}')
//       (a whole `{{ }}` token migrates to a bare generator call; key quoted — it has a dash)
//   • { "x-scopes": ["read", "write"] }            → wrap('{\n  "x-scopes": ["read", "write"]\n}')
//   • { authorization: ["{{ bearer() }}"] }        → wrap('{\n  authorization: [bearer()]\n}')
//
// Edge cases (best-effort, mirroring body-wrapper): a dotted token `{{ a.b() }}` migrates to the
// expression `a.b()`, which references a global `a` the backend only injects for SIMPLE-identifier
// generators — a documented v1 gap (dotted generators were never composition globals). The output
// is always a valid TS object expression inside the `=> ( … )` parens.
export const migrateMetadataToTs = (md?: JsonObject): string => {
  const entries = md ? Object.entries(md) : [];
  if (entries.length === 0) return wrap(EMPTY_METADATA);
  const lines = entries.map(([key, value]) => `  ${keyLiteral(key)}: [${renderElems(value)}]`);
  return wrap(`{\n${lines.join(",\n")}\n}`);
};
