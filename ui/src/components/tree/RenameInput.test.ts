import { describe, expect, it } from "vitest";
import { renameVerdict } from "./RenameInput";

// renameVerdict is the whole commit/cancel/refuse decision table of the tree's
// inline rename box (T4b — docs/design/tree-rewrite-plan.md), pulled out as a pure
// function for exactly the reason isEditableTarget was (Tree.tsx /
// Tree.keydown-guard.test.ts): vitest runs `environment: "node"` with no jsdom, so
// nothing here can type into a real input, but the DECISION each keystroke feeds is
// testable directly. What is NOT reachable from this suite — real focus, a real
// blur, the collision ring actually painting — is on the browser pass.

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
    // The one case where trim() decides between cancel and commit rather than
    // merely tidying the committed string.
    expect(renameVerdict(" SayHello ", "SayHello", SIBLINGS)).toBe("cancel");
  });

  it("refuses a name that collides with a visible sibling", () => {
    expect(renameVerdict("ListHellos", "SayHello", SIBLINGS)).toBe("collision");
  });

  it("refuses a collision found after trimming, not just an exact match", () => {
    expect(renameVerdict(" ListHellos ", "SayHello", SIBLINGS)).toBe("collision");
  });

  it("is case SENSITIVE — the store's own collision check is too", () => {
    // service/store rejects a rename only on an exact sibling-name match, so a
    // case-insensitive check here would refuse names the server would accept.
    expect(renameVerdict("listhellos", "SayHello", SIBLINGS)).toBe("commit");
  });

  it("cancels rather than colliding when the value equals the row's OWN name", () => {
    // The caller excludes the renamed row from the sibling list (Tree.tsx's
    // siblingLabelsFor filters on `r.id !== row.id`), so its own name can only ever
    // reach the "unchanged" branch. Pinned because getting that filter wrong would
    // make every rename box open already-invalid.
    expect(renameVerdict("SayHello", "SayHello", ["SayHello", ...SIBLINGS])).toBe("cancel");
  });

  it("commits against an empty sibling list — an only child collides with nothing", () => {
    expect(renameVerdict("Greet", "SayHello", [])).toBe("commit");
  });
});
