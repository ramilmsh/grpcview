import { describe, it } from "node:test";
import { expect } from "expect";
import {
  candidatesFrom,
  resolveOrBail,
  unresolvedNamesIn,
  VIRTUAL_MODULES,
  type ModuleExports,
  type NameSpan,
} from "./resolve-imports";
import { buildWrapped } from "./script-region";
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

const IDS: ModuleExports = { specifier: "#/scripts/ids", names: ["requestId", "stamp"] };
const OTHER_IDS: ModuleExports = { specifier: "@/shared/ids", names: ["requestId"] };

describe("unresolvedNamesIn", () => {
  it("returns [] for a document with no region", () => {
    const text = "export default async () => ({ id: requestId() });";
    const start = text.indexOf("requestId");
    expect(unresolvedNamesIn(text, [{ start, length: 9, code: 2304 }])).toEqual([]);
  });

  it("keeps a 2304 span inside the region, deduped", () => {
    const text = buildWrapped({
      skeleton: SKELETON,
      region: "{\n  id: requestId(),\n  at: requestId(),\n}",
    });
    const first = text.indexOf("requestId");
    const second = text.indexOf("requestId", first + 1);
    const spans: NameSpan[] = [
      { start: first, length: 9, code: 2304 },
      { start: second, length: 9, code: 2304 },
    ];
    expect(unresolvedNamesIn(text, spans)).toEqual(["requestId"]);
  });

  it("ignores a span in the header and a span of another code", () => {
    const text = buildWrapped({
      imports: ['import { gone } from "#/missing";'],
      skeleton: SKELETON,
      region: "{\n  id: requestId(),\n}",
    });
    const headerSpan = text.indexOf("gone");
    const regionSpan = text.indexOf("requestId");
    expect(
      unresolvedNamesIn(text, [
        { start: headerSpan, length: 4, code: 2304 },
        { start: regionSpan, length: 9, code: 2552 },
      ])
    ).toEqual([]);
  });
});

describe("candidatesFrom", () => {
  it("maps modules to specifiers, skips the current file and appends the virtuals", () => {
    const ctx = {
      modules: [
        { path: "coll/scripts/ids.ts", content: "export const requestId = () => 1;" },
        { path: "coll/scripts/self.ts", content: "export const self = 1;" },
      ],
      collectionId: "coll",
    };
    const candidates = candidatesFrom(ctx, "coll/scripts/self.ts");
    expect(candidates.slice(0, 1)).toEqual([
      { specifier: "#/scripts/ids", names: ["requestId"] },
    ]);
    expect(candidates.slice(1)).toEqual(VIRTUAL_MODULES);
  });
});

