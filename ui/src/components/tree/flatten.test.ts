import { describe, expect, it, vi } from "vitest";
import type { TreeAdapter } from "./types";
import { flatten, resolveExpansion, MAX_RESOLVE_PASSES } from "./flatten";

// A tiny local node shape — flatten.ts must know nothing about gRPC, so proving
// that means never importing a proto type here (same convention as
// lib/format.test.ts's ItemWithPath fixtures). `state` is the adapter's
// getCollapsibleState() answer; it is a field of its own (rather than derived
// from `kids`) because a folder's default expansion is independent of whether it
// currently has children — that independence is itself part of what's tested.
interface Node {
  id: string;
  label: string;
  state: "none" | "collapsed" | "expanded";
  kids?: Node[];
}

const leaf = (id: string): Node => ({ id, label: `label:${id}`, state: "none" });

const folder = (
  id: string,
  kids: Node[],
  state: "collapsed" | "expanded" = "collapsed"
): Node => ({ id, label: `label:${id}`, state, kids });

function adapterFor(roots: Node[]): TreeAdapter<Node> {
  return {
    getId: (n) => n.id,
    getChildren: (n) => (n === undefined ? roots : n.kids ?? []),
    getCollapsibleState: (n) => n.state,
    getTreeItem: (n) => ({ label: n.label }),
    getTypeaheadLabel: (n) => n.label,
  };
}

describe("flatten: roots", () => {
  it("lists roots at depth 0 with a null parentId, in order", () => {
    const roots = [leaf("a"), folder("b", []), leaf("c")];
    const { rows, defaultExpanded } = flatten(adapterFor(roots), new Set());

    expect(rows.map((r) => r.id)).toEqual(["a", "b", "c"]);
    expect(rows.every((r) => r.depth === 0 && r.parentId === null)).toBe(true);
    // "b" is expandable purely because its OWN collapsibleState says so, not
    // because it happens to have children (it has none here).
    expect(rows.map((r) => r.expandable)).toEqual([false, true, false]);
    expect(rows.every((r) => !r.expanded)).toBe(true);
    expect(defaultExpanded).toEqual([]);
  });
});

describe("flatten: expansion-gated descent", () => {
  const tree = [
    folder("A", [leaf("A1"), leaf("A2")]), // collapsed
    folder("B", [folder("B1", [leaf("B1a")]), leaf("B2")]),
    leaf("C"),
  ];

  it("descends only into ids present in the expanded set, in depth-first visual order", () => {
    const { rows } = flatten(adapterFor(tree), new Set(["B", "B1"]));

    // A is collapsed -> no A1/A2 rows at all. B and B1 are both open, so their
    // whole (nested) subtree appears, before C — this IS the order assertion.
    expect(rows.map((r) => r.id)).toEqual(["A", "B", "B1", "B1a", "B2", "C"]);

    const byId = new Map(rows.map((r) => [r.id, r]));
    expect(byId.get("A")).toMatchObject({ depth: 0, parentId: null, expandable: true, expanded: false });
    expect(byId.get("B")).toMatchObject({ depth: 0, parentId: null, expandable: true, expanded: true });
    expect(byId.get("B1")).toMatchObject({ depth: 1, parentId: "B", expandable: true, expanded: true });
    expect(byId.get("B1a")).toMatchObject({ depth: 2, parentId: "B1", expandable: false, expanded: false });
    expect(byId.get("B2")).toMatchObject({ depth: 1, parentId: "B", expandable: false, expanded: false });
    expect(byId.get("C")).toMatchObject({ depth: 0, parentId: null, expandable: false, expanded: false });
  });

  it("a collapsed folder contributes none of its children's rows", () => {
    const { rows } = flatten(adapterFor(tree), new Set());
    expect(rows.map((r) => r.id)).toEqual(["A", "B", "C"]);
  });
});

