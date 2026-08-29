import { describe, it } from "node:test";
import { expect } from "expect";
import {
  specifierCompletions,
  specifierPrefixAt,
  workspacePathForUri,
} from "./module-specifier";
import { WS_PREFIX } from "./workspace-modules";

const MODULES = [
  { path: "example/scripts/ids.ts" },
  { path: "example/scripts/stamp.ts" },
  { path: "example/scripts/nested/deep.ts" },
  { path: "example/tree/workspace/listcollections/body.ts" },
  { path: "other/scripts/x.ts" },
];

describe("specifierPrefixAt", () => {
  it("matches the four specifier forms", () => {
    expect(specifierPrefixAt('import { a } from "#/scr')?.typed).toBe("#/scr");
    expect(specifierPrefixAt("const m = await import('@/scr")?.typed).toBe("@/scr");
    expect(specifierPrefixAt('require("#/')?.typed).toBe("#/");
    expect(specifierPrefixAt('import "@/side')?.typed).toBe("@/side");
  });

  it("does not match outside an unterminated specifier", () => {
    expect(specifierPrefixAt('import { a } from "#/scripts/ids";')).toBeUndefined();
    expect(specifierPrefixAt("const s = \"#/not-an-import")).toBeUndefined();
    expect(specifierPrefixAt("const x = 1;")).toBeUndefined();
  });

  it("points segmentOffset at the character after the last slash", () => {
    const line = 'import { a } from "#/scripts/id';
    const prefix = specifierPrefixAt(line);
    expect(line.slice(prefix!.segmentOffset)).toBe("id");
  });

  it("points segmentOffset at the start of the specifier when it holds no slash", () => {
    const line = 'import { a } from "#';
    expect(line.slice(specifierPrefixAt(line)!.segmentOffset)).toBe("#");
  });
});

describe("specifierCompletions", () => {
  it("offers both sigils for an empty specifier", () => {
    expect(specifierCompletions("", MODULES, "example", undefined).map((c) => c.insertText)).toEqual(
      ["#/", "@/"]
    );
    expect(specifierCompletions("#", MODULES, "example", undefined).map((c) => c.insertText)).toEqual(
      ["#/"]
    );
  });

  it("walks the ACTIVE collection for #/, one segment at a time", () => {
    expect(specifierCompletions("#/", MODULES, "example", undefined)).toEqual([
      { insertText: "scripts/", kind: "folder", specifier: "#/scripts/" },
      { insertText: "tree/", kind: "folder", specifier: "#/tree/" },
    ]);
    expect(specifierCompletions("#/scripts/", MODULES, "example", undefined)).toEqual([
      { insertText: "nested/", kind: "folder", specifier: "#/scripts/nested/" },
      { insertText: "ids", kind: "module", specifier: "#/scripts/ids" },
      { insertText: "stamp", kind: "module", specifier: "#/scripts/stamp" },
    ]);
  });

  it("replaces only the segment being typed", () => {
    expect(specifierCompletions("#/scripts/st", MODULES, "example", undefined)).toEqual([
      { insertText: "stamp", kind: "module", specifier: "#/scripts/stamp" },
    ]);
  });

  it("walks the WORKSPACE root for @/, including other collections", () => {
    expect(
      specifierCompletions("@/", MODULES, "example", undefined).map((c) => c.insertText)
    ).toEqual(["example/", "other/"]);
  });

  it("treats #/ as workspace-relative for the root collection and for none", () => {
    for (const collection of [".", null, undefined]) {
      expect(
        specifierCompletions("#/", MODULES, collection, undefined).map((c) => c.insertText)
      ).toEqual(["example/", "other/"]);
    }
  });

  it("never offers the file being edited", () => {
    expect(
      specifierCompletions("#/scripts/", MODULES, "example", "example/scripts/ids.ts").map(
        (c) => c.insertText
      )
    ).toEqual(["nested/", "stamp"]);
  });

  it("offers nothing for a specifier that is not path-mapped", () => {
    expect(specifierCompletions("grpcview:inv", MODULES, "example", undefined)).toEqual([]);
    expect(specifierCompletions("./ids", MODULES, "example", undefined)).toEqual([]);
  });
});

describe("workspacePathForUri", () => {
  it("maps a registered module URI back to its workspace-relative path", () => {
    expect(workspacePathForUri(`${WS_PREFIX}/example/scripts/ids.ts`)).toBe(
      "example/scripts/ids.ts"
    );
    expect(workspacePathForUri("file:///grpcview/request/body.ts")).toBeUndefined();
  });
});
