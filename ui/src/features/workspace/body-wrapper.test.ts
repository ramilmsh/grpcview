import { describe, expect, it } from "vitest";
import {
  bodyBounds,
  hiddenLineRanges,
  isCanonical,
  migrateBodyToTs,
  PREFIX_LINES,
  wrap,
  WRAP_PREFIX,
  WRAP_SUFFIX,
} from "./body-wrapper";

const MODULE_WITH_IMPORTS = [
  'import { requestId } from "#/scripts/ids";',
  "",
  "export default async (): Promise<RequestMessage> => ({",
  "  id: requestId(),",
  "});",
].join("\n");

const MODULE_NO_IMPORTS = ["export default async () => ({", "  ok: true,", "});"].join("\n");

// The regression fixture: example/tree/workspace/runscript-generators/body.ts — a module whose
// own `export default async (): Promise<RequestMessage> => (` line reads like the wrapper's, and
// whose imports are NOT the wrapper's two, so it must be left entirely alone.
const RUNSCRIPT_GENERATORS_BODY = [
  'import { requestId } from "#/scripts/ids";',
  'import { stamp } from "#/scripts/stamp";',
  "",
  "export default async (): Promise<RequestMessage> => (",
  "{",
  '  collection: "example",',
  "  // `requestId` and `stamp` are files in this collection, imported by path.",
  "  // Nothing is bound by name and nothing is pulled in implicitly: the import",
  "  // graph above is exactly what the sandbox bundles.",
  "  //",
  "  // JSON.stringify turns the generated text into a quoted string literal, so",
  "  // the scratchpad grpcview runs is a single expression and its value comes",
  "  // straight back in the response.",
  '  source: JSON.stringify(`${requestId("gv")} at ${stamp()}`),',
  "}",
  ")",
].join("\n");

const BARE_OBJECT = '{\n  ok: true,\n}';

const MODULE_WITH_COMMENT_MENTION = [
  "// there is no export default here",
  "{",
  "  ok: true,",
  "}",
].join("\n");

const MODULE_WITH_BLOCK_COMMENT_MENTION = [
  "/* export default lives elsewhere */",
  "{ ok: true }",
].join("\n");

