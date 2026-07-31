import { beforeEach, describe, expect, it } from "vitest";
import { useUIStore, type Draft, type InvokeState, type OpenTab } from "./ui-store";

// moveSubtree is the one piece of ui-store.ts that answers the "identity hazard"
// (docs/design/tree-rewrite-plan.md; format.test.ts's own note on itemKey): itemKey
// is name-derived, so a rename changes it, and every piece of state keyed by the OLD
// key has to follow the item to the NEW one or it silently detaches — an open tab
// that quietly forgets its draft/response looks like data loss, not a bug. It
// REPLACED `renameItem` at T4b, which remapped the exact key alone: folder rename
// (T4a) makes the DESCENDANT half reachable, since renaming a folder changes the key
// of everything beneath it at once.
//
// The plan calls prefix-remap correctness "the highest-consequence risk in the plan,
// since failures are silent and cost user work" and asks for unit tests specifically.
// This file is them: the exact-match contract (every keyed field, including the tree
// rewrite's own treeSelection/treeFocused/treeExpanded), the descendant contract at
// one and two levels, and the two ways a prefix remap goes wrong — sweeping up a
// sibling whose NAME is a string prefix of the renamed one, and relabelling
// descendant tabs with the ancestor's new name.

const OLD = "Users/GetUser";
const NEW = "Users/FetchUser";
const OTHER = "Users/Admin/Ban"; // an unrelated key a rename must never touch

const seed = (): void => {
  const tab: OpenTab = { key: OLD, name: "GetUser" };
  const otherTab: OpenTab = { key: OTHER, name: "Ban" };
  const draft: Draft = { body: "{}", metadata: "" };
  const invoke: InvokeState = { error: "boom" };
  useUIStore.setState({
    openTabs: [tab, otherTab],
    activeKey: OLD,
    drafts: { [OLD]: draft, [OTHER]: draft },
    invokes: { [OLD]: invoke, [OTHER]: invoke },
    treeSelection: [OLD, OTHER],
    treeFocused: OLD,
    treeExpanded: new Set(),
  });
};

