import { describe, it } from "node:test";
import { expect } from "expect";
import { collectionBaseName, normalizeCollectionPath } from "./collection-path";

describe("normalizeCollectionPath", () => {
  it("passes a clean relative path through unchanged", () => {
    expect(normalizeCollectionPath("requests")).toBe("requests");
  });

  it("treats empty input as the workspace root", () => {
    expect(normalizeCollectionPath("")).toBe(".");
    expect(normalizeCollectionPath("   ")).toBe(".");
  });

  it("treats a literal dot as the workspace root", () => {
    expect(normalizeCollectionPath(".")).toBe(".");
  });

  it("trims surrounding whitespace", () => {
    expect(normalizeCollectionPath("  requests  ")).toBe("requests");
  });

  it("strips a leading ./, repeatedly", () => {
    expect(normalizeCollectionPath("./requests")).toBe("requests");
    expect(normalizeCollectionPath("././requests")).toBe("requests");
  });

  it("strips trailing slashes", () => {
    expect(normalizeCollectionPath("requests/")).toBe("requests");
    expect(normalizeCollectionPath("requests///")).toBe("requests");
  });

  it("normalizes backslashes to forward slashes", () => {
    expect(normalizeCollectionPath("nested\\requests")).toBe("nested/requests");
  });

  it("preserves nested paths", () => {
    expect(normalizeCollectionPath("apps/api/requests")).toBe(
      "apps/api/requests",
    );
  });
});

describe("collectionBaseName", () => {
  it("names the workspace root explicitly", () => {
    expect(collectionBaseName(".")).toBe("workspace root");
    expect(collectionBaseName("")).toBe("workspace root");
  });

  it("takes the last segment of a nested path", () => {
    expect(collectionBaseName("apps/api/requests")).toBe("requests");
  });

  it("takes the whole path when it has a single segment", () => {
    expect(collectionBaseName("requests")).toBe("requests");
  });

  it("ignores a trailing slash when picking the base name", () => {
    expect(collectionBaseName("apps/api/requests/")).toBe("requests");
  });
});
