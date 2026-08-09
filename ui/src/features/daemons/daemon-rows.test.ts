import { describe, expect, it } from "vitest";
import type { ServerEntry } from "@grpcview/v1/service_pb";
import { daemonLabel, daemonStatus, sortedDaemonRows } from "./daemon-rows";

const entry = (over: Partial<ServerEntry>): ServerEntry =>
  ({
    workspaceRoot: "/tmp/a",
    port: 10000,
    pid: 1,
    version: "dev",
    running: true,
    current: false,
    skewed: false,
    ...over,
  }) as unknown as ServerEntry;

describe("daemonStatus", () => {
  it("is running only when the entry verified as running", () => {
    expect(daemonStatus(entry({ running: true }))).toBe("running");
    expect(daemonStatus(entry({ running: false }))).toBe("stale");
  });
});

describe("daemonLabel", () => {
  it("is the basename of the workspace root", () => {
    expect(daemonLabel("/Users/r/tools/grpcview")).toBe("grpcview");
  });

  it("strips a trailing slash before taking the basename", () => {
    expect(daemonLabel("/Users/r/tools/grpcview/")).toBe("grpcview");
  });

  it("falls back to the full root for '/' or a root with no separator", () => {
    expect(daemonLabel("/")).toBe("/");
    expect(daemonLabel("grpcview")).toBe("grpcview");
  });
});

describe("sortedDaemonRows", () => {
  it("orders by workspace root and does not mutate its input", () => {
    const rows = [entry({ workspaceRoot: "/b" }), entry({ workspaceRoot: "/a" })];
    const sorted = sortedDaemonRows(rows);
    expect(sorted.map((r) => r.workspaceRoot)).toEqual(["/a", "/b"]);
    expect(rows.map((r) => r.workspaceRoot)).toEqual(["/b", "/a"]);
  });
});
