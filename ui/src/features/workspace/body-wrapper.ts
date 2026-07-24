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
import { scanTokens } from "./tokens";

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

// --- Migration to canonical TS (all-JS phase) -------------------------------------------------
// The request body is now ALWAYS authored as TypeScript, and the editor can only host a
// CANONICAL hidden-wrapper module. So every persisted body — legacy JSON, a `{{ }}`-token JSON
// body, or an older explicit TS form — is normalized to the canonical shape on load, before it
// reaches the editor or an invoke payload (see RequestWorkspace).

// The canonical prefix/suffix WITHOUT their wrapper newline, used to recognize + re-wrap an older
// single-/multi-line explicit TS body (`=> ( <expr> )`) that lacks the canonical newlines.
const PREFIX_NO_NL = WRAP_PREFIX.slice(0, -1); // "export default (): RequestMessage => ("
const SUFFIX_NO_NL = WRAP_SUFFIX.slice(1); //                                          ")"

// The seed / empty-body shape: `{ }` with a blank middle line, so an emptied or trivial body
// opens on an editable object literal — NEVER `wrap("")` (which serializes to `=> ()`, a JS
// syntax error). All the "nothing meaningful here" cases funnel through this.
const EMPTY_BODY = "{\n  \n}";

// rewriteBodyTokens splices each recognized `{{ name(args?) }}` body token to its bare
// `name(args?)` call (the token's trimmed inner text) in place, leaving all surrounding text
// untouched. Tokens sit in value position, so the call replaces the whole `{{…}}`
// (e.g. `{"id": {{uuid()}}}` → `{"id": uuid()}`). Non-token text (incl. an unbalanced `{{`) is
// preserved verbatim. Used only by migrateBodyToTs.
const rewriteBodyTokens = (s: string): string => {
  const toks = scanTokens(s);
  if (toks.length === 0) return s;
  let out = "";
  let last = 0;
  for (const t of toks) {
    out += s.slice(last, t.start) + t.inner;
    last = t.end;
  }
  return out + s.slice(last);
};

// migrateBodyToTs upgrades a persisted request body to the canonical hidden-wrapper module.
// Idempotent — an already-canonical body is returned unchanged, so it is safe to run on every
// load / invoke. Cases (worked examples):
//
//   • isCanonical(body)                            → body                       (unchanged)
//   • "" | "{}" | whitespace                       → wrap("{\n  \n}")           (empty seed)
//   • export default (): RequestMessage => ({…})   → wrap("{…}")                (old single-line
//       (incl. multi-line `=> ({\n…\n})`)            T2/T3 form → canonical newlines)
//   • {"id": {{uuid()}}}                           → wrap('{"id": uuid()}')      (JSON + tokens:
//                                                      each token → a bare generator call)
//   • {"a": 1, "b": [2, 3]}                        → wrap('{"a": 1, "b": [2, 3]}')  (plain JSON:
//                                                      a JSON literal is a valid TS expression
//                                                      inside the `=> ( … )` parens)
//
// Edge cases (rare, best-effort — documented not handled specially):
//   • A dotted token `{{ a.b() }}` splices to `a.b()`, an expression referencing a global `a`.
//     The backend only injects SIMPLE-identifier generators as globals (compose.go simpleIdentRe
//     / Editor.tsx), so a dotted generator won't resolve — but dotted generators were already a
//     documented gap, and the splice preserves what the user wrote.
//   • A body that is none of the above (not canonical, not empty, not the explicit TS form, not
//     JSON-ish) is still wrapped verbatim; if it is not a valid TS expression the editor surfaces
//     the error — we cannot validate arbitrary text here.
export const migrateBodyToTs = (body: string): string => {
  if (isCanonical(body)) return body;
  const trimmed = body.trim();
  if (trimmed === "" || trimmed === "{}") return wrap(EMPTY_BODY);
  // Old explicit TS form `export default (): RequestMessage => ( <expr> )` lacking the canonical
  // newlines (single-line or `=> ({\n…\n})`): re-wrap its inner expression into canonical shape.
  // JSON never starts with `export default (…` nor ends with `)`, so this can't catch a JSON body.
  if (body.startsWith(PREFIX_NO_NL) && body.endsWith(SUFFIX_NO_NL)) {
    const inner = body.slice(PREFIX_NO_NL.length, body.length - SUFFIX_NO_NL.length).trim();
    return wrap(inner === "" ? EMPTY_BODY : inner);
  }
  // Otherwise a JSON body: rewrite its `{{ }}` tokens to generator calls, then wrap.
  const rewritten = rewriteBodyTokens(body).trim();
  return wrap(rewritten === "" ? EMPTY_BODY : rewritten);
};
