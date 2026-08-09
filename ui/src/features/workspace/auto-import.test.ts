import { describe, expect, it } from "vitest";
import {
  extractExportedNames,
  importSpecifierFor,
  insertImportEdit,
  namesAlreadyInScope,
} from "./auto-import";
import { buildWrapped } from "./script-region";

const SKELETON = "export default async (): Promise<RequestMessage> => (";

// example/scripts/ids.ts and example/scripts/trace-headers.ts, the two real fixtures the
// browser repro in the task brief is built around ("requ" → requestId, "handl" → handle).
const IDS_SOURCE = [
  "const hex = (n: number): string =>",
  '  Array.from({ length: n }, () => Math.floor(Math.random() * 16).toString(16)).join("");',
  "",
  'export const requestId = (prefix = "req"): string => `${prefix}_${hex(8)}${hex(4)}`;',
  "",
].join("\n");

const TRACE_HEADERS_SOURCE = `import { requestId } from "#/scripts/ids";

export const handle: GvMiddleware = (ctx) => ({
  ...ctx,
  metadata: { ...ctx.metadata, "x-trace-id": requestId("trace") },
});
`;

describe("extractExportedNames", () => {
  it("finds an exported const arrow function", () => {
    expect(extractExportedNames(IDS_SOURCE)).toEqual(["requestId"]);
  });

  it("finds an exported const alongside an import", () => {
    expect(extractExportedNames(TRACE_HEADERS_SOURCE)).toEqual(["handle"]);
  });

  it("finds export function and export class", () => {
    expect(
      extractExportedNames("export function stamp() {}\nexport class Thing {}")
    ).toEqual(["Thing", "stamp"]);
  });

  it("finds an export list, preferring the aliased name", () => {
    expect(extractExportedNames("const a = 1;\nexport { a as b };")).toEqual(["b"]);
  });

  it("excludes a default export — it has no name to import by", () => {
    expect(extractExportedNames("export default async () => ({});")).toEqual([]);
    expect(extractExportedNames("export default function named() {}")).toEqual([]);
    expect(extractExportedNames("const a = 1;\nexport { a as default };")).toEqual([]);
  });

  it("does not fire on a mention inside a comment or string", () => {
    expect(extractExportedNames('// export const fake = 1;\nconst s = "export const alsoFake = 1;";')).toEqual(
      []
    );
  });
});

describe("namesAlreadyInScope", () => {
  it("includes a named import's local binding, following an alias", () => {
    expect(namesAlreadyInScope('import { requestId as reqId } from "#/scripts/ids";')).toEqual(
      new Set(["reqId"])
    );
  });

  it("includes a default and a namespace import", () => {
    const names = namesAlreadyInScope(
      'import Foo from "#/scripts/x";\nimport * as ns from "#/scripts/y";'
    );
    expect(names).toEqual(new Set(["Foo", "ns"]));
  });

  it("includes a top-level declaration even when not exported", () => {
    expect(namesAlreadyInScope("const local = 1;\nfunction helper() {}")).toEqual(
      new Set(["local", "helper"])
    );
  });

  it("does not fire on a mention inside a comment or string", () => {
    expect(
      namesAlreadyInScope('// import { fake } from "x";\nconsole.log("import y from \'z\';");')
    ).toEqual(new Set());
  });
});

describe("importSpecifierFor", () => {
  it("maps a module inside the active collection to a #/-relative specifier", () => {
    expect(importSpecifierFor("example/scripts/trace-headers.ts", "example")).toBe(
      "#/scripts/trace-headers"
    );
  });

  it("maps a module outside the active collection to a @/-relative specifier", () => {
    expect(importSpecifierFor("other/scripts/x.ts", "example")).toBe("@/other/scripts/x");
  });

  it('maps every module to #/ for the "." root collection', () => {
    expect(importSpecifierFor("scripts/foo.ts", ".")).toBe("#/scripts/foo");
  });

  it("falls back to #/ (== @/) when there is no active collection", () => {
    expect(importSpecifierFor("scripts/foo.ts", null)).toBe("#/scripts/foo");
    expect(importSpecifierFor("scripts/foo.ts", undefined)).toBe("#/scripts/foo");
  });

  it("strips a .ts or .tsx extension", () => {
    expect(importSpecifierFor("example/scripts/x.tsx", "example")).toBe("#/scripts/x");
  });
});

describe("insertImportEdit", () => {
  it("inserts at the top of the file when there is no existing import", () => {
    const edit = insertImportEdit('export default { ok: true };\n', "requestId", "#/scripts/ids");
    expect(edit.offset).toBe(0);
    expect(edit.insertText).toBe('import { requestId } from "#/scripts/ids";\n');
  });

  it("inserts right after the last existing import statement", () => {
    const source = 'import { requestId } from "#/scripts/ids";\nimport { stamp } from "#/scripts/stamp";\n\nexport default 1;\n';
    const edit = insertImportEdit(source, "handle", "#/scripts/trace-headers");
    const before = source.slice(0, edit.offset);
    const after = source.slice(edit.offset);
    expect(before).toBe('import { requestId } from "#/scripts/ids";\nimport { stamp } from "#/scripts/stamp";\n');
    expect(edit.insertText).toBe('import { handle } from "#/scripts/trace-headers";\n');
    expect(after).toBe("\nexport default 1;\n");
  });

  it("is not confused by the word import inside a comment or string", () => {
    const source = '// import { fake } from "x";\nconst s = "import y from \'z\';";\nexport default 1;\n';
    const edit = insertImportEdit(source, "requestId", "#/scripts/ids");
    expect(edit.offset).toBe(0);
  });

  it("in a wrapped document with no existing imports, lands at offset 0 — never in the region", () => {
    const source = buildWrapped({ skeleton: SKELETON, region: "{\n  id: 1,\n}" });
    const edit = insertImportEdit(source, "requestId", "#/scripts/ids");
    expect(edit.offset).toBe(0);
    expect(source.slice(0, edit.offset)).toBe("");
  });

  it("in a wrapped document with two existing imports, lands after the second — never after a line inside the region", () => {
    const source = buildWrapped({
      imports: [
        'import { inherit } from "grpcview:metadata";',
        'import { invoke } from "grpcview:invoke";',
      ],
      skeleton: SKELETON,
      region: "{\n  id: 1,\n}",
    });
    const edit = insertImportEdit(source, "requestId", "#/scripts/ids");
    const before = source.slice(0, edit.offset);
    expect(before).toBe(
      [
        'import { inherit } from "grpcview:metadata";',
        'import { invoke } from "grpcview:invoke";',
        "",
      ].join("\n"),
    );
    // The insertion point precedes the skeleton line, which precedes the start marker — nowhere
    // near the region.
    expect(edit.offset).toBeLessThan(source.indexOf(SKELETON));
    expect(edit.offset).toBeLessThan(source.indexOf("// grpcview:script start"));
  });
});