describe("moveSubtree: the renamed item itself", () => {
  beforeEach(seed);

  it("remaps the open tab's key and display name", () => {
    useUIStore.getState().moveSubtree(OLD, NEW, "FetchUser");
    expect(useUIStore.getState().openTabs).toEqual([
      { key: NEW, name: "FetchUser" },
      { key: OTHER, name: "Ban" },
    ]);
  });

  it("remaps activeKey when the renamed item was the active tab", () => {
    useUIStore.getState().moveSubtree(OLD, NEW, "FetchUser");
    expect(useUIStore.getState().activeKey).toBe(NEW);
  });

  it("leaves activeKey alone when a different tab was active", () => {
    useUIStore.setState({ activeKey: OTHER });
    useUIStore.getState().moveSubtree(OLD, NEW, "FetchUser");
    expect(useUIStore.getState().activeKey).toBe(OTHER);
  });

  it("moves the draft and invoke state to the new key, leaving unrelated ones in place", () => {
    useUIStore.getState().moveSubtree(OLD, NEW, "FetchUser");
    const { drafts, invokes } = useUIStore.getState();
    expect(drafts[OLD]).toBeUndefined();
    expect(drafts[NEW]).toEqual({ body: "{}", metadata: "" });
    expect(drafts[OTHER]).toEqual({ body: "{}", metadata: "" });
    expect(invokes[OLD]).toBeUndefined();
    expect(invokes[NEW]).toEqual({ error: "boom" });
    expect(invokes[OTHER]).toEqual({ error: "boom" });
  });

  // The tree rewrite (T0) added treeSelection/treeFocused as name-derived-keyed
  // state alongside activeKey/drafts/invokes. Without rekeying them here, a renamed
  // row that was selected/focused would silently lose its selection wash / focus
  // ring the moment the rename's refetch lands: the row now exists only under NEW,
  // but the tree would still be told OLD is selected/focused.
  it("remaps a selected id inside treeSelection, leaving other selected ids alone", () => {
    useUIStore.getState().moveSubtree(OLD, NEW, "FetchUser");
    expect(useUIStore.getState().treeSelection).toEqual([NEW, OTHER]);
  });

  // Explicit multi-selection (tree-rewrite T2) regression: the per-element `.map`
  // handles ANY array size correctly — mapping is per-element, independent of how
  // many other ids happen to be sitting alongside the renamed one — so this is a
  // confirmatory test, not a new remapping mechanism. Worth pinning explicitly
  // (three ids, only the middle one renamed, order preserved) rather than resting on
  // the two-element case above alone: a bug that only showed up with 3+ selected ids
  // (e.g. an accidental `.slice`/index-based rewrite instead of a per-element `.map`)
  // would not be caught by a two-element fixture where "the other one" and "every
  // other one" cannot be told apart.
  it("remaps one id out of a LARGER multi-selection (3+ ids), preserving every other id and their order", () => {
    const third = "Users/Admin/Promote";
    useUIStore.setState({ treeSelection: [OTHER, OLD, third] });
    useUIStore.getState().moveSubtree(OLD, NEW, "FetchUser");
    expect(useUIStore.getState().treeSelection).toEqual([OTHER, NEW, third]);
  });

  it("remaps treeFocused when the renamed item was focused", () => {
    useUIStore.getState().moveSubtree(OLD, NEW, "FetchUser");
    expect(useUIStore.getState().treeFocused).toBe(NEW);
  });

  it("leaves treeFocused alone when a different row was focused", () => {
    useUIStore.setState({ treeFocused: OTHER });
    useUIStore.getState().moveSubtree(OLD, NEW, "FetchUser");
    expect(useUIStore.getState().treeFocused).toBe(OTHER);
  });

  // treeExpanded holds bare ids too (a folder's own membership when it's open).
  // Only REACHABLE since T4a: a request is never expandable, so before folder
  // rename shipped no renamable item could be a member.
  it("remaps an id inside treeExpanded, leaving other ids alone", () => {
    useUIStore.setState({ treeExpanded: new Set([OLD, OTHER]) });
    useUIStore.getState().moveSubtree(OLD, NEW, "FetchUser");
    expect(useUIStore.getState().treeExpanded).toEqual(new Set([NEW, OTHER]));
  });

  it("leaves treeExpanded's reference untouched when the renamed id isn't a member", () => {
    useUIStore.setState({ treeExpanded: new Set([OTHER]) });
    const before = useUIStore.getState().treeExpanded;
    useUIStore.getState().moveSubtree(OLD, NEW, "FetchUser");
    expect(useUIStore.getState().treeExpanded).toBe(before);
  });

  it("is a no-op when the key doesn't actually change", () => {
    const before = useUIStore.getState();
    useUIStore.getState().moveSubtree(OLD, OLD, "GetUser");
    const after = useUIStore.getState();
    // Reference-equal, not just deep-equal: set({}) must never rebuild
    // equivalent-looking-but-new containers for state nothing actually renamed.
    expect(after.openTabs).toBe(before.openTabs);
    expect(after.treeSelection).toBe(before.treeSelection);
    expect(after.drafts).toBe(before.drafts);
    expect(after.invokes).toBe(before.invokes);
    expect(after.treeExpanded).toBe(before.treeExpanded);
  });

  it("leaves every collection's reference untouched when nothing at all matched", () => {
    // The whole point of the per-collection `changed` flags: renaming something
    // nobody has open must not hand zustand a fresh array/object/Set and re-render
    // every consumer of state the rename never touched.
    const before = useUIStore.getState();
    useUIStore.getState().moveSubtree("Nowhere/Nothing", "Nowhere/Something", "Something");
    const after = useUIStore.getState();
    expect(after.openTabs).toBe(before.openTabs);
    expect(after.treeSelection).toBe(before.treeSelection);
    expect(after.drafts).toBe(before.drafts);
    expect(after.invokes).toBe(before.invokes);
    expect(after.treeExpanded).toBe(before.treeExpanded);
  });
});

