import { describe, expect, it } from "vitest";
import { isModule, leadsWithBrace } from "./module-sniff";

describe("leadsWithBrace", () => {
  it("is true for a bare object literal", () => {
    expect(leadsWithBrace("{ ok: true }")).toBe(true);
  });

  it("is true past leading whitespace", () => {
    expect(leadsWithBrace("\n\n  {\n  ok: true,\n}")).toBe(true);
  });

  it("is true past a leading line comment", () => {
    expect(leadsWithBrace("// a note\n{ ok: true }")).toBe(true);
  });

  it("is true past a leading block comment", () => {
    expect(leadsWithBrace("/*\n * a note\n */\n{ ok: true }")).toBe(true);
  });

  it("is false for a module with imports", () => {
    expect(leadsWithBrace('import { x } from "#/x";\nexport default async () => ({});')).toBe(
      false
    );
  });

  it("is false for a module with no imports", () => {
    expect(leadsWithBrace("export default async () => ({})")).toBe(false);
  });

  it("is false for a non-object expression", () => {
    expect(leadsWithBrace("[1, 2, 3]")).toBe(false);
    expect(leadsWithBrace("makeBody()")).toBe(false);
  });

  it("is false for empty text", () => {
    expect(leadsWithBrace("")).toBe(false);
    expect(leadsWithBrace("   \n ")).toBe(false);
  });

  it("is not fooled by a `{` inside a leading string", () => {
    expect(leadsWithBrace('const s = "{"; export default s;')).toBe(false);
  });
});

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
