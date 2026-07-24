// Canonical wrapping for the TypeScript request body (ts-request-body-plan §T4 — the "hidden
// wrapper" bare-object authoring mode). The Monaco model text, the persisted draftBody, and the
// invoke payload are ALL the same string:
//
//     export default (): RequestMessage => (
//     <body>
//     )
//
// The editor merely HIDES the first (prefix) and last (suffix) lines via the editor's internal
// setHiddenAreas so the user sees only the bare <body> object. Hiding is a pure VIEW concern —
// nothing ever round-trips unwrapped, so persistence / reload / invoke are untouched.
//
// Why this exact shape (both halves are load-bearing):
//   • `=> (` … `)` puts <body> in EXPRESSION position, so an object literal `{ … }` type-checks
//     (and excess-property-checks) against the RequestMessage return annotation. A bare `{ … }`
//     at statement position parses as a BLOCK, not an object — no typing, and it would misparse.
//   • `export default` keeps the sent source on the backend's entry-point eval path
//     (service/scripting/entry.go: hasDefaultExport → generatorPostlude), NOT the last-expression
//     path where a bare `{ … }` also misparses. So invoke + composition are byte-identical to the
//     shipped T2/T3 explicit form; this feature is a view-only delta over it.

export const WRAP_PREFIX = "export default async (): Promise<RequestMessage> => (\n";
export const WRAP_SUFFIX = "\n)";
export const PREFIX_LINES = 1; // model lines occupied by WRAP_PREFIX (the `=> (` line)
export const SUFFIX_LINES = 1; // model lines occupied by WRAP_SUFFIX (the `)` line)

// wrap turns a bare body into the canonical module. Used when seeding / normalizing a body.
export const wrap = (body: string): string => WRAP_PREFIX + body + WRAP_SUFFIX;

// isCanonical is the discriminator for "is this model text in canonical hidden-wrapper form".
// Deliberately an EXACT prefix/suffix match, not a fuzzy "contains export default", so only a
// canonical module hides cleanly.
export const isCanonical = (text: string): boolean =>
  text.startsWith(WRAP_PREFIX) && text.endsWith(WRAP_SUFFIX);

// --- Canonicalization -------------------------------------------------------------------------
// The request body is ALWAYS authored as TypeScript, and the editor can only host a CANONICAL
// hidden-wrapper module. A body that is not already canonical (an empty seed, a bare JSON object
// from a history re-run, or a cs/bd compose extra) is normalized to the canonical shape before it
// reaches the editor or an invoke payload (see RequestWorkspace).

// The seed / empty-body shape: `{ }` with a blank middle line, so an emptied or trivial body opens
// on an editable object literal — NEVER `wrap("")` (which serializes to `=> ()`, a JS syntax
// error). All the "nothing meaningful here" cases funnel through this.
const EMPTY_BODY = "{\n  \n}";

// migrateBodyToTs normalizes a request body to the canonical hidden-wrapper module. Idempotent —
// an already-canonical body is returned unchanged, so it is safe to run on every load / invoke.
// A bare JSON object (a history-run body, or a cs/bd compose extra authored as raw JSON) is a
// valid TS expression inside the `=> ( … )` parens, so it is wrapped verbatim; an empty or `{}`
// body becomes the empty seed. Anything else is wrapped verbatim too — if it is not a valid TS
// expression the editor surfaces the error, since arbitrary text cannot be validated here.
export const migrateBodyToTs = (body: string): string => {
  if (isCanonical(body)) return body;
  const trimmed = body.trim();
  if (trimmed === "" || trimmed === "{}") return wrap(EMPTY_BODY);
  return wrap(trimmed);
};
