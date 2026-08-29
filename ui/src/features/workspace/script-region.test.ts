import { describe, it } from "node:test";
import { expect } from "expect";
import {
  buildWrapped,
  END_MARKER,
  findRegion,
  normalizeSkeleton,
  regionBounds,
  regionHeader,
  regionHiddenRanges,
  regionText,
  START_MARKER,
} from "./script-region";

const SKELETON = "export default async (): Promise<RequestMessage> => (";

const WRAPPED = buildWrapped({ skeleton: SKELETON, region: "{\n  id: 1,\n}" });

const WRAPPED_WITH_IMPORTS = buildWrapped({
  imports: [
    'import { inherit } from "grpcview:metadata";',
    'import { invoke } from "grpcview:invoke";',
  ],
  skeleton: SKELETON,
  region: "{\n  id: 1,\n}",
});

describe("findRegion", () => {
  it("finds a well-formed region", () => {
    const region = findRegion(WRAPPED);
    expect(region).toBeDefined();
    expect(region!.startLine).toBe(2);
    expect(region!.endLine).toBe(6);
    expect(region!.total).toBe(WRAPPED.split("\n").length);
  });

  it("returns undefined for a plain script with no markers", () => {
    expect(findRegion("export default async () => ({ ok: true });")).toBeUndefined();
  });

  it("returns undefined with only a start marker", () => {
    expect(findRegion([SKELETON, START_MARKER, "{}", ")"].join("\n"))).toBeUndefined();
  });

  it("returns undefined with only an end marker", () => {
    expect(findRegion([SKELETON, "{}", END_MARKER, ")"].join("\n"))).toBeUndefined();
  });

  it("returns undefined with two start markers", () => {
    const text = [SKELETON, START_MARKER, START_MARKER, "{}", END_MARKER, ")"].join("\n");
    expect(findRegion(text)).toBeUndefined();
  });

  it("returns undefined with two end markers", () => {
    const text = [SKELETON, START_MARKER, "{}", END_MARKER, END_MARKER, ")"].join("\n");
    expect(findRegion(text)).toBeUndefined();
  });

  it("returns undefined when the end marker precedes the start marker", () => {
    const text = [END_MARKER, "{}", START_MARKER].join("\n");
    expect(findRegion(text)).toBeUndefined();
  });

  it("returns undefined for a zero-line region (markers on adjacent lines)", () => {
    const text = [SKELETON, START_MARKER, END_MARKER, ")"].join("\n");
    expect(findRegion(text)).toBeUndefined();
  });

  it("finds a marker line with leading/trailing whitespace", () => {
    const text = [SKELETON, `  ${START_MARKER}  `, "{}", `\t${END_MARKER}`, ")"].join("\n");
    const region = findRegion(text);
    expect(region).toBeDefined();
    expect(region!.startLine).toBe(2);
    expect(region!.endLine).toBe(4);
  });

  it("does not match marker text that is merely part of a longer line", () => {
    const text = [SKELETON, `foo ${START_MARKER} bar`, "{}", END_MARKER, ")"].join("\n");
    expect(findRegion(text)).toBeUndefined();
  });

  it("does match marker text sitting alone on a line inside a template literal (documented false positive)", () => {
    // findRegion is a pure line scan; it does not track string/template state. A template literal
    // whose content is, on its own line, exactly the marker text is indistinguishable from a real
    // marker. This is accepted (see the file header) rather than hand-rolling a lexer for it.
    const text = [
      "const s = `",
      START_MARKER,
      "body",
      END_MARKER,
      "`;",
    ].join("\n");
    expect(findRegion(text)).toBeDefined();
  });
});

