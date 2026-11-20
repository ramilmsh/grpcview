import { describe, it } from "node:test";
import { expect } from "expect";
import { SourceOrigin, type DescriptorSource } from "@grpcview/v1/workspace_pb";
import { errorSummary, refreshAllIds, targetSourceId } from "./sources-popover";

const src = (
  id: string,
  source: DescriptorSource["source"],
): DescriptorSource =>
  ({
    id,
    source,
    origin: SourceOrigin.COLLECTION,
    commitDescriptors: false,
  }) as unknown as DescriptorSource;

const bazel = src("bazel://a:b", {
  case: "bazel",
  value: { label: "//a:b" },
} as DescriptorSource["source"]);
const refl = (addr: string) =>
  src(`reflection:${addr}`, {
    case: "reflection",
    value: { address: addr },
  } as DescriptorSource["source"]);
const upload = (name: string, path: string) =>
  src(`upload:${name}`, {
    case: "upload",
    value: { fileName: name, path },
  } as DescriptorSource["source"]);
const reference = src("reflection:gone:1", {
  case: undefined,
} as DescriptorSource["source"]);

describe("errorSummary", () => {
  it("takes the first non-blank line and flags the rest", () => {
    expect(errorSummary("\n  build failed  \nERROR: x\n")).toEqual({
      line: "build failed",
      more: true,
    });
  });

  it("has nothing more for a short one-liner", () => {
    expect(errorSummary("connection refused")).toEqual({
      line: "connection refused",
      more: false,
    });
  });

  it("offers the full text when the one line is too long to read", () => {
    expect(errorSummary("x".repeat(100)).more).toBe(true);
  });
});

describe("refreshAllIds", () => {
  it("keeps priority order and skips only pathless uploads", () => {
    expect(
      refreshAllIds([
        refl("a:1"),
        upload("picked.pb", ""),
        upload("tracked.pb", "protos/tracked.pb"),
        bazel,
        reference,
      ]),
    ).toEqual([
      "reflection:a:1",
      "upload:tracked.pb",
      "bazel://a:b",
      "reflection:gone:1",
    ]);
  });
});

describe("targetSourceId", () => {
  it("is the first reflection source, not a dangling reference", () => {
    expect(targetSourceId([bazel, reference, refl("b:2"), refl("c:3")])).toBe(
      "reflection:b:2",
    );
  });

  it("is null without a reflection source", () => {
    expect(targetSourceId([bazel, upload("u.pb", "")])).toBeNull();
  });
});
