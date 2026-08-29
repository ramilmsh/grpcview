// The TypeScript request body: the Monaco model text, the persisted draftBody and the invoke
// payload are all the same string, with the editor merely hiding the machine-owned lines around
// an author-visible region (script-region.ts). `=> (` … `)` is load-bearing — without it `{ … }`
// parses as a block, not an object literal.
//
// Two forms only: a JSON-like object literal (wrapped) or a module the author owns (never
// wrapped, nothing hidden). Which one a given text is comes from leadsWithBrace, NOT from
// sniffing `export default` — a body is wrapped if and only if its first token is `{`.
//
// Per D2 (docs/design/planned/script-region.md) the skeleton carries no standard imports:
// `invoke` and `params` are ordinary auto-import candidates, not an always-on prefix.
import {
  buildWrapped,
  findRegion,
  normalizeSkeleton,
  regionHiddenRanges,
  regionBounds,
} from "./script-region";
import { leadsWithBrace } from "./module-sniff";

export const BODY_SKELETON =
  "export default async (): Promise<RequestMessage> => (";

export const isWrapped = (text: string): boolean =>
  findRegion(text) !== undefined;

export const wrap = (body: string): string =>
  buildWrapped({ skeleton: BODY_SKELETON, region: body });

// Never `wrap("")`, which serializes to `=> ()` — a JS syntax error.
const EMPTY_BODY = "{\n  \n}";

// migrateBodyToTs wraps a JSON-like body into the marked region, repairs the skeleton of an
// already-wrapped body, or leaves anything else — an author-written module, a bare expression, a
// `[` array — untouched, byte-for-byte. Idempotent.
//
// The store (service/store/codec.go's writeSourceFile) normalizes body.ts to exactly one trailing
// newline on write, so a persisted body arrives here as "<content>\n". Stripping trailing
// newlines before the region scan is what keeps the round trip byte-identical: the store re-adds
// exactly one on the next write.
export const migrateBodyToTs = (body: string): string => {
  const stripped = body.replace(/\n+$/, "");
  if (findRegion(stripped)) return normalizeSkeleton(stripped, BODY_SKELETON);
  const trimmed = stripped.trim();
  if (trimmed === "" || trimmed === "{}") return wrap(EMPTY_BODY);
  if (!leadsWithBrace(stripped)) return stripped;
  return wrap(trimmed);
};

export const bodyBounds = regionBounds;
export const hiddenLineRanges = regionHiddenRanges;