// The half that only became reachable at T4a. A FOLDER rename leaves the folder's
// directory and every descendant file exactly where they are on disk (T4a's
// slug-identity model), but every client-side key underneath it is path-derived and
// therefore changes at once.
describe("moveSubtree: descendants of a renamed folder", () => {
  const FOLDER = "Users";
  const RENAMED = "People";
  const CHILD = "Users/GetUser";
  const CHILD_RENAMED = "People/GetUser";
  const DEEP = "Users/Admin/Ban";
  const DEEP_RENAMED = "People/Admin/Ban";

  beforeEach(() => {
    const draft: Draft = { body: "{}", metadata: "" };
    const invoke: InvokeState = { error: "boom" };
    useUIStore.setState({
      openTabs: [
        { key: CHILD, name: "GetUser" },
        { key: DEEP, name: "Ban" },
      ],
      activeKey: DEEP,
      drafts: { [CHILD]: draft, [DEEP]: draft },
      invokes: { [CHILD]: invoke, [DEEP]: invoke },
      treeSelection: [CHILD],
      treeFocused: DEEP,
      treeExpanded: new Set([FOLDER, "Users/Admin"]),
    });
  });

  it("moves a one-level descendant's tab, draft and invoke, keeping its OWN display name", () => {
    useUIStore.getState().moveSubtree(FOLDER, RENAMED, RENAMED);
    const { openTabs, drafts, invokes } = useUIStore.getState();
    // "GetUser", not "People": a tab shows only the item's own last path segment,
    // so renaming an ancestor folder must not relabel it. This is the assertion
    // that fails if the exact-match name rewrite is applied to descendants too.
    expect(openTabs[0]).toEqual({ key: CHILD_RENAMED, name: "GetUser" });
    expect(drafts[CHILD]).toBeUndefined();
    expect(drafts[CHILD_RENAMED]).toEqual({ body: "{}", metadata: "" });
    expect(invokes[CHILD_RENAMED]).toEqual({ error: "boom" });
  });

  it("moves a TWO-level descendant, rewriting only the prefix and keeping the whole tail", () => {
    useUIStore.getState().moveSubtree(FOLDER, RENAMED, RENAMED);
    const { openTabs, drafts, activeKey, treeFocused } = useUIStore.getState();
    expect(openTabs[1]).toEqual({ key: DEEP_RENAMED, name: "Ban" });
    expect(drafts[DEEP_RENAMED]).toEqual({ body: "{}", metadata: "" });
    // activeKey and treeFocused are single keys, not collections — they need the
    // descendant rule too, not just the exact-match one.
    expect(activeKey).toBe(DEEP_RENAMED);
    expect(treeFocused).toBe(DEEP_RENAMED);
  });

  it("moves a descendant id inside treeSelection", () => {
    useUIStore.getState().moveSubtree(FOLDER, RENAMED, RENAMED);
    expect(useUIStore.getState().treeSelection).toEqual([CHILD_RENAMED]);
  });

  it("moves BOTH the renamed folder's own treeExpanded id and its descendant folders'", () => {
    // The renamed folder was expanded and so was a folder inside it; both ids
    // change, or the tree paints the renamed folder shut and its open child
    // stays "expanded" under an id no row has.
    useUIStore.getState().moveSubtree(FOLDER, RENAMED, RENAMED);
    expect(useUIStore.getState().treeExpanded).toEqual(new Set([RENAMED, "People/Admin"]));
  });

  // THE prefix-remap trap. "/" is keyOf's join character (format.ts), so the
  // descendant test has to be `oldKey + "/"` and never bare `oldKey`: a bare
  // startsWith would rewrite "Users2/x" into "People2/x" and detach an unrelated
  // request's tab, draft and response.
  it("never touches a SIBLING whose name is a string prefix of the renamed folder's", () => {
    const sibling = "Users2/GetUser";
    const draft: Draft = { body: "sibling", metadata: "" };
    useUIStore.setState({
      openTabs: [{ key: sibling, name: "GetUser" }],
      activeKey: sibling,
      drafts: { [sibling]: draft },
      invokes: {},
      treeSelection: [sibling],
      treeFocused: sibling,
      treeExpanded: new Set(["Users2"]),
    });
    useUIStore.getState().moveSubtree(FOLDER, RENAMED, RENAMED);
    const s = useUIStore.getState();
    expect(s.openTabs).toEqual([{ key: sibling, name: "GetUser" }]);
    expect(s.activeKey).toBe(sibling);
    expect(s.drafts[sibling]).toEqual(draft);
    expect(s.treeSelection).toEqual([sibling]);
    expect(s.treeFocused).toBe(sibling);
    expect(s.treeExpanded).toEqual(new Set(["Users2"]));
  });

  it("leaves an unrelated subtree's keys untouched while remapping the renamed one", () => {
    const unrelated = "Orders/List";
    useUIStore.setState({
      openTabs: [
        { key: CHILD, name: "GetUser" },
        { key: unrelated, name: "List" },
      ],
      drafts: { [CHILD]: { body: "a", metadata: "" }, [unrelated]: { body: "b", metadata: "" } },
    });
    useUIStore.getState().moveSubtree(FOLDER, RENAMED, RENAMED);
    const { openTabs, drafts } = useUIStore.getState();
    expect(openTabs).toEqual([
      { key: CHILD_RENAMED, name: "GetUser" },
      { key: unrelated, name: "List" },
    ]);
    expect(drafts[unrelated]).toEqual({ body: "b", metadata: "" });
  });
});
