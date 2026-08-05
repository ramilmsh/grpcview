import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { SourceOrigin, type DescriptorSource } from "@grpcview/v1/workspace_pb";
import {
  COMMITTED_LABEL,
  LOCAL_LABEL,
  NO_DEFINITION_LABEL,
  REDROP_TO_REFRESH,
  refreshTitle,
  removeConsequence,
  SourceRow,
  sourceKind,
  sourceLabel,
  type SourceRowCallbacks,
} from "./source-row";

// No jsdom, so a row is rendered with react-dom/server and asserted on as markup. What that
// proves: which kind/shared/commit state a row claims, which controls it offers, and what the
// remove dialog promises. Click behavior and the native tooltips are browser-only.

const cb: SourceRowCallbacks = {
  onMove: () => {},
  onRefresh: () => {},
  onRemove: () => {},
  onSetCommit: () => {},
};

const resolved = (fileCount: number, services: string[]) => ({
  fileCount,
  serviceNames: services,
  wonServiceNames: services,
  error: "",
});

const reflection = (address: string, over: Partial<DescriptorSource> = {}): DescriptorSource =>
  ({
    id: `reflection:${address}`,
    source: { case: "reflection", value: { address } },
    resolved: resolved(2, ["fixture.Greeter"]),
    origin: SourceOrigin.COLLECTION,
    commitDescriptors: false,
    ...over,
  }) as unknown as DescriptorSource;

// `path` is the refresh recipe, empty for anything that came from a browser file picker.
const upload = (
  fileName: string,
  path = "",
  over: Partial<DescriptorSource> = {}
): DescriptorSource =>
  ({
    id: `upload:${fileName}`,
    source: { case: "upload", value: { fileName, path } },
    resolved: resolved(3, ["fixture.Greeter"]),
    origin: SourceOrigin.COLLECTION,
    commitDescriptors: false,
    ...over,
  }) as unknown as DescriptorSource;

const bazel = (label: string, over: Partial<DescriptorSource> = {}): DescriptorSource =>
  ({
    id: `bazel:${label}`,
    source: { case: "bazel", value: { label } },
    resolved: resolved(4, ["fixture.Greeter"]),
    origin: SourceOrigin.COLLECTION,
    commitDescriptors: false,
    ...over,
  }) as unknown as DescriptorSource;

// A WORKSPACE-origin entry with no oneof arm: the manifest no longer defines the id.
const danglingReference = (id: string): DescriptorSource =>
  ({
    id,
    source: { case: undefined },
    resolved: {
      fileCount: 0,
      serviceNames: [],
      wonServiceNames: [],
      error: `no source named ${id} in grpcview.work.json`,
    },
    origin: SourceOrigin.WORKSPACE,
    commitDescriptors: false,
  }) as unknown as DescriptorSource;

const row = (s: DescriptorSource, index = 0, count = 1, busy = false): string =>
  renderToStaticMarkup(
    <SourceRow source={s} index={index} count={count} busy={busy} cb={cb} />
  );

describe("sourceKind", () => {
  it("reads the oneof, and calls a reference with no arm neither kind", () => {
    expect(sourceKind(reflection("localhost:50051"))).toBe("reflection");
    expect(sourceKind(upload("api.binpb"))).toBe("descriptorSet");
    expect(sourceKind(bazel("//proto/echo/v1:echov1_proto"))).toBe("bazel");
    expect(sourceKind(danglingReference("reflection:gone:1"))).toBe("reference");
  });

  // The regression this pins: with no bazel arm, sourceKind fell through to "reference", so a
  // perfectly good bazel source rendered as a dangling reference the manifest does not define.
  it("names a bazel source by its label, never by its id", () => {
    expect(sourceLabel(bazel("//proto/echo/v1:echov1_proto"))).toBe(
      "//proto/echo/v1:echov1_proto"
    );
  });
});

describe("a row's kind and origin", () => {
  it("labels a collection-origin reflection source by host, with no shared badge", () => {
    const markup = row(reflection("localhost:50051"));
    expect(markup).toContain("localhost:50051");
    expect(markup).toContain(">reflection<");
    expect(markup).not.toContain("shared");
    expect(markup).toContain("2 files, 1 service");
  });

  it("badges a WORKSPACE-origin source as shared, keeping its kind", () => {
    const markup = row(reflection("127.0.0.1:50121", { origin: SourceOrigin.WORKSPACE }));
    expect(markup).toContain("shared");
    expect(markup).toContain("grpcview.work.json"); // the badge's tooltip says where it lives
    expect(markup).toContain(">reflection<");
    // Remove drops this collection's reference, and says so before the dialog opens.
    expect(markup).toContain("Remove this collection&#x27;s reference");
  });

  it("claims no kind for a reference the manifest does not define, and shows the server's error", () => {
    const markup = row(danglingReference("reflection:gone:1"));
    expect(markup).toContain("reflection:gone:1"); // the id is all there is to name it
    expect(markup).not.toContain(">descriptor set<");
    expect(markup).not.toContain(">reflection<");
    expect(markup).toContain("shared");
    expect(markup).toContain(NO_DEFINITION_LABEL);
    expect(markup).toContain("no source named reflection:gone:1 in grpcview.work.json");
    // Visible to be removable: the remove control is still there.
    expect(markup).toContain("aria-label=\"Remove reflection:gone:1\"");
  });

  // Dropping a definition does not drop what the collection already resolved: a committed
  // sidecar (or a cached blob) keeps answering, so the server reports NO error and the counts
  // stay real. The row still has to say the config is gone.
  it("says a reference has no definition even when stored descriptors still resolve", () => {
    const stale = danglingReference("reflection:gone:1");
    const markup = row({
      ...stale,
      resolved: resolved(3, ["fixture.Greeter"]),
    } as unknown as DescriptorSource);
    expect(markup).toContain("3 files, 1 service");
    expect(markup).toContain(NO_DEFINITION_LABEL);
    expect(markup).toContain("grpcview.work.json does not define reflection:gone:1");
    expect(markup).not.toContain(">reflection<");
  });
});