describe("flatten: a leaf can never be descended into", () => {
  it("naming a leaf's id in `expanded` changes nothing", () => {
    const roots = [leaf("a"), leaf("b")];
    const withoutLeafExpanded = flatten(adapterFor(roots), new Set());
    const withLeafExpanded = flatten(adapterFor(roots), new Set(["a"]));
    expect(withLeafExpanded).toEqual(withoutLeafExpanded);
  });

  it("never even calls getChildren for a leaf", () => {
    const roots = [leaf("a")];
    const base = adapterFor(roots);
    const getChildren = vi.fn(base.getChildren);
    flatten({ ...base, getChildren }, new Set(["a"]));

    // Only the root-level call (getChildren(undefined)) happens; "a" is not
    // expandable, so isExpanded is false regardless of membership in the set,
    // and flatten() must never recurse into it.
    expect(getChildren).toHaveBeenCalledTimes(1);
    expect(getChildren).toHaveBeenCalledWith(undefined);
  });
});

describe("flatten: defaultExpanded seeding", () => {
  // D defaults open, and so does its child D1; D1's own child D1a is a plain
  // leaf so it can never itself land in defaultExpanded, which keeps the
  // "reports nothing once seeded" assertion below unambiguous.
  const tree = [folder("D", [folder("D1", [leaf("D1a")], "expanded")], "expanded")];

  it("reports a default-expanded root not yet in the caller's expanded set", () => {
    const { defaultExpanded, rows } = flatten(adapterFor(tree), new Set());
    expect(defaultExpanded).toEqual(["D"]);
    // D1 is invisible this pass (D was never seeded into `expanded`), so it must
    // not be reported: defaultExpanded only ever covers VISIBLE rows.
    expect(rows.map((r) => r.id)).toEqual(["D"]);
  });

  it("reports the next level once the caller seeds the previous one", () => {
    const { defaultExpanded, rows } = flatten(adapterFor(tree), new Set(["D"]));
    expect(defaultExpanded).toEqual(["D1"]);
    expect(rows.map((r) => r.id)).toEqual(["D", "D1"]);
  });

  it("reports nothing once every default-expanded ancestor is seeded", () => {
    const { defaultExpanded, rows } = flatten(adapterFor(tree), new Set(["D", "D1"]));
    expect(defaultExpanded).toEqual([]);
    expect(rows.map((r) => r.id)).toEqual(["D", "D1", "D1a"]);
  });
});

describe("flatten: indexById", () => {
  it("maps every row's id to its own array index", () => {
    const tree = [
      folder("A", [leaf("A1"), leaf("A2")], "expanded"),
      folder("B", [leaf("B1")], "expanded"),
    ];
    const { rows, indexById } = flatten(adapterFor(tree), new Set(["A", "B"]));

    expect(rows.length).toBe(5);
    rows.forEach((row, i) => expect(indexById.get(row.id)).toBe(i));
    expect(indexById.size).toBe(rows.length);
  });
});

