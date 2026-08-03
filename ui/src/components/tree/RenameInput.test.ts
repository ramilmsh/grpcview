import { describe, expect, it } from "vitest";
import { renameVerdict } from "./RenameInput";

const SIBLINGS = ["ListHellos", "SendHellos"];

describe("renameVerdict", () => {
  it("commits a non-blank, changed, collision-free name", () => {
    expect(renameVerdict("Greet", "SayHello", SIBLINGS)).toBe("commit");
  });

  it("trims before deciding, and commits the trimmed value's verdict", () => {
    expect(renameVerdict("  Greet  ", "SayHello", SIBLINGS)).toBe("commit");
  });

  it("cancels a blank name — a rename to nothing is not a rename", () => {
    expect(renameVerdict("", "SayHello", SIBLINGS)).toBe("cancel");
  });

  it("cancels a name that is only whitespace", () => {
    expect(renameVerdict("   ", "SayHello", SIBLINGS)).toBe("cancel");
  });

  it("cancels an UNCHANGED name — the server would take it, but there is nothing to persist", () => {
    expect(renameVerdict("SayHello", "SayHello", SIBLINGS)).toBe("cancel");
  });

  it("cancels a name that is unchanged only after trimming", () => {
    expect(renameVerdict(" SayHello ", "SayHello", SIBLINGS)).toBe("cancel");
  });

  it("refuses a name that collides with a visible sibling", () => {
    expect(renameVerdict("ListHellos", "SayHello", SIBLINGS)).toBe("collision");
  });

  it("refuses a collision found after trimming, not just an exact match", () => {
    expect(renameVerdict(" ListHellos ", "SayHello", SIBLINGS)).toBe("collision");
  });

  it("is case SENSITIVE — the store's own collision check is too", () => {
    expect(renameVerdict("listhellos", "SayHello", SIBLINGS)).toBe("commit");
  });

  it("cancels rather than colliding when the value equals the row's OWN name", () => {
    expect(renameVerdict("SayHello", "SayHello", ["SayHello", ...SIBLINGS])).toBe("cancel");
  });

  it("commits against an empty sibling list — an only child collides with nothing", () => {
    expect(renameVerdict("Greet", "SayHello", [])).toBe("commit");
  });
});
