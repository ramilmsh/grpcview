import { describe, expect, it } from "vitest";
import {
  defaultMetadataModule,
  hiddenLineRanges,
  hostMetadataScript,
  isCanonical,
  META_PREFIX_LINES,
  META_WRAP_PREFIX,
  META_WRAP_SUFFIX,
  metaBounds,
  migrateMetadataToTs,
  wrap,
} from "./metadata-wrapper";

// example's `RunScript (generators)` metadata script: a module reached via `import`, the same
// shape that exposed the double-wrap bug in body-wrapper.ts.
const MODULE_WITH_IMPORTS = [
  'import { traceId } from "#/scripts/ids";',
  "",
  "export default async (): Promise<Metadata> => ({",
  '  "x-trace-id": [traceId()],',
  "});",
].join("\n");

const MODULE_NO_IMPORTS = ["export default async () => ({", '  a: ["b"],', "});"].join("\n");

describe("metaBounds / hiddenLineRanges", () => {
  it("hides nothing for a module with leading imports", () => {
    expect(hiddenLineRanges(MODULE_WITH_IMPORTS)).toEqual([]);
    const bounds = metaBounds(MODULE_WITH_IMPORTS);
    expect(bounds.first).toBe(1);
    expect(bounds.last).toBe(bounds.total);
  });

  it("hides nothing for a module with no imports", () => {
    expect(hiddenLineRanges(MODULE_NO_IMPORTS)).toEqual([]);
  });

  it("still hides the prefix/suffix wrapper lines for the canonical default module", () => {
    const canonical = defaultMetadataModule();
    const ranges = hiddenLineRanges(canonical);
    expect(ranges.length).toBe(2);
    expect(ranges[0]).toEqual({ startLine: 1, endLine: META_PREFIX_LINES });
    const total = canonical.split("\n").length;
    expect(ranges[1]).toEqual({ startLine: total, endLine: total });
  });

  it("still hides the prefix/suffix wrapper lines for a migrated history entry", () => {
    const canonical = migrateMetadataToTs({ "x-request-id": "abc" });
    expect(isCanonical(canonical)).toBe(true);
    expect(hiddenLineRanges(canonical).length).toBe(2);
  });
});

describe("isCanonical", () => {
  it("recognizes the exact wrap", () => {
    expect(isCanonical(META_WRAP_PREFIX + "{}" + META_WRAP_SUFFIX)).toBe(true);
  });

  it("rejects a module that merely contains export default", () => {
    expect(isCanonical(MODULE_WITH_IMPORTS)).toBe(false);
  });

  it("recognizes the default (inherit()) metadata module", () => {
    expect(isCanonical(defaultMetadataModule())).toBe(true);
  });
});

describe("migrateMetadataToTs", () => {
  it("wraps an empty seed and is idempotent to re-wrap", () => {
    const empty = migrateMetadataToTs();
    expect(isCanonical(empty)).toBe(true);
    expect(migrateMetadataToTs()).toBe(empty);
  });

  it("renders every value as a string[] literal", () => {
    const migrated = migrateMetadataToTs({ "x-a": "1", "x-b": ["2", "3"] });
    expect(isCanonical(migrated)).toBe(true);
    expect(migrated).toContain('"x-a": ["1"]');
    expect(migrated).toContain('"x-b": ["2", "3"]');
  });
});

describe("wrap", () => {
  it("round-trips through isCanonical", () => {
    expect(isCanonical(wrap("{}"))).toBe(true);
  });
});

// The store (service/store/codec.go's writeSourceFile) normalizes metadata.ts to exactly one
// trailing newline on write, so every persisted script arrives here as "<content>\n".
describe("hostMetadataScript", () => {
  it("recognizes a canonical script + trailing newline as canonical after hosting", () => {
    const canonical = defaultMetadataModule();
    const hosted = hostMetadataScript(canonical + "\n");
    expect(hosted).toBe(canonical);
    expect(isCanonical(hosted)).toBe(true);
  });

  it("hides the whole import prefix and one suffix line for the hosted text", () => {
    const hosted = hostMetadataScript(defaultMetadataModule() + "\n");
    const ranges = hiddenLineRanges(hosted);
    expect(ranges.length).toBe(2);
    expect(ranges[0]).toEqual({ startLine: 1, endLine: META_PREFIX_LINES });
    expect(ranges[1].endLine - ranges[1].startLine).toBe(0);
  });

  it("puts the editable region strictly inside the wrapper for the hosted text", () => {
    const hosted = hostMetadataScript(defaultMetadataModule() + "\n");
    const bounds = metaBounds(hosted);
    expect(bounds.first).toBeGreaterThan(1);
    expect(bounds.last).toBeLessThan(bounds.total);
  });

  it("leaves an author-written module unchanged apart from the stripped newline", () => {
    const hosted = hostMetadataScript(MODULE_WITH_IMPORTS + "\n");
    expect(hosted).toBe(MODULE_WITH_IMPORTS);
    expect(hiddenLineRanges(hosted)).toEqual([]);
  });

  it("returns an empty script unchanged", () => {
    expect(hostMetadataScript("")).toBe("");
  });

  it("wraps a hand-written JSON-like script, whose first token is `{`", () => {
    const hosted = hostMetadataScript('{\n  "x-a": ["1"],\n}\n');
    expect(isCanonical(hosted)).toBe(true);
    expect(hosted).toBe(wrap('{\n  "x-a": ["1"],\n}'));
  });

  it("wraps a JSON-like script behind a leading comment", () => {
    const hosted = hostMetadataScript('// headers\n{ "x-a": ["1"] }');
    expect(isCanonical(hosted)).toBe(true);
  });

  it("leaves a module that never mentions export default alone", () => {
    const helper = 'const md: Metadata = {};\nexport { md };';
    expect(hostMetadataScript(helper)).toBe(helper);
    expect(hiddenLineRanges(helper)).toEqual([]);
  });

  it("carries invoke and inherit in the hidden prefix", () => {
    expect(META_WRAP_PREFIX).toContain('import { invoke } from "grpcview:invoke";');
    expect(META_WRAP_PREFIX).toContain('import { inherit } from "grpcview:metadata";');
    expect(META_WRAP_PREFIX).not.toContain("grpcview:request");
    expect(META_WRAP_PREFIX.split("\n").length - 1).toBe(META_PREFIX_LINES);
  });

  it("is idempotent", () => {
    const once = hostMetadataScript(defaultMetadataModule() + "\n");
    const twice = hostMetadataScript(once);
    expect(twice).toBe(once);
  });
});
