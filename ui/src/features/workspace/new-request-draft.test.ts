import { describe, it } from "node:test";
import { expect } from "expect";
import {
  canCreate,
  collisionMessage,
  draftTitle,
  folderLabel,
  nameAfterPick,
  nameCollides,
  siblingNames,
} from "./new-request-draft";
import { filterMethods, filterServices, moveIndex } from "./method-picker";

// The row and page call connect-query hooks, which need a transport; what is tested is
// everything they decide without one.

const folder = (name: string, children: unknown[] = []) => ({
  item: { name, content: { case: "folder" } },
  children,
});
const request = (name: string) => ({
  item: { name, content: { case: "request" } },
});

describe("draftTitle", () => {
  it("is the name, or the placeholder while blank", () => {
    expect(draftTitle("")).toBe("New request");
    expect(draftTitle("  ")).toBe("New request");
    expect(draftTitle("Echo")).toBe("Echo");
  });
});

describe("nameAfterPick", () => {
  it("fills an empty name from the method", () => {
    expect(nameAfterPick("", "Check")).toBe("Check");
    expect(nameAfterPick(" ", "Check")).toBe("Check");
  });
  it("never overwrites a typed name", () => {
    expect(nameAfterPick("Mine", "Check")).toBe("Mine");
  });
});

describe("siblingNames + nameCollides", () => {
  const roots = [
    folder("a", [request("r"), folder("b", [request("deep")])]),
    request("top"),
  ] as Parameters<typeof siblingNames>[0];

  it("lists the names in the target folder", () => {
    expect(siblingNames(roots, [])).toEqual(["a", "top"]);
    expect(siblingNames(roots, ["a"])).toEqual(["r", "b"]);
    expect(siblingNames(roots, ["a", "b"])).toEqual(["deep"]);
  });

  it("is null for a folder that is gone, or a request on the path", () => {
    expect(siblingNames(roots, ["gone"])).toBeNull();
    expect(siblingNames(roots, ["top"])).toBeNull();
  });

  it("collides on an exact trimmed match, folders included", () => {
    const sib = siblingNames(roots, ["a"]) ?? [];
    expect(nameCollides(sib, " r ")).toBe(true);
    expect(nameCollides(sib, "b")).toBe(true);
    expect(nameCollides(sib, "R")).toBe(false);
    expect(nameCollides(sib, "")).toBe(false);
  });

  it("says where", () => {
    expect(collisionMessage(" r ", "a")).toBe(
      "A request named r already exists in a.",
    );
  });
});

describe("folderLabel", () => {
  it("is the folder's name, or the collection's at the root", () => {
    expect(folderLabel("Payments", [])).toBe("Payments");
    expect(folderLabel("Payments", ["a", "b"])).toBe("b");
  });
});

describe("canCreate", () => {
  const ok = { name: "x", method: {}, collides: false, pending: false };
  it("needs a name, a method, no collision, and nothing in flight", () => {
    expect(canCreate(ok)).toBe(true);
    expect(canCreate({ ...ok, name: "  " })).toBe(false);
    expect(canCreate({ ...ok, method: null })).toBe(false);
    expect(canCreate({ ...ok, collides: true })).toBe(false);
    expect(canCreate({ ...ok, pending: true })).toBe(false);
  });
});

const services = [
  {
    name: "EchoService",
    package: "echo.v1",
    methods: [{ name: "Echo" }, { name: "EchoStream" }],
  },
  { name: "Health", package: "", methods: [{ name: "Check" }] },
];

describe("search filtering", () => {
  const names = (q: string) => filterServices(services, q).map((s) => s.name);

  it("keeps every service for a blank query", () => {
    expect(names(" ")).toEqual(["EchoService", "Health"]);
  });

  it("matches service name, package and method name, case-insensitively", () => {
    expect(names("health")).toEqual(["Health"]);
    expect(names("ECHO.V1")).toEqual(["EchoService"]);
    expect(names("check")).toEqual(["Health"]);
    expect(names("nope")).toEqual([]);
  });

  it("shows all methods of a service that matched itself, else just the hits", () => {
    expect(filterMethods(services[0], "echo.v1").map((m) => m.name)).toEqual([
      "Echo",
      "EchoStream",
    ]);
    expect(filterMethods(services[0], "stream").map((m) => m.name)).toEqual([
      "EchoStream",
    ]);
  });
});

describe("moveIndex", () => {
  it("starts nowhere, then Down lands first and Up lands last", () => {
    expect(moveIndex(null, 3, 1)).toBe(0);
    expect(moveIndex(null, 3, -1)).toBe(2);
  });
  it("clamps, and has nothing to move over when empty", () => {
    expect(moveIndex(2, 3, 1)).toBe(2);
    expect(moveIndex(0, 3, -1)).toBe(0);
    expect(moveIndex(null, 0, 1)).toBeNull();
  });
});