describe("resolveOrBail", () => {
  it("is none for a document with no region", () => {
    const outcome = resolveOrBail({
      text: "export default async () => ({ id: requestId() });",
      skeleton: SKELETON,
      unresolved: ["requestId"],
      candidates: [IDS],
    });
    expect(outcome).toEqual({ kind: "none" });
  });

  it("is none when nothing is unresolved", () => {
    const text = buildWrapped({ skeleton: SKELETON, region: "{\n  id: 1,\n}" });
    expect(resolveOrBail({ text, skeleton: SKELETON, unresolved: [], candidates: [IDS] })).toEqual({
      kind: "none",
    });
  });

  it("adds the one import for a name exactly one module exports", () => {
    const text = buildWrapped({ skeleton: SKELETON, region: "{\n  id: requestId(),\n}" });
    const outcome = resolveOrBail({
      text,
      skeleton: SKELETON,
      unresolved: ["requestId"],
      candidates: [IDS],
    });
    expect(outcome.kind).toBe("addImports");
    const edits = (outcome as { edits: LineEdit[] }).edits;
    expect(edits).toHaveLength(1);
    expect(applyLineEdit(text, edits[0])).toBe(
      buildWrapped({
        imports: ['import { requestId } from "#/scripts/ids";'],
        skeleton: SKELETON,
        region: "{\n  id: requestId(),\n}",
      })
    );
  });

  it("resolves a name against a grpcview:* virtual module", () => {
    const text = buildWrapped({ skeleton: SKELETON, region: "{\n  id: params.id,\n}" });
    const outcome = resolveOrBail({
      text,
      skeleton: SKELETON,
      unresolved: ["params"],
      candidates: candidatesFrom({ modules: [], collectionId: null }, undefined),
    });
    const edits = (outcome as { edits: LineEdit[] }).edits;
    expect(applyLineEdit(text, edits[0])).toBe(
      buildWrapped({
        imports: ['import { params } from "grpcview:request";'],
        skeleton: SKELETON,
        region: "{\n  id: params.id,\n}",
      })
    );
  });

  it("bails when two modules export the same name", () => {
    const text = buildWrapped({ skeleton: SKELETON, region: "{\n  id: requestId(),\n}" });
    expect(
      resolveOrBail({
        text,
        skeleton: SKELETON,
        unresolved: ["requestId"],
        candidates: [IDS, OTHER_IDS],
      })
    ).toEqual({ kind: "bail" });
  });

  it("bails on an unknown name, adding nothing for the resolvable ones alongside it", () => {
    const text = buildWrapped({
      skeleton: SKELETON,
      region: "{\n  id: requestId(),\n  at: nowhere(),\n}",
    });
    expect(
      resolveOrBail({
        text,
        skeleton: SKELETON,
        unresolved: ["requestId", "nowhere"],
        candidates: [IDS],
      })
    ).toEqual({ kind: "bail" });
  });

  it("merges into an existing header and re-sorts by specifier then name (D9)", () => {
    const text = buildWrapped({
      imports: ['import { stamp } from "#/scripts/ids";'],
      skeleton: SKELETON,
      region: "{\n  id: invoke(),\n  at: stamp(),\n  n: requestId(),\n}",
    });
    const outcome = resolveOrBail({
      text,
      skeleton: SKELETON,
      unresolved: ["invoke", "requestId"],
      candidates: [IDS, ...VIRTUAL_MODULES],
    });
    const edits = (outcome as { edits: LineEdit[] }).edits;
    expect(applyLineEdit(text, edits[0])).toBe(
      buildWrapped({
        imports: [
          'import { requestId, stamp } from "#/scripts/ids";',
          'import { invoke } from "grpcview:invoke";',
        ],
        skeleton: SKELETON,
        region: "{\n  id: invoke(),\n  at: stamp(),\n  n: requestId(),\n}",
      })
    );
  });

  it("preserves an unmanaged header line while adding an import", () => {
    const text = buildWrapped({
      imports: ['import Foo from "other";'],
      skeleton: SKELETON,
      region: "{\n  id: requestId(),\n}",
    });
    const outcome = resolveOrBail({
      text,
      skeleton: SKELETON,
      unresolved: ["requestId"],
      candidates: [IDS],
    });
    const edits = (outcome as { edits: LineEdit[] }).edits;
    expect(applyLineEdit(text, edits[0])).toBe(
      buildWrapped({
        imports: ['import Foo from "other";', 'import { requestId } from "#/scripts/ids";'],
        skeleton: SKELETON,
        region: "{\n  id: requestId(),\n}",
      })
    );
  });

  it("does not bail on a name the header already imports, and makes no edit for it", () => {
    const text = buildWrapped({
      imports: ['import { requestId } from "#/scripts/ids";'],
      skeleton: SKELETON,
      region: "{\n  id: requestId(),\n}",
    });
    expect(
      resolveOrBail({
        text,
        skeleton: SKELETON,
        unresolved: ["requestId"],
        candidates: [IDS, OTHER_IDS],
      })
    ).toEqual({ kind: "none" });
  });

  it("treats an aliased binding in the header as already resolved", () => {
    const text = buildWrapped({
      imports: ['import { stamp as requestId } from "#/scripts/ids";'],
      skeleton: SKELETON,
      region: "{\n  id: requestId(),\n}",
    });
    expect(
      resolveOrBail({
        text,
        skeleton: SKELETON,
        unresolved: ["requestId"],
        candidates: [],
      })
    ).toEqual({ kind: "none" });
  });
});
