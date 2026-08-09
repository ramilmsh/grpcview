// Canonical wrapping for the TypeScript request body: the Monaco model text, the persisted
// draftBody and the invoke payload are all the same string, with the editor merely HIDING the
// prefix/suffix lines. `=> (` … `)` is load-bearing — without it `{ … }` parses as a block.
//
// Two forms only: a JSON-like object literal (wrapped) or a module the author owns (never
// wrapped, nothing hidden). Which one a given text is comes from leadsWithBrace, NOT from
// sniffing `export default` — a body is wrapped if and only if its first token is `{`.
//
// The hidden prefix carries `invoke` and `params` already imported. They cost nothing when
// unused (no noUnusedLocals in monaco-scripts.ts's compiler options, and esbuild drops them),
// and they are what makes a wrapped body able to `await invoke(…)` or read `params.x` without
// the author first having to break out of the JSON-like form.
import { leadsWithBrace } from "./module-sniff";

export const WRAP_PREFIX =
  'import { invoke } from "grpcview:invoke";\n' +
  'import { params } from "grpcview:request";\n' +
  "\n" +
  "export default async (): Promise<RequestMessage> => (\n";
export const WRAP_SUFFIX = "\n)";
export const PREFIX_LINES = 4;
export const SUFFIX_LINES = 1;

export const wrap = (body: string): string => WRAP_PREFIX + body + WRAP_SUFFIX;

export const isCanonical = (text: string): boolean =>
  text.startsWith(WRAP_PREFIX) && text.endsWith(WRAP_SUFFIX);

// Never `wrap("")`, which serializes to `=> ()` — a JS syntax error.
const EMPTY_BODY = "{\n  \n}";

// migrateBodyToTs wraps a JSON-like body into the canonical module, or leaves anything else —
// an author-written module, a bare expression, a `[` array — untouched, byte-for-byte. Idempotent.
//
// The store (service/store/codec.go's writeSourceFile) normalizes body.ts/metadata.ts to exactly
// one trailing newline on write, so a persisted body arrives here as "<canonical>\n" — one byte
// isCanonical never matched, since WRAP_SUFFIX ends in `)`, not `\n`. Stripping trailing newlines
// before the canonical check is what re-recognizes it; the store re-adds exactly one on the next
// write, so the round trip is byte-identical and produces no spurious git diff.
export const migrateBodyToTs = (body: string): string => {
  const stripped = body.replace(/\n+$/, "");
  if (isCanonical(stripped)) return stripped;
  const trimmed = stripped.trim();
  if (trimmed === "" || trimmed === "{}") return wrap(EMPTY_BODY);
  if (!leadsWithBrace(stripped)) return stripped;
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