describe("regionBounds / regionHiddenRanges", () => {
  it("bounds the region strictly between the markers", () => {
    const bounds = regionBounds(WRAPPED);
    const lines = WRAPPED.split("\n");
    expect(lines[bounds.first - 1]).toBe("{");
    expect(lines[bounds.last - 1]).toBe("}");
  });

  it("hides line 1 through the start marker, and the end marker through EOF", () => {
    const ranges = regionHiddenRanges(WRAPPED);
    const region = findRegion(WRAPPED)!;
    expect(ranges).toEqual([
      { startLine: 1, endLine: region.startLine },
      { startLine: region.endLine, endLine: region.total },
    ]);
  });

  it("treats the whole document as the region, with nothing hidden, when there is no region", () => {
    const plain = "export default async () => ({ ok: true });";
    expect(regionHiddenRanges(plain)).toEqual([]);
    const bounds = regionBounds(plain);
    expect(bounds.first).toBe(1);
    expect(bounds.last).toBe(bounds.total);
  });

  it("is unaffected by a store-normalized trailing newline", () => {
    const withTrailingNewline = WRAPPED + "\n";
    const plain = regionBounds(WRAPPED);
    const withNL = regionBounds(withTrailingNewline);
    expect(withNL.first).toBe(plain.first);
    expect(withNL.last).toBe(plain.last);
    expect(withNL.total).toBe(plain.total + 1);

    const plainRanges = regionHiddenRanges(WRAPPED);
    const nlRanges = regionHiddenRanges(withTrailingNewline);
    expect(nlRanges[0]).toEqual(plainRanges[0]);
    expect(nlRanges[1].startLine).toBe(plainRanges[1].startLine);
    expect(nlRanges[1].endLine).toBe(plainRanges[1].endLine + 1);
  });
});

describe("regionText", () => {
  it("returns the text strictly between the markers", () => {
    expect(regionText(WRAPPED)).toBe("{\n  id: 1,\n}");
  });

  it("returns undefined when there is no region", () => {
    expect(regionText("export default async () => ({ ok: true });")).toBeUndefined();
  });
});

describe("buildWrapped", () => {
  it("builds the exact shape with no imports", () => {
    expect(buildWrapped({ skeleton: SKELETON, region: "{}" })).toBe(
      [SKELETON, START_MARKER, "{}", END_MARKER, ")"].join("\n"),
    );
  });

  it("builds the exact shape with imports, separated by exactly one blank line", () => {
    expect(
      buildWrapped({
        imports: ['import { requestId } from "#/scripts/ids";'],
        skeleton: SKELETON,
        region: "{}",
      }),
    ).toBe(
      [
        'import { requestId } from "#/scripts/ids";',
        "",
        SKELETON,
        START_MARKER,
        "{}",
        END_MARKER,
        ")",
      ].join("\n"),
    );
  });

  it("produces no trailing newline", () => {
    expect(buildWrapped({ skeleton: SKELETON, region: "{}" }).endsWith("\n")).toBe(false);
  });
});

describe("normalizeSkeleton", () => {
  it("repairs a wrong skeleton line", () => {
    const wrongSkeleton = "export default async () => (";
    const text = buildWrapped({ skeleton: wrongSkeleton, region: "{}" });
    const fixed = normalizeSkeleton(text, SKELETON);
    expect(fixed).toBe(buildWrapped({ skeleton: SKELETON, region: "{}" }));
  });

  it("preserves the import block verbatim", () => {
    const fixed = normalizeSkeleton(WRAPPED_WITH_IMPORTS, SKELETON);
    expect(fixed).toBe(WRAPPED_WITH_IMPORTS);
  });

  it("is idempotent", () => {
    const once = normalizeSkeleton(WRAPPED_WITH_IMPORTS, SKELETON);
    const twice = normalizeSkeleton(once, SKELETON);
    expect(twice).toBe(once);
  });

  it("returns text unchanged when there is no region", () => {
    const plain = "export default async () => ({ ok: true });";
    expect(normalizeSkeleton(plain, SKELETON)).toBe(plain);
  });

  it("stays byte-identical across a store-normalized trailing newline round trip", () => {
    const stripped = (WRAPPED_WITH_IMPORTS + "\n").replace(/\n+$/, "");
    expect(normalizeSkeleton(stripped, SKELETON)).toBe(WRAPPED_WITH_IMPORTS);
  });
});

describe("regionHeader", () => {
  it("returns everything above the start marker", () => {
    expect(regionHeader(WRAPPED_WITH_IMPORTS)).toBe(
      [
        'import { inherit } from "grpcview:metadata";',
        'import { invoke } from "grpcview:invoke";',
        "",
        SKELETON,
      ].join("\n"),
    );
  });

  it("returns the whole text when there is no region", () => {
    const plain = "export default async () => ({ ok: true });";
    expect(regionHeader(plain)).toBe(plain);
  });

  it("is a prefix of the original text", () => {
    const header = regionHeader(WRAPPED_WITH_IMPORTS);
    expect(WRAPPED_WITH_IMPORTS.startsWith(header)).toBe(true);
  });
});
