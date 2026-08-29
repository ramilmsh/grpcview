import { describe, it } from "node:test";
import { expect } from "expect";
import { resolveActiveCollection } from "./active-collection";

const listing = (...ids: string[]) => ids.map((id) => ({ id }));

describe("resolveActiveCollection", () => {
  it("keeps the stored choice while it is still listed", () => {
    expect(
      resolveActiveCollection("services/payments/requests", listing("requests", "services/payments/requests"))
    ).toBe("services/payments/requests");
  });

  it("falls back to the first collection when the stored id is gone", () => {
    expect(resolveActiveCollection("deleted", listing("requests", "other"))).toBe("requests");
  });

  it("falls back to the first collection when nothing is stored", () => {
    expect(resolveActiveCollection(null, listing("requests", "other"))).toBe("requests");
  });

  it("is null when the workspace holds no collections", () => {
    expect(resolveActiveCollection(null, [])).toBeNull();
    expect(resolveActiveCollection("requests", [])).toBeNull();
  });

  it("does not treat a stored id as a path prefix of a listed one", () => {
    // "services" is not the collection "services/payments/requests"; a slash-containing
    // id must be matched whole, never split.
    expect(resolveActiveCollection("services", listing("services/payments/requests"))).toBe(
      "services/payments/requests"
    );
  });
});
