import { describe, it } from "node:test";
import { expect } from "expect";
import {
  fuzzyHits,
  groupMethods,
  highlightSegments,
  initialActive,
  shortServiceName,
} from "./method-dropdown";
import { moveIndex } from "./method-picker";

const svc = (pkg: string, name: string, methods: string[]) => ({
  name,
  package: pkg,
  methods: methods.map((m) => ({ name: m })),
});

const SERVICES = [
  svc("grpcview.echo.v1", "EchoService", ["Echo"]),
  svc("grpcview.v1", "WorkspaceService", [
    "AddDescriptorSource",
    "ListCollections",
    "RunScript",
  ]),
  svc("grpc.reflection.v1alpha", "ServerReflection", ["ServerReflectionInfo"]),
];

describe("fuzzyHits", () => {
  it("matches a case-insensitive subsequence", () => {
    expect(fuzzyHits("ListCollections", "lc")).toEqual([0, 4]);
    expect(fuzzyHits("ListCollections", "LISTC")).toEqual([0, 1, 2, 3, 4]);
  });
  it("is null on a miss, empty on an empty query", () => {
    expect(fuzzyHits("Echo", "x")).toBeNull();
    expect(fuzzyHits("Echo", "ohce")).toBeNull();
    expect(fuzzyHits("Echo", "")).toEqual([]);
  });
});

describe("groupMethods", () => {
  it("lists everything grouped by service on an empty query", () => {
    const { groups, rows } = groupMethods(SERVICES, "  ");
    expect(groups.map((g) => g.full)).toEqual([
      "grpcview.echo.v1.EchoService",
      "grpcview.v1.WorkspaceService",
      "grpc.reflection.v1alpha.ServerReflection",
    ]);
    expect(rows.map((r) => r.index)).toEqual([0, 1, 2, 3, 4]);
  });
  it("matches across Service/Method and drops empty groups", () => {
    const { groups, rows } = groupMethods(SERVICES, "work/run");
    expect(groups.map((g) => g.service.name)).toEqual(["WorkspaceService"]);
    expect(rows.map((r) => r.method.name)).toEqual(["RunScript"]);
    expect(rows[0].hits).toEqual([0, 1, 2]);
  });
  it("highlights only hits within the method name", () => {
    const { rows } = groupMethods(SERVICES, "echo");
    expect(rows.map((r) => r.method.name)).toEqual(["Echo"]);
    // Greedy: "Echo" is consumed by "EchoService", so the method name gets none.
    expect(rows[0].hits).toEqual([]);
  });
  it("does not match the package", () => {
    expect(groupMethods(SERVICES, "alpha").rows).toEqual([]);
  });
});

describe("highlightSegments", () => {
  it("splits into hit runs", () => {
    expect(highlightSegments("ListCollections", [0, 1, 4])).toEqual([
      { text: "Li", hit: true },
      { text: "st", hit: false },
      { text: "C", hit: true },
      { text: "ollections", hit: false },
    ]);
    expect(highlightSegments("Echo", [])).toEqual([
      { text: "Echo", hit: false },
    ]);
  });
});

describe("shortServiceName", () => {
  it("keeps names up to 32 chars", () => {
    expect(shortServiceName("grpcview.v1.WorkspaceService")).toBe(
      "grpcview.v1.WorkspaceService",
    );
  });
  it("drops leading package segments past that", () => {
    expect(shortServiceName("grpc.reflection.v1alpha.ServerReflection")).toBe(
      "…v1alpha.ServerReflection",
    );
  });
  it("leaves a long unpackaged name alone", () => {
    expect(shortServiceName("A".repeat(40))).toBe("A".repeat(40));
  });
});

describe("initialActive", () => {
  const { rows } = groupMethods(SERVICES, "");
  const current = {
    service: "grpcview.v1.WorkspaceService",
    method: "RunScript",
  };
  it("is the current method on an empty query", () => {
    expect(initialActive(rows, "", current)).toBe(3);
  });
  it("is the first match while typing, or with no current", () => {
    expect(initialActive(rows, "s", current)).toBe(0);
    expect(initialActive(rows, "", null)).toBe(0);
    expect(initialActive(rows, "", { service: "x", method: "y" })).toBe(0);
  });
  it("is null with nothing listed", () => {
    expect(initialActive([], "", current)).toBeNull();
  });
});

describe("keyboard movement over the flat rows", () => {
  it("clamps at the ends and crosses group boundaries", () => {
    const { rows } = groupMethods(SERVICES, "");
    expect(moveIndex(0, rows.length, 1)).toBe(1);
    expect(moveIndex(4, rows.length, 1)).toBe(4);
    expect(moveIndex(0, rows.length, -1)).toBe(0);
  });
});