describe("a bazel row", () => {
  it("shows the label as the thing it points at, and claims the bazel kind", () => {
    const markup = row(bazel("//proto/echo/v1:echov1_proto"));
    expect(markup).toContain("//proto/echo/v1:echov1_proto");
    expect(markup).toContain(">bazel<");
    expect(markup).toContain("4 files, 1 service");
    // Not a reference: the row must not claim the manifest fails to define it, and must not
    // borrow another kind's chip.
    expect(markup).not.toContain(NO_DEFINITION_LABEL);
    expect(markup).not.toContain(">descriptor set<");
    expect(markup).not.toContain(">reflection<");
  });

  it("says on the chip that resolving it builds, and that trust gates the build", () => {
    const markup = row(bazel("//proto/echo/v1:echov1_proto"));
    expect(markup).toContain("bazel build");
    expect(markup).toContain("until the workspace is trusted");
  });

  it("carries a shared badge when the workspace manifest defines it", () => {
    const markup = row(
      bazel("//proto/echo/v1:echov1_proto", { origin: SourceOrigin.WORKSPACE })
    );
    expect(markup).toContain("shared");
    expect(markup).toContain(">bazel<");
  });
});

describe("the refresh affordance", () => {
  it("offers to re-read an upload that recorded where its bytes came from", () => {
    expect(refreshTitle(upload("api.binpb", "proto/api.binpb"))).toBe(
      "Re-read this descriptor set from proto/api.binpb"
    );
    // The recipe is short, so the row's own secondary line shows it.
    expect(row(upload("api.binpb", "proto/api.binpb"))).toContain("proto/api.binpb");
  });

  // A browser upload has no path, and the honest answer is not "this failed" but "the refresh
  // is a different gesture" — so the control SAYS re-adding the file is the refresh, and stays
  // enabled rather than being disabled with no explanation.
  it("tells a pathless upload that re-adding the same file IS its refresh", () => {
    expect(refreshTitle(upload("api.binpb"))).toBe(REDROP_TO_REFRESH);
    expect(REDROP_TO_REFRESH).toContain("Add the same file again to refresh it");
    // Middle of three, so neither priority arrow is disabled for being at an end.
    const markup = row(upload("api.binpb"), 1, 3);
    expect(markup).toContain("there is no path to re-read");
    // Enabled: with nothing pending, the row disables nothing — a pathless upload keeps its
    // refresh control precisely so the tooltip above is reachable.
    expect(markup).not.toContain("disabled=\"\"");
  });

  it("promises a build for a bazel label, and a re-reflect for an address", () => {
    expect(refreshTitle(bazel("//p:t"))).toContain("Run bazel build for this label");
    expect(refreshTitle(reflection("localhost:50051"))).toBe("Re-reflect this target");
    expect(refreshTitle(danglingReference("reflection:gone:1"))).toBe(
      "Re-resolve this reference"
    );
  });
});

describe("the commit toggle", () => {
  it("says which of the two places this source's descriptors live in", () => {
    expect(row(reflection("localhost:50051"))).toContain(LOCAL_LABEL);
    expect(row(reflection("localhost:50051", { commitDescriptors: true }))).toContain(
      COMMITTED_LABEL
    );
    // One control, not two: the row never claims both states at once.
    expect(row(reflection("localhost:50051", { commitDescriptors: true }))).not.toContain(
      LOCAL_LABEL
    );
  });

  it("explains what clicking does, in both directions", () => {
    expect(row(reflection("localhost:50051"))).toContain("Click to commit them");
    expect(row(reflection("localhost:50051", { commitDescriptors: true }))).toContain(
      "Click to cache them in local state instead"
    );
  });

  it("warns on an UNCOMMITTED upload, whose only copy is then local state", () => {
    const markup = row(upload("api.binpb"));
    expect(markup).toContain("var(--warn)");
    expect(markup).toContain("a clone of this repo has no schema for it at all");
    // Committed, the warning is gone and nothing about an upload is special.
    const committed = row(upload("api.binpb", "", { commitDescriptors: true }));
    expect(committed).not.toContain("var(--warn)");
    expect(committed).not.toContain("has no schema for it at all");
    // A reflection source is never warned about: it can be re-fetched from its address.
    expect(row(reflection("localhost:50051"))).not.toContain("var(--warn)");
  });

  it("is disabled with every other control while a mutation is in flight", () => {
    const markup = row(reflection("localhost:50051"), 0, 3, true);
    // Five controls: commit, raise, lower, refresh, remove.
    expect(markup.split("disabled=\"\"")).toHaveLength(6);
  });
});

describe("removeConsequence", () => {
  it("promises a collection's re-derivation, never a workspace's", () => {
    const text = removeConsequence(reflection("localhost:50051"));
    expect(text).toBe("This collection's definitions are re-derived from the sources that remain.");
  });

  it("promises the shared definition survives when the row is only a reference", () => {
    const text = removeConsequence(reflection("x:1", { origin: SourceOrigin.WORKSPACE }));
    expect(text).toContain("stays in grpcview.work.json");
    expect(text).toContain("only this collection's reference goes");
    expect(text).not.toContain("workspace's definitions");
  });

  it("promises nothing survives for a reference nothing defines", () => {
    const text = removeConsequence(danglingReference("reflection:gone:1"));
    expect(text).toContain("does not define it");
    expect(text).not.toContain("stays in grpcview.work.json");
  });
});
