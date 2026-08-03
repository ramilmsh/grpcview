// Canonical wrapping for the TypeScript request body: the Monaco model text, the persisted
// draftBody and the invoke payload are all the same string, with the editor merely HIDING the
// prefix/suffix lines. `=> (` … `)` is load-bearing — without it `{ … }` parses as a block.

export const WRAP_PREFIX = "export default async (): Promise<RequestMessage> => (\n";
export const WRAP_SUFFIX = "\n)";
export const PREFIX_LINES = 1;
export const SUFFIX_LINES = 1;

export const wrap = (body: string): string => WRAP_PREFIX + body + WRAP_SUFFIX;

export const isCanonical = (text: string): boolean =>
  text.startsWith(WRAP_PREFIX) && text.endsWith(WRAP_SUFFIX);

// Never `wrap("")`, which serializes to `=> ()` — a JS syntax error.
const EMPTY_BODY = "{\n  \n}";

// migrateBodyToTs normalizes a body to the canonical module. Idempotent.
export const migrateBodyToTs = (body: string): string => {
  if (isCanonical(body)) return body;
  const trimmed = body.trim();
  if (trimmed === "" || trimmed === "{}") return wrap(EMPTY_BODY);
  return wrap(trimmed);
};
