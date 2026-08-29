import { describe, it } from "node:test";
import { expect } from "expect";
import { validateScriptPath } from "./script-path";

describe("validateScriptPath", () => {
  it("accepts a script directly under scripts/", () => {
    expect(validateScriptPath("scripts/uuid.ts")).toBeNull();
  });

  it("accepts a nested script under scripts/", () => {
    expect(validateScriptPath("scripts/mw/trace-headers.ts")).toBeNull();
  });

  it("rejects an empty path", () => {
    expect(validateScriptPath("")).not.toBeNull();
  });

  it("rejects an absolute path", () => {
    expect(validateScriptPath("/scripts/uuid.ts")).not.toBeNull();
  });

  it("rejects a path not ending in .ts", () => {
    expect(validateScriptPath("scripts/uuid.js")).not.toBeNull();
    expect(validateScriptPath("scripts/uuid")).not.toBeNull();
  });

  it("rejects a .. segment anywhere in the path", () => {
    expect(validateScriptPath("scripts/../evil.ts")).not.toBeNull();
    expect(validateScriptPath("scripts/a/../../evil.ts")).not.toBeNull();
  });

  it("rejects a path whose first segment is not scripts", () => {
    expect(validateScriptPath("lib/uuid.ts")).not.toBeNull();
  });

  it("rejects the scripts/ directory with nothing after it", () => {
    expect(validateScriptPath("scripts/")).not.toBeNull();
  });

  it("states the rule rather than a generic message", () => {
    expect(validateScriptPath("")).toMatch(/scripts\//);
    expect(validateScriptPath("")).toMatch(/\.ts/);
  });
});
