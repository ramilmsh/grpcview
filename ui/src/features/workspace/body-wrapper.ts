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
export const WRAP_PREFIX = "export default (): RequestMessage => (\n";
export const WRAP_SUFFIX = "\n)";
export const PREFIX_LINES = 1; // model lines occupied by WRAP_PREFIX (the `=> (` line)
export const SUFFIX_LINES = 1; // model lines occupied by WRAP_SUFFIX (the `)` line)

// wrap turns a bare body into the canonical module. Used when seeding / migrating a body.
export const wrap = (body: string): string => WRAP_PREFIX + body + WRAP_SUFFIX;

// isCanonical is the discriminator for "is this model text in canonical hidden-wrapper form".
// Deliberately an EXACT prefix/suffix match, not a fuzzy "contains export default": an old
// single-line T3 body (`=> ({ … })`) is NOT canonical, so it renders EXPANDED (unhidden, valid,
// editable) rather than being mis-hidden. New / migrated bodies are canonical and hide cleanly.
export const isCanonical = (text: string): boolean =>
  text.startsWith(WRAP_PREFIX) && text.endsWith(WRAP_SUFFIX);

// unwrap recovers the bare <body> from a canonical module (identity for non-canonical text).
// Used by migration / a future "expand to module" toggle; the editor itself never unwraps.
export const unwrap = (text: string): string =>
  isCanonical(text) ? text.slice(WRAP_PREFIX.length, text.length - WRAP_SUFFIX.length) : text;
