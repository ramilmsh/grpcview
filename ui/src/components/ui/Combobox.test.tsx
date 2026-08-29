import { describe, it } from "node:test";
import { expect } from "expect";
import { renderToStaticMarkup } from "react-dom/server";
import { Combobox, filterOptions } from "./Combobox";

// No jsdom: the filter is a plain function and is tested as one, and the component is
// rendered with react-dom/server to pin what it commits to before any interaction —
// a free-text field, and no popup. Opening, arrow keys and picking are browser-only.

const TARGETS = [
  "//proto/health:health_proto",
  "//proto/pay/v1:pay_proto",
  "//services/pay:descriptors",
];

describe("filterOptions", () => {
  it("returns everything for an empty query", () => {
    expect(filterOptions(TARGETS, "   ")).toEqual(TARGETS);
  });

  it("matches every term anywhere, in any order, ignoring case", () => {
    expect(filterOptions(TARGETS, "PAY v1")).toEqual([
      "//proto/pay/v1:pay_proto",
    ]);
    expect(filterOptions(TARGETS, "pay")).toEqual([
      "//proto/pay/v1:pay_proto",
      "//services/pay:descriptors",
    ]);
  });

  it("puts prefix matches first, keeping the input order within each group", () => {
    expect(filterOptions(TARGETS, "//proto")).toEqual([
      "//proto/health:health_proto",
      "//proto/pay/v1:pay_proto",
    ]);
    // "proto" is a prefix of nothing here, so its matches keep their given order.
    expect(filterOptions(TARGETS, "proto")).toEqual([
      "//proto/health:health_proto",
      "//proto/pay/v1:pay_proto",
    ]);
  });

  it("matches nothing when a term does not appear", () => {
    expect(filterOptions(TARGETS, "pay ledger")).toEqual([]);
  });
});

describe("Combobox", () => {
  const markup = (over: Partial<Parameters<typeof Combobox>[0]> = {}) =>
    renderToStaticMarkup(
      <Combobox value="" onChange={() => {}} options={TARGETS} {...over} />,
    );

  it("is a text field first: closed, editable, and typed into freely", () => {
    const html = markup();
    expect(html).toContain('role="combobox"');
    expect(html).toContain('aria-expanded="false"');
    expect(html).not.toContain('role="listbox"');
    // No select, no datalist — nothing that could constrain the value to the options.
    expect(html).not.toContain("<select");
    expect(html).not.toContain("<datalist");
  });

  it("keeps the value the caller gave it, whether or not it is an option", () => {
    expect(markup({ value: "//not:listed" })).toContain('value="//not:listed"');
  });

  it("offers the caret even while the options are still loading", () => {
    const html = markup({ loading: true, options: [] });
    expect(html).toContain("Show suggestions");
    expect(html).toContain('role="combobox"');
  });
});
