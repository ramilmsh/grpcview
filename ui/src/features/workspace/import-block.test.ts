import { describe, expect, it } from "vitest";
import {
  parseImportBlock,
  pruneEdits,
  renderImportBlock,
  type UnusedSpan,
} from "./import-block";
import { buildWrapped, START_MARKER } from "./script-region";
import type { LineEdit } from "./region-edits";

const SKELETON = "export default async (): Promise<RequestMessage> => (";

function applyLineEdit(text: string, edit: LineEdit): string {
  const lines = text.split("\n");
  const offsetOf = (line: number, col: number): number => {
    let offset = 0;
    for (let i = 0; i < line - 1; i++) offset += lines[i].length + 1;
    return offset + (col - 1);
  };
  const start = offsetOf(edit.range.startLineNumber, edit.range.startColumn);
  const end = offsetOf(edit.range.endLineNumber, edit.range.endColumn);
  return text.slice(0, start) + edit.text + text.slice(end);
}

describe("parseImportBlock", () => {
  it("captures a plain named import, dropping the skeleton and blank-line noise", () => {
    const header = ['import { a, b as c } from "mod";', "", SKELETON].join("\n");
    expect(parseImportBlock(header)).toEqual({
      imports: [{ specifier: "mod", names: ["a", "b as c"] }],
      other: [],
    });
  });

  it("returns empty for an empty header", () => {
    expect(parseImportBlock("")).toEqual({ imports: [], other: [] });
  });

  it("leaves a default import in other, untouched and unmanaged", () => {
    const header = ['import Foo from "mod";', SKELETON].join("\n");
    const result = parseImportBlock(header);
    expect(result.imports).toEqual([]);
    expect(result.other).toEqual(['import Foo from "mod";']);
  });

  it("leaves a default+named combined import in other", () => {
    const header = ['import Foo, { a, b } from "mod";', SKELETON].join("\n");
    const result = parseImportBlock(header);
    expect(result.imports).toEqual([]);
    expect(result.other).toEqual(['import Foo, { a, b } from "mod";']);
  });

  it("leaves a namespace import in other", () => {
    const header = ['import * as ns from "mod";', SKELETON].join("\n");
    expect(parseImportBlock(header).other).toEqual(['import * as ns from "mod";']);
  });

  it("leaves a side-effect import in other", () => {
    const header = ['import "mod";', SKELETON].join("\n");
    expect(parseImportBlock(header).other).toEqual(['import "mod";']);
  });

  it("leaves a whole type-only import in other", () => {
    const header = ['import type { A } from "mod";', SKELETON].join("\n");
    expect(parseImportBlock(header).other).toEqual(['import type { A } from "mod";']);
  });

  it("leaves a mixed value/type named import in other", () => {
    const header = ['import { type A, b } from "mod";', SKELETON].join("\n");
    expect(parseImportBlock(header).other).toEqual(['import { type A, b } from "mod";']);
  });

  it("merges multiple managed import lines into separate entries", () => {
    const header = [
      'import { a } from "mod1";',
      'import { b } from "mod2";',
      SKELETON,
    ].join("\n");
    expect(parseImportBlock(header).imports).toEqual([
      { specifier: "mod1", names: ["a"] },
      { specifier: "mod2", names: ["b"] },
    ]);
  });
});

describe("renderImportBlock", () => {
  it("sorts by specifier, then by name, and merges duplicate specifiers", () => {
    const rendered = renderImportBlock([
      { specifier: "z-mod", names: ["z"] },
      { specifier: "a-mod", names: ["b"] },
      { specifier: "a-mod", names: ["a"] },
    ]);
    expect(rendered).toEqual([
      'import { a, b } from "a-mod";',
      'import { z } from "z-mod";',
    ]);
  });

  it("dedupes an identical name repeated for the same specifier", () => {
    expect(
      renderImportBlock([
        { specifier: "mod", names: ["a"] },
        { specifier: "mod", names: ["a"] },
      ]),
    ).toEqual(['import { a } from "mod";']);
  });

  it("returns [] for no imports", () => {
    expect(renderImportBlock([])).toEqual([]);
  });
});

