import { describe, expect, it } from "vitest";
import { isEditableTarget } from "./Tree";

// isEditableTarget is the guard handleKeyDown uses to bail out before ever
// building an intent (Tree.tsx, right above `export function Tree`): a row's
// inline rename <input> (EditableName.tsx) is a DOM descendant of `.tree` and
// never calls stopPropagation() on its own onKeyDown, so every keystroke typed
// while renaming would otherwise bubble up and get reinterpreted by
// keyToIntent — dropping the space in a two-word rename, popping the delete
// confirmation on a stray Delete/cmd+Backspace, hijacking the caret on
// Home/End/arrows, and so on. Duck-typed over a minimal shape rather than a
// real HTMLElement/EventTarget, which is what lets this run under vitest's
// `node` environment (vitest.config.ts) with no jsdom — see that config's own
// header comment for why the test suite is deliberately DOM-free.
describe("isEditableTarget", () => {
  it("is true for an <input> — today's only in-row example (EditableName's rename box)", () => {
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
    // Guards against a loose truthy check (e.g. `!!target.isContentEditable`
    // would also accept this, but so would any other truthy junk) — the real
    // DOM property is a strict boolean, and the check is written to match
    // that (`=== true`) rather than coerce.
    expect(isEditableTarget({ tagName: "SPAN", isContentEditable: undefined })).toBe(false);
  });

  it("is false for an empty target with neither field present", () => {
    expect(isEditableTarget({})).toBe(false);
  });
});
