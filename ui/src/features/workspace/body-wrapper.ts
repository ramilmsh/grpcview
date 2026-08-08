// Canonical wrapping for the TypeScript request body: the Monaco model text, the persisted
// draftBody and the invoke payload are all the same string, with the editor merely HIDING the
// prefix/suffix lines. `=> (` … `)` is load-bearing — without it `{ … }` parses as a block.
//
// Two forms only, matching the backend (service/scripting/entry.go): a module (has its own
// `export default`) or an expression (wrapped into one). A module is never wrapped and nothing
// about it is hidden — see isModule's callers below.
import { isModule } from "./module-sniff";

export const WRAP_PREFIX = "export default async (): Promise<RequestMessage> => (\n";
export const WRAP_SUFFIX = "\n)";
export const PREFIX_LINES = 1;
export const SUFFIX_LINES = 1;

export const wrap = (body: string): string => WRAP_PREFIX + body + WRAP_SUFFIX;

export const isCanonical = (text: string): boolean =>
  text.startsWith(WRAP_PREFIX) && text.endsWith(WRAP_SUFFIX);

// Never `wrap("")`, which serializes to `=> ()` — a JS syntax error.
const EMPTY_BODY = "{\n  \n}";

// migrateBodyToTs normalizes a body to the canonical module, or leaves an author-written module
// (e.g. one reached via `import`) untouched, byte-for-byte. Idempotent.
export const migrateBodyToTs = (body: string): string => {
  if (isCanonical(body)) return body;
  if (isModule(body)) return body;
  const trimmed = body.trim();
  if (trimmed === "" || trimmed === "{}") return wrap(EMPTY_BODY);
  return wrap(trimmed);
};

export interface BodyBounds {
  first: number;
  last: number;
  total: number;
}

// bodyBounds locates the editable region of `text`: past the hidden prefix and short of the
// hidden suffix for a canonical wrapper, or the whole document for a module — text-only (no
// Monaco model needed) so Editor.tsx and this file's tests agree on the same geometry.
export const bodyBounds = (text: string): BodyBounds => {
  const total = text.split("\n").length;
  if (!isCanonical(text)) return { first: 1, last: total, total };
  const first = PREFIX_LINES + 1;
  const last = Math.max(first, total - SUFFIX_LINES);
  return { first, last, total };
};

export interface HiddenLineRange {
  startLine: number;
  endLine: number;
}

// hiddenLineRanges: the wrapper's prefix/suffix lines for a canonical body, or none for a module —
// there is nothing to hide, per the two-forms comment above.
export const hiddenLineRanges = (text: string): HiddenLineRange[] => {
  if (!isCanonical(text)) return [];
  const { last, total } = bodyBounds(text);
  return [
    { startLine: 1, endLine: PREFIX_LINES },
    { startLine: last + 1, endLine: total },
  ];
};
