import { describe, expect, it } from "vitest";
import { collectionModulePrefix, workspaceModulePaths, workspaceModuleUri } from "./workspace-modules";

describe("workspaceModuleUri", () => {
  it("registers a workspace-relative path under the file:///grpcview/ws prefix", () => {
    expect(workspaceModuleUri("example/scripts/uuid.ts")).toBe(
      "file:///grpcview/ws/example/scripts/uuid.ts"
    );
  });
});

describe("collectionModulePrefix", () => {
  it("maps a nested collection id to a sub-path", () => {
    expect(collectionModulePrefix("example")).toBe("file:///grpcview/ws/example");
  });

  it('maps "." (a workspace-root collection) to the workspace prefix itself, not a trailing "."', () => {
    expect(collectionModulePrefix(".")).toBe("file:///grpcview/ws");
  });

  it("falls back to the workspace prefix when there is no active collection yet", () => {
    expect(collectionModulePrefix(null)).toBe("file:///grpcview/ws");
    expect(collectionModulePrefix(undefined)).toBe("file:///grpcview/ws");
  });
});

describe("workspaceModulePaths", () => {
  it("maps @/* to the workspace root and #/* to a nested collection", () => {
    expect(workspaceModulePaths("example")).toEqual({
      "@/*": ["file:///grpcview/ws/*"],
      "#/*": ["file:///grpcview/ws/example/*"],
    });
  });

  it('maps #/* to the workspace root for a "." (root) collection, with no "./" segment', () => {
    expect(workspaceModulePaths(".")).toEqual({
      "@/*": ["file:///grpcview/ws/*"],
      "#/*": ["file:///grpcview/ws/*"],
    });
  });
});