describe("flatten: posInSet/setSize", () => {
  it("gives every root a 1-based posInSet and the same setSize == root count", () => {
    const roots = [leaf("a"), leaf("b"), leaf("c"), leaf("d")];
    const { rows } = flatten(adapterFor(roots), new Set());

    expect(rows.map((r) => r.posInSet)).toEqual([1, 2, 3, 4]);
    expect(rows.every((r) => r.setSize === 4)).toBe(true);
  });

  it("a nested folder's children get posInSet ascending 1..n, and setSize == the folder's visible child count", () => {
    const tree = [folder("F", [leaf("C1"), leaf("C2"), leaf("C3")], "expanded")];
    const { rows } = flatten(adapterFor(tree), new Set(["F"]));
    const byId = new Map(rows.map((r) => [r.id, r]));

    expect(byId.get("F")).toMatchObject({ posInSet: 1, setSize: 1 }); // F is the sole root
    expect(byId.get("C1")).toMatchObject({ posInSet: 1, setSize: 3 });
    expect(byId.get("C2")).toMatchObject({ posInSet: 2, setSize: 3 });
    expect(byId.get("C3")).toMatchObject({ posInSet: 3, setSize: 3 });
  });

  it("a collapsed folder's children contribute nothing — they are not rows, so they cannot inflate anyone's setSize", () => {
    const roots = [folder("A", [leaf("A1"), leaf("A2"), leaf("A3")]), leaf("B")]; // A collapsed (default)
    const { rows } = flatten(adapterFor(roots), new Set());

    expect(rows.map((r) => r.id)).toEqual(["A", "B"]);
    expect(rows.map((r) => r.posInSet)).toEqual([1, 2]);
    // Both A and B see setSize 2 (the two ROOTS), never 5 — A's three hidden
    // children never became rows at all, so they were never candidates to be
    // counted into anyone's set to begin with.
    expect(rows.every((r) => r.setSize === 2)).toBe(true);
  });

  it("a deeper level restarts numbering at 1 rather than continuing the parent's own posInSet", () => {
    // D is the SECOND root (posInSet 2), so an implementation that ran one
    // running counter across the whole flattened array (instead of scoping it
    // to each parent's own children) would give D1 posInSet 3, continuing on
    // from D's own 2. The correct behavior restarts D's children at 1.
    const tree = [leaf("R0"), folder("D", [leaf("D1"), leaf("D2")], "expanded")];
    const { rows } = flatten(adapterFor(tree), new Set(["D"]));
    const byId = new Map(rows.map((r) => [r.id, r]));

    expect(byId.get("R0")).toMatchObject({ posInSet: 1, setSize: 2 });
    expect(byId.get("D")).toMatchObject({ posInSet: 2, setSize: 2 });
    expect(byId.get("D1")).toMatchObject({ posInSet: 1, setSize: 2 }); // restarts, not 3
    expect(byId.get("D2")).toMatchObject({ posInSet: 2, setSize: 2 });
  });

  it("setSize counts only same-parent siblings, even when another parent's rows of the same depth sit nearby in the flat array", () => {
    // F1's two children and F2's three children are both at depth 1, and F2's
    // own row sits immediately after F1's last child in the flat array — as
    // close as two different parents' children can get without literally
    // interleaving. A setSize computed by grouping on DEPTH (or on raw array
    // adjacency) instead of on parentId would conflate the two groups (e.g.
    // reporting 5 for everyone, or carrying C1/C2's numbering into F2's
    // children); grouping on parentId keeps them fully independent.
    const tree = [
      folder("F1", [leaf("C1"), leaf("C2")], "expanded"),
      folder("F2", [leaf("D1"), leaf("D2"), leaf("D3")], "expanded"),
    ];
    const { rows } = flatten(adapterFor(tree), new Set(["F1", "F2"]));
    const byId = new Map(rows.map((r) => [r.id, r]));

    expect(rows.map((r) => r.id)).toEqual(["F1", "C1", "C2", "F2", "D1", "D2", "D3"]);
    expect(byId.get("C1")).toMatchObject({ posInSet: 1, setSize: 2 });
    expect(byId.get("C2")).toMatchObject({ posInSet: 2, setSize: 2 });
    // D1 restarts at 1 with setSize 3, wholly independent of F1's children
    // immediately preceding it in the array.
    expect(byId.get("D1")).toMatchObject({ posInSet: 1, setSize: 3 });
    expect(byId.get("D2")).toMatchObject({ posInSet: 2, setSize: 3 });
    expect(byId.get("D3")).toMatchObject({ posInSet: 3, setSize: 3 });
  });
});

