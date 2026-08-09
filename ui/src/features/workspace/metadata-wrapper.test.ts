import { describe, expect, it } from "vitest";
import {
  defaultMetadataModule,
  hiddenLineRanges,
  isCanonical,
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
    expect(ranges[0]).toEqual({ startLine: 1, endLine: 1 });
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
