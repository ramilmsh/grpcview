import { describe, expect, it } from "vitest";
import { isEditableTarget } from "./Tree";

describe("isEditableTarget", () => {
  it("is true for an <input> — today's only in-row example (the tree's own rename box)", () => {
    expect(isEditableTarget({ tagName: "INPUT" })).toBe(true);
  });

  it("is true for a <textarea>", () => {
    expect(isEditableTarget({ tagName: "TEXTAREA" })).toBe(true);
  });

  it("is true for any contentEditable element, regardless of its tag", () => {
    expect(isEditableTarget({ tagName: "DIV", isContentEditable: true })).toBe(true);
  });

  it("is false for a plain row surface — a DIV that is not contentEditable", () => {
    expect(isEditableTarget({ tagName: "DIV" })).toBe(false);
  });

  it("is false for a row's own hover-revealed buttons (rename/delete/gear/plus)", () => {
    expect(isEditableTarget({ tagName: "BUTTON" })).toBe(false);
  });

  it("is false when isContentEditable is merely absent, not explicitly true", () => {
    expect(isEditableTarget({ tagName: "SPAN", isContentEditable: undefined })).toBe(false);
  });

  it("is false for an empty target with neither field present", () => {
    expect(isEditableTarget({})).toBe(false);
  });
});
