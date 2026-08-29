import { describe, it } from "node:test";
import { expect } from "expect";
import { moduleSpecifiers, requireTypesDts } from "./require-types";

const EXPORTING = "export const requestId = () => 'x';\n";

const MODULES = [
  { path: "example/scripts/ids.ts", content: EXPORTING },
  { path: "other/scripts/x.ts", content: EXPORTING },
];

describe("moduleSpecifiers", () => {
  it("gives a module inside the active collection both spellings", () => {
    expect(moduleSpecifiers("example/scripts/ids.ts", "example")).toEqual([
      "@/example/scripts/ids",
      "#/scripts/ids",
    ]);
  });

  it("gives a module outside it only the workspace-rooted one", () => {
    expect(moduleSpecifiers("other/scripts/x.ts", "example")).toEqual(["@/other/scripts/x"]);
  });

  it("treats #/ as workspace-relative for the root collection and for none", () => {
    for (const collection of [".", null, undefined]) {
      expect(moduleSpecifiers("example/scripts/ids.ts", collection)).toEqual([
        "@/example/scripts/ids",
        "#/example/scripts/ids",
      ]);
    }
  });
});

describe("requireTypesDts", () => {
  it("maps every specifier to the module behind it", () => {
    expect(requireTypesDts(MODULES, "example")).toBe(
      `interface GvModules {
  "#/scripts/ids": typeof import("#/scripts/ids");
  "@/example/scripts/ids": typeof import("@/example/scripts/ids");
  "@/other/scripts/x": typeof import("@/other/scripts/x");
}
`
    );
  });

  // A file with no export is not a module: `typeof import()` of it does not typecheck, and an
  // empty buffer is the normal state of a script the moment it is created.
  it("skips a file that exports nothing", () => {
    expect(
      requireTypesDts([{ path: "example/scripts/empty.ts", content: "const x = 1;\n" }], "example")
    ).toBeUndefined();
  });

  it("recognizes the other export forms", () => {
    for (const content of ["export default 1;\n", "export * from './a';\n", "export{a};\n"]) {
      expect(requireTypesDts([{ path: "a.ts", content }], ".")).toBeDefined();
    }
  });
});
