import { describe, expect, it } from "vitest";
import { modeSwitchFor, unwrapEdits, wrapEdits, type LineEdit } from "./region-edits";
import { buildWrapped, findRegion, regionText } from "./script-region";

const SKELETON = "export default async (): Promise<RequestMessage> => (";

const wrapped = (region: string, imports?: readonly string[]) =>
  buildWrapped({ imports, skeleton: SKELETON, region });

// A small line/column splicing helper, independent of monaco, so tests assert on the resulting
// TEXT rather than only on the edit objects — a wrong column shows up as garbled output.
function toOffset(text: string, line: number, column: number): number {
  const lines = text.split("\n");
  let offset = 0;
  for (let i = 0; i < line - 1; i++) offset += lines[i].length + 1;
  return offset + (column - 1);
}

function applyEdits(text: string, edits: LineEdit[]): string {
  const withOffsets = edits
    .map((e) => ({
      start: toOffset(text, e.range.startLineNumber, e.range.startColumn),
      end: toOffset(text, e.range.endLineNumber, e.range.endColumn),
      text: e.text,
    }))
    .sort((a, b) => b.start - a.start);
  let result = text;
  for (const e of withOffsets) {
    result = result.slice(0, e.start) + e.text + result.slice(e.end);
  }
  return result;
}

describe("modeSwitchFor", () => {
  it("is none for a wrapped {…} region", () => {
    expect(modeSwitchFor(wrapped("{\n  id: 1,\n}"))).toBe("none");
  });

  it("is toPlain for a wrapped region starting with [", () => {
    expect(modeSwitchFor(wrapped("[1, 2, 3]"))).toBe("toPlain");
  });

  it("is toPlain for a wrapped region starting with an identifier", () => {
    expect(modeSwitchFor(wrapped("makeBody()"))).toBe("toPlain");
  });

  it("is none for an empty region (holds the current mode)", () => {
    expect(modeSwitchFor(wrapped(""))).toBe("none");
  });

  it("is none for a whitespace-only region", () => {
    expect(modeSwitchFor(wrapped("  \n  "))).toBe("none");
  });

  it("is none for a wrapped region opening with a comment then {", () => {
    expect(modeSwitchFor(wrapped("// a note\n{\n  id: 1,\n}"))).toBe("none");
  });

  it("is toWrapped for a plain document starting with {", () => {
    expect(modeSwitchFor("{\n  id: 1,\n}")).toBe("toWrapped");
  });

  it("is none for a plain document starting with export default", () => {
    expect(modeSwitchFor("export default async () => ({ id: 1 })")).toBe("none");
  });

  it("is toWrapped for a plain document starting with a comment then {", () => {
    expect(modeSwitchFor("// a note\n{\n  id: 1,\n}")).toBe("toWrapped");
  });
});

describe("unwrapEdits", () => {
  it("takes the whole shim with it when nothing depends on it", () => {
    const text = wrapped("[1, 2, 3]");
    const region = findRegion(text)!;
    const result = applyEdits(text, unwrapEdits(text, region));
    expect(findRegion(result)).toBeUndefined();
    expect(result).toBe("[1, 2, 3]");
  });

  it("leaves no trailing newline behind when the document has one", () => {
    const text = `${wrapped("[1, 2, 3]")}\n`;
    const region = findRegion(text)!;
    expect(applyEdits(text, unwrapEdits(text, region))).toBe("[1, 2, 3]");
  });

  it("keeps the import block, skeleton and trailing ) when the header imports", () => {
    const text = wrapped("makeBody()", ['import { makeBody } from "#/scripts/x";']);
    const region = findRegion(text)!;
    const result = applyEdits(text, unwrapEdits(text, region));
    expect(findRegion(result)).toBeUndefined();
    expect(result).toBe(
      ['import { makeBody } from "#/scripts/x";', "", SKELETON, "makeBody()", ")"].join("\n")
    );
  });

  // The resolve-or-bail path unwraps a region that may well still lead with `{`. Dropping the
  // shim there would hand modeSwitchFor a bare object, which re-wraps on the spot and undoes the
  // bail.
  it("keeps the shim when the region leads with { , so the text cannot re-wrap", () => {
    const text = wrapped("{\n  id: 1,\n}");
    const region = findRegion(text)!;
    const result = applyEdits(text, unwrapEdits(text, region));
    expect(result).toBe([SKELETON, "{", "  id: 1,", "}", ")"].join("\n"));
    expect(modeSwitchFor(result)).toBe("none");
  });
});

describe("wrapEdits", () => {
  it("round-trips so regionText gives back the original document", () => {
    const original = "{\n  id: 1,\n}";
    const result = applyEdits(original, wrapEdits(original, SKELETON));
    expect(findRegion(result)).toBeDefined();
    expect(regionText(result)).toBe(original);
  });

  it("wraps a single-line document", () => {
    const original = "{ id: 1 }";
    const result = applyEdits(original, wrapEdits(original, SKELETON));
    expect(regionText(result)).toBe(original);
    expect(result).toBe(`${SKELETON}\n// grpcview:script start\n{ id: 1 }\n// grpcview:script end\n)`);
  });
});

describe("unwrap-then-wrap stability", () => {
  it("is stable on a bare object", () => {
    const original = wrapped("{\n  id: 1,\n}");
    const region = findRegion(original)!;
    const unwrapped = applyEdits(original, unwrapEdits(original, region));
    // Unwrapped text starts with the (now-visible) skeleton's `export default`, so modeSwitchFor
    // reads it as already-settled plain text, not something to immediately re-wrap.
    expect(modeSwitchFor(unwrapped)).toBe("none");

    const rewrapped = applyEdits(unwrapped, wrapEdits(unwrapped, SKELETON));
    expect(regionText(rewrapped)).toBe(unwrapped);
  });
});