describe("migrateBodyToTs", () => {
  it("leaves a module with leading imports untouched, byte-identical", () => {
    expect(migrateBodyToTs(MODULE_WITH_IMPORTS)).toBe(MODULE_WITH_IMPORTS);
  });

  it("leaves a module with no imports untouched", () => {
    expect(migrateBodyToTs(MODULE_NO_IMPORTS)).toBe(MODULE_NO_IMPORTS);
  });

  it("leaves the runscript-generators example body untouched, byte-identical, one wrap only", () => {
    const migrated = migrateBodyToTs(RUNSCRIPT_GENERATORS_BODY);
    expect(migrated).toBe(RUNSCRIPT_GENERATORS_BODY);
    expect(migrated.match(/export default/g)?.length).toBe(1);
    expect(hiddenLineRanges(migrated)).toEqual([]);
  });

  it("still wraps a bare object literal", () => {
    const migrated = migrateBodyToTs(BARE_OBJECT);
    expect(migrated).toBe(wrap(BARE_OBJECT.trim()));
    expect(isCanonical(migrated)).toBe(true);
  });

  it('still wraps "" as the empty-body seed', () => {
    const migrated = migrateBodyToTs("");
    expect(isCanonical(migrated)).toBe(true);
    expect(migrated).not.toBe("");
  });

  it('still wraps "{}" as the empty-body seed', () => {
    const migrated = migrateBodyToTs("{}");
    expect(isCanonical(migrated)).toBe(true);
  });

  it("leaves a bare expression that does not lead with `{` alone", () => {
    expect(migrateBodyToTs("[1, 2, 3]")).toBe("[1, 2, 3]");
    expect(migrateBodyToTs("makeBody()")).toBe("makeBody()");
  });

  it("carries invoke and params in the hidden prefix", () => {
    expect(WRAP_PREFIX).toContain('import { invoke } from "grpcview:invoke";');
    expect(WRAP_PREFIX).toContain('import { params } from "grpcview:request";');
    expect(WRAP_PREFIX.split("\n").length - 1).toBe(PREFIX_LINES);
  });

  it("does not treat a `// ` comment mentioning export default as a module", () => {
    const migrated = migrateBodyToTs(MODULE_WITH_COMMENT_MENTION);
    expect(isCanonical(migrated)).toBe(true);
    expect(migrated).toBe(wrap(MODULE_WITH_COMMENT_MENTION.trim()));
  });

  it("does not treat a `/* */` comment mentioning export default as a module", () => {
    const migrated = migrateBodyToTs(MODULE_WITH_BLOCK_COMMENT_MENTION);
    expect(isCanonical(migrated)).toBe(true);
    expect(migrated).toBe(wrap(MODULE_WITH_BLOCK_COMMENT_MENTION.trim()));
  });

  for (const [name, body] of Object.entries({
    "module with imports": MODULE_WITH_IMPORTS,
    "module without imports": MODULE_NO_IMPORTS,
    "runscript-generators example body": RUNSCRIPT_GENERATORS_BODY,
    "bare object": BARE_OBJECT,
    empty: "",
    "empty object": "{}",
    "comment mentioning export default": MODULE_WITH_COMMENT_MENTION,
    "block comment mentioning export default": MODULE_WITH_BLOCK_COMMENT_MENTION,
  })) {
    it(`is idempotent for: ${name}`, () => {
      const once = migrateBodyToTs(body);
      const twice = migrateBodyToTs(once);
      expect(twice).toBe(once);
    });
  }

  // The store (service/store/codec.go's writeSourceFile) normalizes body.ts to exactly one
  // trailing newline on write, so every persisted body arrives here as "<content>\n".
  describe("a store-normalized trailing newline", () => {
    it("is still recognized as canonical", () => {
      const canonical = wrap(BARE_OBJECT.trim());
      const migrated = migrateBodyToTs(canonical + "\n");
      expect(migrated).toBe(canonical);
      expect(isCanonical(migrated)).toBe(true);
    });

    it("leaves an author-written module unchanged apart from the stripped newline", () => {
      const migrated = migrateBodyToTs(MODULE_WITH_IMPORTS + "\n");
      expect(migrated).toBe(MODULE_WITH_IMPORTS);
      expect(hiddenLineRanges(migrated)).toEqual([]);
    });

    it("hosting is idempotent", () => {
      const once = migrateBodyToTs(wrap(BARE_OBJECT.trim()) + "\n");
      const twice = migrateBodyToTs(once);
      expect(twice).toBe(once);
    });
  });
});

describe("hiddenLineRanges / bodyBounds", () => {
  it("hides nothing for a module with leading imports", () => {
    const migrated = migrateBodyToTs(MODULE_WITH_IMPORTS);
    expect(hiddenLineRanges(migrated)).toEqual([]);
    const bounds = bodyBounds(migrated);
    expect(bounds.first).toBe(1);
    expect(bounds.last).toBe(bounds.total);
  });

  it("hides nothing for a module with no imports", () => {
    const migrated = migrateBodyToTs(MODULE_NO_IMPORTS);
    expect(hiddenLineRanges(migrated)).toEqual([]);
  });

  it("still hides the prefix/suffix wrapper lines for a wrapped bare object", () => {
    const migrated = migrateBodyToTs(BARE_OBJECT);
    const ranges = hiddenLineRanges(migrated);
    expect(ranges.length).toBe(2);
    expect(ranges[0]).toEqual({ startLine: 1, endLine: PREFIX_LINES });
    const total = migrated.split("\n").length;
    expect(ranges[1]).toEqual({ startLine: total, endLine: total });
  });

  it("hides the whole import prefix and one suffix line for a store-normalized trailing newline", () => {
    const migrated = migrateBodyToTs(wrap(BARE_OBJECT.trim()) + "\n");
    const ranges = hiddenLineRanges(migrated);
    expect(ranges.length).toBe(2);
    expect(ranges[0]).toEqual({ startLine: 1, endLine: PREFIX_LINES });
    expect(ranges[1].endLine - ranges[1].startLine).toBe(0);
    const bounds = bodyBounds(migrated);
    expect(bounds.first).toBeGreaterThan(1);
    expect(bounds.last).toBeLessThan(bounds.total);
  });
});

describe("isCanonical", () => {
  it("recognizes the exact wrap", () => {
    expect(isCanonical(WRAP_PREFIX + "{}" + WRAP_SUFFIX)).toBe(true);
  });

  it("rejects a module that merely contains export default", () => {
    expect(isCanonical(MODULE_WITH_IMPORTS)).toBe(false);
  });
});
