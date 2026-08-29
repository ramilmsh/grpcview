import { describe, it } from "node:test";
import { expect } from "expect";
import {
  defaultMetadataModule,
  hiddenLineRanges,
  hostMetadataScript,
  isWrapped,
  META_SKELETON,
  metaBounds,
  migrateMetadataToTs,
  wrap,
} from "./metadata-wrapper";
import { buildWrapped, END_MARKER, findRegion, START_MARKER } from "./script-region";

// example's `RunScript (generators)` metadata script: a module reached via `import`, carrying no
// `grpcview:script` markers.
const MODULE_WITH_IMPORTS = [
  'import { traceId } from "#/scripts/ids";',
  "",
  "export default async (): Promise<Metadata> => ({",
  '  "x-trace-id": [traceId()],',
  "});",
].join("\n");

const MODULE_NO_IMPORTS = ["export default async () => ({", '  a: ["b"],', "});"].join("\n");

// D11: the old marker-less wrapper this design replaces. No markers, does not lead with `{` — a
// plain script, with no migration path back into a marked region.
const OLD_MARKERLESS_WRAPPED_METADATA = [
  'import { invoke } from "grpcview:invoke";',
  'import { inherit } from "grpcview:metadata";',
  "",
  "export default async (): Promise<Metadata> => (",
  "{}",
  ")",
].join("\n");

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

  it("hides line 1 through the start marker and the end marker through EOF for the default module", () => {
    const canonical = defaultMetadataModule();
    const region = findRegion(canonical)!;
    const ranges = hiddenLineRanges(canonical);
    expect(ranges).toEqual([
      { startLine: 1, endLine: region.startLine },
      { startLine: region.endLine, endLine: region.total },
    ]);
  });

  it("hides the wrapper for a migrated history entry", () => {
    const migrated = migrateMetadataToTs({ "x-request-id": "abc" });
    expect(isWrapped(migrated)).toBe(true);
    expect(hiddenLineRanges(migrated).length).toBe(2);
  });
});

describe("isWrapped", () => {
  it("recognizes a marked region", () => {
    expect(isWrapped(wrap("{}"))).toBe(true);
  });

  it("rejects a module that merely contains export default", () => {
    expect(isWrapped(MODULE_WITH_IMPORTS)).toBe(false);
  });

  it("recognizes the default (inherit()) metadata module", () => {
    expect(isWrapped(defaultMetadataModule())).toBe(true);
  });

  it("rejects the old marker-less wrapper", () => {
    expect(isWrapped(OLD_MARKERLESS_WRAPPED_METADATA)).toBe(false);
  });
});

describe("migrateMetadataToTs", () => {
  it("wraps an empty seed and is idempotent to re-wrap", () => {
    const empty = migrateMetadataToTs();
    expect(isWrapped(empty)).toBe(true);
    expect(migrateMetadataToTs()).toBe(empty);
  });

  it("renders every value as a string[] literal", () => {
    const migrated = migrateMetadataToTs({ "x-a": "1", "x-b": ["2", "3"] });
    expect(isWrapped(migrated)).toBe(true);
    expect(migrated).toContain('"x-a": ["1"]');
    expect(migrated).toContain('"x-b": ["2", "3"]');
  });
});

describe("wrap", () => {
  it("round-trips through isWrapped", () => {
    expect(isWrapped(wrap("{}"))).toBe(true);
  });
});

describe("defaultMetadataModule", () => {
  it("carries `inherit` as a derived import above the region, the one standard import that survives D2", () => {
    const canonical = defaultMetadataModule();
    expect(canonical).toContain('import { inherit } from "grpcview:metadata";');
    expect(canonical).not.toContain("grpcview:invoke");
    expect(META_SKELETON).not.toContain("import");
  });

  it("preserves that import block through skeleton normalization", () => {
    const canonical = defaultMetadataModule();
    expect(hostMetadataScript(canonical + "\n")).toBe(canonical);
  });
});

// The store (service/store/codec.go's writeSourceFile) normalizes metadata.ts to exactly one
// trailing newline on write, so every persisted script arrives here as "<content>\n".
describe("hostMetadataScript", () => {
  it("recognizes a wrapped script + trailing newline as wrapped after hosting, byte-identical", () => {
    const canonical = defaultMetadataModule();
    const hosted = hostMetadataScript(canonical + "\n");
    expect(hosted).toBe(canonical);
    expect(isWrapped(hosted)).toBe(true);
  });

  it("hides line 1 through the start marker and the end marker through EOF for the hosted text", () => {
    const hosted = hostMetadataScript(defaultMetadataModule() + "\n");
    const region = findRegion(hosted)!;
    const ranges = hiddenLineRanges(hosted);
    expect(ranges).toEqual([
      { startLine: 1, endLine: region.startLine },
      { startLine: region.endLine, endLine: region.total },
    ]);
  });

  it("puts the editable region strictly between the markers for the hosted text", () => {
    const hosted = hostMetadataScript(defaultMetadataModule() + "\n");
    const lines = hosted.split("\n");
    const bounds = metaBounds(hosted);
    expect(lines[bounds.first - 2].trim()).toBe(START_MARKER);
    expect(lines[bounds.last].trim()).toBe(END_MARKER);
  });

  it("leaves an author-written module unchanged apart from the stripped newline", () => {
    const hosted = hostMetadataScript(MODULE_WITH_IMPORTS + "\n");
    expect(hosted).toBe(MODULE_WITH_IMPORTS);
    expect(hiddenLineRanges(hosted)).toEqual([]);
  });

  it("D11: leaves the old marker-less wrapper as a plain script, unmigrated", () => {
    const hosted = hostMetadataScript(OLD_MARKERLESS_WRAPPED_METADATA);
    expect(hosted).toBe(OLD_MARKERLESS_WRAPPED_METADATA);
    expect(isWrapped(hosted)).toBe(false);
  });

  it("returns an empty script unchanged", () => {
    expect(hostMetadataScript("")).toBe("");
  });

  it("wraps a hand-written JSON-like script, whose first token is `{`", () => {
    const hosted = hostMetadataScript('{\n  "x-a": ["1"],\n}\n');
    expect(isWrapped(hosted)).toBe(true);
    expect(hosted).toBe(wrap('{\n  "x-a": ["1"],\n}'));
  });

  it("wraps a JSON-like script behind a leading comment", () => {
    const hosted = hostMetadataScript('// headers\n{ "x-a": ["1"] }');
    expect(isWrapped(hosted)).toBe(true);
  });

  it("leaves a module that never mentions export default alone", () => {
    const helper = "const md: Metadata = {};\nexport { md };";
    expect(hostMetadataScript(helper)).toBe(helper);
    expect(hiddenLineRanges(helper)).toEqual([]);
  });

  it("repairs a wrapped script carrying the wrong skeleton line", () => {
    const wrongSkeleton = buildWrapped({
      skeleton: "export default async () => (",
      region: "{\n  ok: true,\n}",
    });
    const hosted = hostMetadataScript(wrongSkeleton);
    expect(hosted).toBe(wrap("{\n  ok: true,\n}"));
  });

  it("carries no standard imports in the skeleton beyond the derived default seed", () => {
    expect(META_SKELETON).not.toContain("import");
    expect(wrap("{}")).not.toContain("grpcview:invoke");
    expect(wrap("{}")).not.toContain("grpcview:metadata");
  });

  it("is idempotent", () => {
    const once = hostMetadataScript(defaultMetadataModule() + "\n");
    const twice = hostMetadataScript(once);
    expect(twice).toBe(once);
  });
});
