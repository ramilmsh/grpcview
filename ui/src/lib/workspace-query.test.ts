import { describe, it } from "node:test";
import { expect } from "expect";
import type { Transport } from "@connectrpc/connect";
import { createQueryOptions } from "@connectrpc/connect-query";
import {
  get,
  listWorkspaceModules,
} from "@grpcview/v1/service-WorkspaceService_connectquery";
import { create } from "@bufbuild/protobuf";
import { GetDefinitionsResponseSchema } from "@grpcview/v1/service_pb";
import {
  keyForCollection,
  nextDefinitions,
  workspaceModulesKey,
} from "./workspace-query";

// The one claim the collection tier rests on: useCollectionItems' per-collection Get shares a
// cache entry with useWorkspace's, so the active collection is fetched once and a write RPC's
// cache seed (which writes keyForCollection) reaches the tier's queries too.
//
// useQuery(get, input) IS useQuery(createQueryOptions(get, input, {transport})) upstream, so
// comparing keyForCollection against createQueryOptions' key compares it against the exact key
// useWorkspace registers. The transport is keyed by object identity (a WeakMap upstream), so
// both sides must be handed the same one — which in the app is useTransport()'s.
const transport = {} as Transport;

describe("keyForCollection", () => {
  it("is the key connect-query itself derives for Get with that collection", () => {
    for (const id of [".", "services/payments/requests"]) {
      expect(keyForCollection(transport, id)).toEqual(
        createQueryOptions(get, { collection: id }, { transport }).queryKey,
      );
    }
  });

  it("is per collection, so two collections cannot clobber one entry", () => {
    expect(keyForCollection(transport, "a")).not.toEqual(
      keyForCollection(transport, "b"),
    );
  });
});

// Same claim, for the listing Monaco's extra libs are built from: a write invalidates this key,
// useWorkspaceModules registers useQuery(listWorkspaceModules, {}), and if the two ever stopped
// agreeing the invalidation would silently match nothing — leaving the editor typechecking
// against the modules as they were when the page loaded.
describe("workspaceModulesKey", () => {
  it("is the key connect-query itself derives for ListWorkspaceModules", () => {
    expect(workspaceModulesKey(transport)).toEqual(
      createQueryOptions(listWorkspaceModules, {}, { transport }).queryKey,
    );
  });
});

// Editor/gv-types effects re-register Monaco libs on `files` identity, so it must change only
// with the digest — and never carry a failed or emptied set.
describe("nextDefinitions", () => {
  const data = (digest: string) =>
    create(GetDefinitionsResponseSchema, {
      digest,
      files: [{ path: "a_pb.ts", content: digest }],
    });
  const empty = { collection: "c", defs: { digest: "", files: new Map() } };
  const held = {
    collection: "c",
    defs: nextDefinitions(empty, "c", "d1", {
      isError: false,
      data: data("d1"),
    }),
  };

  it("builds files from the response", () => {
    expect(held.defs.digest).toBe("d1");
    expect(held.defs.files.get("a_pb.ts")).toBe("d1");
  });

  it("keeps identity for an unchanged digest", () => {
    expect(
      nextDefinitions(held, "c", "d1", { isError: false, data: data("d1") }),
    ).toBe(held.defs);
  });

  it("holds the last set while a new digest loads, but not across collections", () => {
    expect(nextDefinitions(held, "c", "d2", { isError: false })).toBe(
      held.defs,
    );
    expect(
      nextDefinitions(held, "other", "d2", { isError: false }).files.size,
    ).toBe(0);
  });

  it("drops everything on an empty digest or a failed fetch", () => {
    expect(
      nextDefinitions(held, "c", "", { isError: false, data: data("d1") }).files
        .size,
    ).toBe(0);
    expect(
      nextDefinitions(held, "c", "d1", { isError: true, data: data("d1") })
        .files.size,
    ).toBe(0);
  });
});
