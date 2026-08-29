import { describe, it } from "node:test";
import { expect } from "expect";
import { bazelHint } from "./AddSourceModal";

// The picker's empty state is the whole point of these: a listing that fails must leave the
// user with a field they can type into and a sentence saying so, never a dead end. The modal
// itself is not rendered here — it calls a connect-query hook, which needs a transport.

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
