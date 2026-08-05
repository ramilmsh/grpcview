import { describe, expect, it } from "vitest";
import type { Transport } from "@connectrpc/connect";
import { createQueryOptions } from "@connectrpc/connect-query";
import { get } from "@grpcview/v1/service-WorkspaceService_connectquery";
import { keyForCollection } from "./workspace-query";

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
        createQueryOptions(get, { collection: id }, { transport }).queryKey
      );
    }
  });

  it("is per collection, so two collections cannot clobber one entry", () => {
    expect(keyForCollection(transport, "a")).not.toEqual(keyForCollection(transport, "b"));
  });
});
