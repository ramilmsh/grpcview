import { describe, it } from "node:test";
import { expect } from "expect";
import {
  BOTTOM,
  bazelHint,
  commitFor,
  draftValid,
  formatSize,
  positionOf,
  priorityOptions,
} from "./add-source-draft";

// The card itself is not rendered here — it calls a connect-query hook, which needs a
// transport. What is tested is everything it decides without one.

const empty = { address: "", label: "", file: null };

describe("draftValid", () => {
  it("needs a non-blank address for reflection", () => {
    expect(draftValid("reflection", empty)).toBe(false);
    expect(draftValid("reflection", { ...empty, address: "  " })).toBe(false);
    expect(draftValid("reflection", { ...empty, address: "localhost:1" })).toBe(
      true,
    );
  });

  it("needs a non-blank label for bazel, and ignores the other kinds' fields", () => {
    expect(draftValid("bazel", { ...empty, address: "localhost:1" })).toBe(
      false,
    );
    expect(draftValid("bazel", { ...empty, label: "//pkg:t" })).toBe(true);
  });

  it("needs a chosen file for a descriptor set", () => {
    expect(draftValid("descriptorSet", { ...empty, label: "//pkg:t" })).toBe(
      false,
    );
    expect(
      draftValid("descriptorSet", {
        ...empty,
        file: { name: "a.binpb", size: 1 },
      }),
    ).toBe(true);
  });
});

describe("commitFor", () => {
  it("defaults on for a descriptor set only", () => {
    expect(commitFor("reflection", null)).toBe(false);
    expect(commitFor("bazel", null)).toBe(false);
    expect(commitFor("descriptorSet", null)).toBe(true);
  });

  it("keeps the user's own choice across a type switch", () => {
    expect(commitFor("descriptorSet", false)).toBe(false);
    expect(commitFor("reflection", true)).toBe(true);
  });
});

describe("priorityOptions", () => {
  it("offers only the top with no rows", () => {
    expect(priorityOptions([])).toEqual([
      { value: "0", label: "Top — wins conflicts" },
    ]);
  });

  it("puts Top first, one slot per gap, then Bottom", () => {
    const opts = priorityOptions(["a", "b", "c"]);
    expect(opts.map((o) => o.value)).toEqual(["0", "1", "2", BOTTOM]);
    expect(opts[0].label).toBe("Top — wins conflicts");
    expect(opts[1].label).toBe("After 1. a");
    expect(opts[3].label).toBe("Bottom");
  });

  it("maps Bottom to an unset position and the rest to their index", () => {
    expect(positionOf(BOTTOM)).toBeUndefined();
    expect(positionOf("0")).toBe(0);
    expect(positionOf("2")).toBe(2);
  });
});

describe("formatSize", () => {
  it("picks a unit", () => {
    expect(formatSize(512)).toBe("512 B");
    expect(formatSize(2048)).toBe("2.0 KB");
    expect(formatSize(3 * 1024 * 1024)).toBe("3.0 MB");
  });
});

// The picker's empty state: a listing that fails must leave the user with a field they can
// type into and a sentence saying so, never a dead end.
describe("bazelHint", () => {
  const targets = (over: Partial<Parameters<typeof bazelHint>[0]> = {}) => ({
    labels: [],
    isPending: false,
    error: null,
    ...over,
  });

  it("says nothing while the query is still running", () => {
    expect(bazelHint(targets({ isPending: true }))).toBe("");
  });

  it("says nothing when there are targets to show", () => {
    expect(bazelHint(targets({ labels: ["//proto:proto"] }))).toBe("");
  });

  it("names the empty workspace and the two rule kinds it looked for", () => {
    expect(bazelHint(targets())).toContain("proto_library");
    expect(bazelHint(targets())).toContain("proto_descriptor_set");
  });

  it("passes a failure through in the server's words, and still points at typing", () => {
    const hint = bazelHint(
      targets({ error: new Error("the workspace /repo is not trusted") }),
    );
    expect(hint).toContain("not trusted");
    expect(hint).toContain("type the label");
  });
});