describe("pruneEdits", () => {
  it("returns undefined for a document with no region", () => {
    expect(pruneEdits("export default async () => ({ ok: true });", SKELETON, [])).toBeUndefined();
  });

  it("returns undefined when no span matches a managed import", () => {
    const text = buildWrapped({
      imports: ['import { a } from "mod";'],
      skeleton: SKELETON,
      region: "{\n  a,\n}",
    });
    const unused: UnusedSpan[] = [{ start: 0, length: 1, code: 1234 }]; // wrong code, ignored
    expect(pruneEdits(text, SKELETON, unused)).toBeUndefined();
  });

  it("drops a single unused name from a multi-name import (narrow 6133 span)", () => {
    const text = buildWrapped({
      imports: ['import { a, b } from "mod";'],
      skeleton: SKELETON,
      region: "{\n  a,\n}",
    });
    const start = text.indexOf("b }");
    const edits = pruneEdits(text, SKELETON, [{ start, length: 1, code: 6133 }]);
    expect(edits).toBeDefined();
    expect(edits!).toHaveLength(1);
    const result = applyLineEdit(text, edits![0]);
    expect(result).toBe(
      buildWrapped({
        imports: ['import { a } from "mod";'],
        skeleton: SKELETON,
        region: "{\n  a,\n}",
      }),
    );
  });

  it("drops the whole specifier when a single-binding import is wholly unused (whole-decl 6133 span)", () => {
    const decl = 'import { c } from "mod2";';
    const text = buildWrapped({
      imports: ['import { a } from "mod1";', decl],
      skeleton: SKELETON,
      region: "{\n  a,\n}",
    });
    const start = text.indexOf(decl);
    const edits = pruneEdits(text, SKELETON, [{ start, length: decl.length, code: 6133 }]);
    const result = applyLineEdit(text, edits![0]);
    expect(result).toBe(
      buildWrapped({
        imports: ['import { a } from "mod1";'],
        skeleton: SKELETON,
        region: "{\n  a,\n}",
      }),
    );
  });

  it("drops the whole specifier when a multi-binding import is wholly unused (6192 span)", () => {
    const decl = 'import { c, d } from "mod2";';
    const text = buildWrapped({
      imports: ['import { a } from "mod1";', decl],
      skeleton: SKELETON,
      region: "{\n  a,\n}",
    });
    const start = text.indexOf(decl);
    const edits = pruneEdits(text, SKELETON, [{ start, length: decl.length, code: 6192 }]);
    const result = applyLineEdit(text, edits![0]);
    expect(result).toBe(
      buildWrapped({
        imports: ['import { a } from "mod1";'],
        skeleton: SKELETON,
        region: "{\n  a,\n}",
      }),
    );
  });

  it("ignores a span inside the region — the author's code, never pruned", () => {
    const text = buildWrapped({
      imports: ['import { a } from "mod";'],
      skeleton: SKELETON,
      region: "{\n  a,\n}",
    });
    const markerStart = text.indexOf(START_MARKER);
    // A span AT the marker line itself: still "in or past" the region, not the header.
    const edits = pruneEdits(text, SKELETON, [{ start: markerStart, length: 1, code: 6133 }]);
    expect(edits).toBeUndefined();
  });

  it("never prunes an unmanaged (default) import even if flagged unused", () => {
    const text = buildWrapped({
      imports: ['import Foo from "mod";'],
      skeleton: SKELETON,
      region: "{\n  a: 1,\n}",
    });
    const decl = 'import Foo from "mod";';
    const start = text.indexOf(decl);
    const edits = pruneEdits(text, SKELETON, [{ start, length: decl.length, code: 6133 }]);
    expect(edits).toBeUndefined();
  });

  it("preserves an unmanaged import verbatim while pruning a managed one alongside it", () => {
    const other = 'import Foo from "other";';
    const decl = 'import { c } from "mod2";';
    const text = buildWrapped({
      imports: [other, decl],
      skeleton: SKELETON,
      region: "{\n  a,\n}",
    });
    const start = text.indexOf(decl);
    const edits = pruneEdits(text, SKELETON, [{ start, length: decl.length, code: 6133 }]);
    const result = applyLineEdit(text, edits![0]);
    expect(result).toBe(
      buildWrapped({
        imports: [other],
        skeleton: SKELETON,
        region: "{\n  a,\n}",
      }),
    );
  });

  it("result of applying pruneEdits matches buildWrapped for the same imports and region — no drift", () => {
    const text = buildWrapped({
      imports: ['import { a, b } from "mod1";', 'import { c } from "mod2";'],
      skeleton: SKELETON,
      region: "{\n  a,\n}",
    });
    const bStart = text.indexOf("b }");
    const cDecl = 'import { c } from "mod2";';
    const cStart = text.indexOf(cDecl);
    const unused: UnusedSpan[] = [
      { start: bStart, length: 1, code: 6133 },
      { start: cStart, length: cDecl.length, code: 6133 },
    ];
    const edits = pruneEdits(text, SKELETON, unused);
    const result = applyLineEdit(text, edits![0]);
    const expected = buildWrapped({
      imports: renderImportBlock([{ specifier: "mod1", names: ["a"] }]),
      skeleton: SKELETON,
      region: "{\n  a,\n}",
    });
    expect(result).toBe(expected);
  });
});