describe("flatten: guards", () => {
  it("throws when getChildren returns a real Promise, naming the T8 async path", () => {
    const adapter: TreeAdapter<Node> = {
      getId: (n) => n.id,
      getChildren: () => Promise.resolve([]),
      getCollapsibleState: () => "none",
      getTreeItem: (n) => ({ label: n.label }),
      getTypeaheadLabel: (n) => n.label,
    };
    expect(() => flatten(adapter, new Set())).toThrow(/T8/);
    expect(() => flatten(adapter, new Set())).toThrow(/tree-rewrite-plan\.md/);
  });

  it("throws for any thenable, not only a real Promise instance", () => {
    // A structurally-thenable, non-Promise value: proves the guard checks
    // `typeof x.then === "function"`, not `instanceof Promise`. Reaching this
    // shape at all requires escaping TreeAdapter's declared return type, which
    // is exactly the kind of misbehaving adapter the guard exists for.
    const fakeThenable = { then: () => undefined };
    const adapter = {
      getId: (n: Node) => n.id,
      getChildren: () => fakeThenable,
      getCollapsibleState: () => "none" as const,
      getTreeItem: (n: Node) => ({ label: n.label }),
      getTypeaheadLabel: (n: Node) => n.label,
    } as unknown as TreeAdapter<Node>;

    expect(() => flatten(adapter, new Set())).toThrow(/T8/);
  });

  it("throws on sibling ids that collide, naming both labels", () => {
    const tree = [
      { id: "dup", label: "First Dup", state: "none" as const },
      { id: "dup", label: "Second Dup", state: "none" as const },
    ];
    let caught: unknown;
    try {
      flatten(adapterFor(tree), new Set());
    } catch (e) {
      caught = e;
    }
    expect(caught).toBeInstanceOf(Error);
    expect((caught as Error).message).toContain("First Dup");
    expect((caught as Error).message).toContain("Second Dup");
  });

  it("throws on ids that collide across different parents, not just siblings", () => {
    // "dup" appears once as a root and once nested inside an unrelated expanded
    // folder — proves the check is tree-wide (one shared indexById), not scoped
    // to a single getChildren() result.
    const tree = [
      { id: "dup", label: "Root Dup", state: "none" as const },
      folder("F", [{ id: "dup", label: "Nested Dup", state: "none" as const }], "expanded"),
    ];
    let caught: unknown;
    try {
      flatten(adapterFor(tree), new Set(["F"]));
    } catch (e) {
      caught = e;
    }
    expect(caught).toBeInstanceOf(Error);
    expect((caught as Error).message).toContain("Root Dup");
    expect((caught as Error).message).toContain("Nested Dup");
  });
});

describe("resolveExpansion", () => {
  it("resolves a deep default-expanded chain in a single call", () => {
    // A/B/C all default open; D is a plain leaf. flatten() alone only reveals one
    // level per pass (see "flatten: defaultExpanded seeding" above) — proving
    // resolveExpansion surfaces every level from ONE call, with no caller-driven
    // re-flatten in between, is the entire point of this function existing.
    const tree = [folder("A", [folder("B", [folder("C", [leaf("D")], "expanded")], "expanded")], "expanded")];

    const { flat, seeded } = resolveExpansion(adapterFor(tree), new Set(), new Set());

    expect(flat.rows.map((r) => r.id)).toEqual(["A", "B", "C", "D"]);
    expect(flat.defaultExpanded).toEqual([]); // fully resolved: nothing left to fold in
    expect(seeded).toEqual(["A", "B", "C"]);
  });

  it("never re-folds an id already in `seen`, even though it still matches defaultExpanded", () => {
    // A defaults open, but the caller says it was already seeded once before
    // (e.g. the user manually collapsed it after an earlier auto-open elsewhere in
    // the app's lifetime) — resolution must leave it collapsed, not force it back
    // open just because it once again looks like an unseeded default.
    const tree = [folder("A", [leaf("A1")], "expanded")];

    const { flat, seeded } = resolveExpansion(adapterFor(tree), new Set(), new Set(["A"]));

    expect(flat.rows.map((r) => r.id)).toEqual(["A"]); // A1 never appears: A stays collapsed
    expect(flat.defaultExpanded).toEqual(["A"]); // still reported by flatten()...
    expect(seeded).toEqual([]); // ...but never folded in, because it's already `seen`
  });

  it("stops at MAX_RESOLVE_PASSES rather than hanging on a pathological adapter", () => {
    // A single-child CHAIN defined lazily from the parent's own id, rather than a
    // pre-built tree — genuinely never terminates on its own (every node reports
    // "expanded", and there is always one more child to reveal). Exists only to
    // prove the loop bound holds; a well-formed adapter never approaches it (see
    // MAX_RESOLVE_PASSES's own comment for the tree-depth convergence argument).
    interface Chain {
      id: string;
    }
    const chainAdapter: TreeAdapter<Chain> = {
      getId: (n) => n.id,
      getChildren: (n) => [{ id: n ? `${n.id}.1` : "root" }],
      getCollapsibleState: () => "expanded",
      getTreeItem: (n) => ({ label: n.id }),
      getTypeaheadLabel: (n) => n.id,
    };

    const { seeded } = resolveExpansion(chainAdapter, new Set(), new Set());

    // Terminates (this assertion running at all is half the proof) at exactly the
    // bound, having seeded one new id per pass the whole way — not zero, not
    // unbounded.
    expect(seeded.length).toBe(MAX_RESOLVE_PASSES);
  });
});
