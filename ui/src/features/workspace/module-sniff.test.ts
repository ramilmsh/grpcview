import { describe, expect, it } from "vitest";
import { isModule } from "./module-sniff";

describe("isModule", () => {
  it("is true for a plain default export", () => {
    expect(isModule("export default async () => ({})")).toBe(true);
  });

  it("is true for a module with a leading import", () => {
    expect(
      isModule('import { x } from "#/scripts/x";\nexport default async () => x();')
    ).toBe(true);
  });

  it("is false for a bare object literal", () => {
    expect(isModule("{ ok: true }")).toBe(false);
  });

  it("is false when the only mention is inside a // comment", () => {
    expect(isModule("// there is no export default here\n{ ok: true }")).toBe(false);
  });

  it("is false when the only mention is inside a /* */ comment", () => {
    expect(isModule("/* export default lives elsewhere */\n{ ok: true }")).toBe(false);
  });

  it("is false when the only mention is inside a multi-line block comment", () => {
    expect(isModule("/*\n * export default\n */\n{ ok: true }")).toBe(false);
  });

  it("is true when real code follows a // comment on an earlier line", () => {
    expect(isModule("// export default\nexport default 1")).toBe(true);
  });

  it("is true when real code follows a /* */ comment on the same line", () => {
    expect(isModule("/* note */ export default 1")).toBe(true);
  });

  it("is true when a `/*` inside a string opens a fake unterminated block comment", () => {
    // Without literal-masking, this `/*` never finds a closing `*/` and blanks everything
    // to EOF — including the real `export default` — which is exactly the bug this test guards.
    expect(isModule('const s = "a/*b";\nexport default { s };')).toBe(true);
  });

  it("is true when a `//` inside a string opens a fake line comment", () => {
    expect(isModule('const s = "//"; export default { s };')).toBe(true);
  });

  it("is false when the only mention is inside a string literal", () => {
    expect(isModule('const s = "export default 1";\n({ s })')).toBe(false);
  });
});
