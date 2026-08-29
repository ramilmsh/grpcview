import { describe, it } from "node:test";
import { expect } from "expect";
import {
  BODY_SKELETON,
  bodyBounds,
  hiddenLineRanges,
  isWrapped,
  migrateBodyToTs,
  wrap,
} from "./body-wrapper";
import { buildWrapped, END_MARKER, findRegion, START_MARKER } from "./script-region";

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
// whose imports are its own (not the wrapper's), and which carries no `grpcview:script` markers.
// It must be left entirely alone.
const RUNSCRIPT_GENERATORS_BODY = [
  'import { requestId } from "#/scripts/ids";',
  'import { stamp } from "#/scripts/stamp";',
  "",
  "export default async (): Promise<RequestMessage> => (",
  "{",
  '  collection: "example",',
  "  source: JSON.stringify(`${requestId(\"gv\")} at ${stamp()}`),",
  "}",
  ")",
].join("\n");

// D11: a body.ts left over from the marker-less wrapper this design replaces. It has no markers
// and does not lead with `{` (it starts with `import`), so it reads as a plain script — there is
// no migration path back into a marked region. This is a deliberate one-time break.
const OLD_MARKERLESS_WRAPPED_BODY = [
  'import { invoke } from "grpcview:invoke";',
  'import { params } from "grpcview:request";',
  "",
  "export default async (): Promise<RequestMessage> => (",
  "{}",
  ")",
].join("\n");

const BARE_OBJECT = "{\n  ok: true,\n}";

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

  it("leaves the runscript-generators example body untouched, byte-identical", () => {
    const migrated = migrateBodyToTs(RUNSCRIPT_GENERATORS_BODY);
    expect(migrated).toBe(RUNSCRIPT_GENERATORS_BODY);
    expect(hiddenLineRanges(migrated)).toEqual([]);
  });

  it("D11: leaves the old marker-less wrapper as a plain script, unmigrated", () => {
    const migrated = migrateBodyToTs(OLD_MARKERLESS_WRAPPED_BODY);
    expect(migrated).toBe(OLD_MARKERLESS_WRAPPED_BODY);
    expect(isWrapped(migrated)).toBe(false);
    expect(hiddenLineRanges(migrated)).toEqual([]);
  });

  it("still wraps a bare object literal", () => {
    const migrated = migrateBodyToTs(BARE_OBJECT);
    expect(migrated).toBe(wrap(BARE_OBJECT.trim()));
    expect(isWrapped(migrated)).toBe(true);
  });

  it('still wraps "" as the empty-body seed', () => {
    const migrated = migrateBodyToTs("");
    expect(isWrapped(migrated)).toBe(true);
    expect(migrated).not.toBe("");
  });

  it('still wraps "{}" as the empty-body seed', () => {
    const migrated = migrateBodyToTs("{}");
    expect(isWrapped(migrated)).toBe(true);
  });

  it("leaves a bare expression that does not lead with `{` alone", () => {
    expect(migrateBodyToTs("[1, 2, 3]")).toBe("[1, 2, 3]");
    expect(migrateBodyToTs("makeBody()")).toBe("makeBody()");
  });

  it("carries no standard imports in the skeleton", () => {
    expect(BODY_SKELETON).not.toContain("import");
    const migrated = wrap("{}");
    expect(migrated).not.toContain("grpcview:invoke");
    expect(migrated).not.toContain("grpcview:request");
  });

  it("does not treat a `// ` comment mentioning export default as a module", () => {
    const migrated = migrateBodyToTs(MODULE_WITH_COMMENT_MENTION);
    expect(isWrapped(migrated)).toBe(true);
    expect(migrated).toBe(wrap(MODULE_WITH_COMMENT_MENTION.trim()));
  });

  it("does not treat a `/* */` comment mentioning export default as a module", () => {
    const migrated = migrateBodyToTs(MODULE_WITH_BLOCK_COMMENT_MENTION);
    expect(isWrapped(migrated)).toBe(true);
    expect(migrated).toBe(wrap(MODULE_WITH_BLOCK_COMMENT_MENTION.trim()));
  });

  it("repairs a wrapped body carrying the wrong skeleton line", () => {
    const wrongSkeleton = buildWrapped({
      skeleton: "export default async () => (",
      region: "{\n  ok: true,\n}",
    });
    const migrated = migrateBodyToTs(wrongSkeleton);
    expect(migrated).toBe(wrap("{\n  ok: true,\n}"));
  });

  it("preserves a machine-owned import block above the region", () => {
    const withImport = buildWrapped({
      imports: ['import { requestId } from "#/scripts/ids";'],
      skeleton: BODY_SKELETON,
      region: "{\n  id: requestId(),\n}",
    });
    expect(migrateBodyToTs(withImport)).toBe(withImport);
  });

  for (const [name, body] of Object.entries({
    "module with imports": MODULE_WITH_IMPORTS,
    "module without imports": MODULE_NO_IMPORTS,
    "runscript-generators example body": RUNSCRIPT_GENERATORS_BODY,
    "old marker-less wrapper": OLD_MARKERLESS_WRAPPED_BODY,
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
    it("is still recognized as wrapped, and the round trip is byte-identical", () => {
      const wrapped = wrap(BARE_OBJECT.trim());
      const migrated = migrateBodyToTs(wrapped + "\n");
      expect(migrated).toBe(wrapped);
      expect(isWrapped(migrated)).toBe(true);
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
    expect(hiddenLineRanges(MODULE_NO_IMPORTS)).toEqual([]);
  });

  it("hides line 1 through the start marker and the end marker through EOF for a wrapped bare object", () => {
    const migrated = migrateBodyToTs(BARE_OBJECT);
    const region = findRegion(migrated)!;
    const ranges = hiddenLineRanges(migrated);
    expect(ranges).toEqual([
      { startLine: 1, endLine: region.startLine },
      { startLine: region.endLine, endLine: region.total },
    ]);
  });

  it("puts the editable region strictly between the markers", () => {
    const migrated = migrateBodyToTs(BARE_OBJECT);
    const lines = migrated.split("\n");
    const bounds = bodyBounds(migrated);
    expect(lines[bounds.first - 2].trim()).toBe(START_MARKER);
    expect(lines[bounds.last].trim()).toBe(END_MARKER);
  });

  it("stays put across a store-normalized trailing newline", () => {
    const migrated = migrateBodyToTs(wrap(BARE_OBJECT.trim()) + "\n");
    const bounds = bodyBounds(migrated);
    expect(bounds.first).toBeGreaterThan(1);
    expect(bounds.last).toBeLessThan(bounds.total);
  });
});

describe("isWrapped", () => {
  it("recognizes a marked region", () => {
    expect(isWrapped(wrap("{}"))).toBe(true);
  });

  it("rejects a module that merely contains export default", () => {
    expect(isWrapped(MODULE_WITH_IMPORTS)).toBe(false);
  });

  it("rejects the old marker-less wrapper", () => {
    expect(isWrapped(OLD_MARKERLESS_WRAPPED_BODY)).toBe(false);
  });
});
