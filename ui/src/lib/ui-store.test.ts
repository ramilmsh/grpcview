import { beforeEach, describe, expect, it } from "vitest";
import { useUIStore, type Draft, type InvokeState, type OpenTab } from "./ui-store";

// Keys are "<collection id>/<slug path>" (see format.ts's itemKey), so every fixture
// here is a slug key. Only a MOVE ever changes one — a rename leaves the slug alone,
// which is why moveSubtree has no rename caller left.
const OLD = "./users/get-user";
const NEW = "./archive/get-user";
const OTHER = "./users/admin/ban";

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

describe("moveSubtree: the moved item itself", () => {
  beforeEach(seed);

  it("remaps the open tab's key, keeping its display name — a move never renames", () => {
    useUIStore.getState().moveSubtree(OLD, NEW);
    expect(useUIStore.getState().openTabs).toEqual([
      { key: NEW, name: "GetUser" },
      { key: OTHER, name: "Ban" },
    ]);
  });

  it("remaps activeKey when the moved item was the active tab", () => {
    useUIStore.getState().moveSubtree(OLD, NEW);
    expect(useUIStore.getState().activeKey).toBe(NEW);
  });

  it("leaves activeKey alone when a different tab was active", () => {
    useUIStore.setState({ activeKey: OTHER });
    useUIStore.getState().moveSubtree(OLD, NEW);
    expect(useUIStore.getState().activeKey).toBe(OTHER);
  });

  it("moves the draft and invoke state to the new key, leaving unrelated ones in place", () => {
    useUIStore.getState().moveSubtree(OLD, NEW);
    const { drafts, invokes } = useUIStore.getState();
    expect(drafts[OLD]).toBeUndefined();
    expect(drafts[NEW]).toEqual({ body: "{}", metadata: "" });
    expect(drafts[OTHER]).toEqual({ body: "{}", metadata: "" });
    expect(invokes[OLD]).toBeUndefined();
    expect(invokes[NEW]).toEqual({ error: "boom" });
    expect(invokes[OTHER]).toEqual({ error: "boom" });
  });

  it("remaps a re-slugged key in place — what a destination collision produces", () => {
    useUIStore.getState().moveSubtree(OLD, "./archive/get-user-2");
    const { activeKey, drafts } = useUIStore.getState();
    expect(activeKey).toBe("./archive/get-user-2");
    expect(drafts["./archive/get-user-2"]).toEqual({ body: "{}", metadata: "" });
  });

  it("remaps a selected id inside treeSelection, leaving other selected ids alone", () => {
    useUIStore.getState().moveSubtree(OLD, NEW);
    expect(useUIStore.getState().treeSelection).toEqual([NEW, OTHER]);
  });

  it("remaps one id out of a LARGER multi-selection (3+ ids), preserving every other id and their order", () => {
    const third = "./users/admin/promote";
    useUIStore.setState({ treeSelection: [OTHER, OLD, third] });
    useUIStore.getState().moveSubtree(OLD, NEW);
    expect(useUIStore.getState().treeSelection).toEqual([OTHER, NEW, third]);
  });

  it("remaps treeFocused when the moved item was focused", () => {
    useUIStore.getState().moveSubtree(OLD, NEW);
    expect(useUIStore.getState().treeFocused).toBe(NEW);
  });

  it("leaves treeFocused alone when a different row was focused", () => {
    useUIStore.setState({ treeFocused: OTHER });
    useUIStore.getState().moveSubtree(OLD, NEW);
    expect(useUIStore.getState().treeFocused).toBe(OTHER);
  });

  it("remaps an id inside treeExpanded, leaving other ids alone", () => {
    useUIStore.setState({ treeExpanded: new Set([OLD, OTHER]) });
    useUIStore.getState().moveSubtree(OLD, NEW);
    expect(useUIStore.getState().treeExpanded).toEqual(new Set([NEW, OTHER]));
  });

  it("leaves treeExpanded's reference untouched when the moved id isn't a member", () => {
    useUIStore.setState({ treeExpanded: new Set([OTHER]) });
    const before = useUIStore.getState().treeExpanded;
    useUIStore.getState().moveSubtree(OLD, NEW);
    expect(useUIStore.getState().treeExpanded).toBe(before);
  });

  it("is a no-op when the key doesn't actually change (a pure reorder in its own parent)", () => {
    const before = useUIStore.getState();
    useUIStore.getState().moveSubtree(OLD, OLD);
    const after = useUIStore.getState();
    expect(after.openTabs).toBe(before.openTabs);
    expect(after.treeSelection).toBe(before.treeSelection);
    expect(after.drafts).toBe(before.drafts);
    expect(after.invokes).toBe(before.invokes);
    expect(after.treeExpanded).toBe(before.treeExpanded);
  });

  it("leaves every collection's reference untouched when nothing at all matched", () => {
    const before = useUIStore.getState();
    useUIStore.getState().moveSubtree("./nowhere/nothing", "./elsewhere/nothing");
    const after = useUIStore.getState();
    expect(after.openTabs).toBe(before.openTabs);
    expect(after.treeSelection).toBe(before.treeSelection);
    expect(after.drafts).toBe(before.drafts);
    expect(after.invokes).toBe(before.invokes);
    expect(after.treeExpanded).toBe(before.treeExpanded);
  });
});

describe("moveSubtree: descendants of a moved folder", () => {
  const FOLDER = "./users";
  const MOVED = "./archive/users";
  const CHILD = "./users/get-user";
  const CHILD_MOVED = "./archive/users/get-user";
  const DEEP = "./users/admin/ban";
  const DEEP_MOVED = "./archive/users/admin/ban";

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
      treeExpanded: new Set([FOLDER, "./users/admin"]),
    });
  });

  it("moves a one-level descendant's tab, draft and invoke, keeping its OWN display name", () => {
    useUIStore.getState().moveSubtree(FOLDER, MOVED);
    const { openTabs, drafts, invokes } = useUIStore.getState();
    expect(openTabs[0]).toEqual({ key: CHILD_MOVED, name: "GetUser" });
    expect(drafts[CHILD]).toBeUndefined();
    expect(drafts[CHILD_MOVED]).toEqual({ body: "{}", metadata: "" });
    expect(invokes[CHILD_MOVED]).toEqual({ error: "boom" });
  });

  it("moves a TWO-level descendant, rewriting only the prefix and keeping the whole tail", () => {
    useUIStore.getState().moveSubtree(FOLDER, MOVED);
    const { openTabs, drafts, activeKey, treeFocused } = useUIStore.getState();
    expect(openTabs[1]).toEqual({ key: DEEP_MOVED, name: "Ban" });
    expect(drafts[DEEP_MOVED]).toEqual({ body: "{}", metadata: "" });
    expect(activeKey).toBe(DEEP_MOVED);
    expect(treeFocused).toBe(DEEP_MOVED);
  });

  it("moves a descendant id inside treeSelection", () => {
    useUIStore.getState().moveSubtree(FOLDER, MOVED);
    expect(useUIStore.getState().treeSelection).toEqual([CHILD_MOVED]);
  });

  it("moves BOTH the moved folder's own treeExpanded id and its descendant folders'", () => {
    useUIStore.getState().moveSubtree(FOLDER, MOVED);
    expect(useUIStore.getState().treeExpanded).toEqual(
      new Set([MOVED, "./archive/users/admin"])
    );
  });

  it("never touches a SIBLING whose slug is a string prefix of the moved folder's", () => {
    const sibling = "./users2/get-user";
    const draft: Draft = { body: "sibling", metadata: "" };
    useUIStore.setState({
      openTabs: [{ key: sibling, name: "GetUser" }],
      activeKey: sibling,
      drafts: { [sibling]: draft },
      invokes: {},
      treeSelection: [sibling],
      treeFocused: sibling,
      treeExpanded: new Set(["./users2"]),
    });
    useUIStore.getState().moveSubtree(FOLDER, MOVED);
    const s = useUIStore.getState();
    expect(s.openTabs).toEqual([{ key: sibling, name: "GetUser" }]);
    expect(s.activeKey).toBe(sibling);
    expect(s.drafts[sibling]).toEqual(draft);
    expect(s.treeSelection).toEqual([sibling]);
    expect(s.treeFocused).toBe(sibling);
    expect(s.treeExpanded).toEqual(new Set(["./users2"]));
  });

  it("leaves an unrelated subtree's keys untouched while remapping the moved one", () => {
    const unrelated = "./orders/list";
    useUIStore.setState({
      openTabs: [
        { key: CHILD, name: "GetUser" },
        { key: unrelated, name: "List" },
      ],
      drafts: { [CHILD]: { body: "a", metadata: "" }, [unrelated]: { body: "b", metadata: "" } },
    });
    useUIStore.getState().moveSubtree(FOLDER, MOVED);
    const { openTabs, drafts } = useUIStore.getState();
    expect(openTabs).toEqual([
      { key: CHILD_MOVED, name: "GetUser" },
      { key: unrelated, name: "List" },
    ]);
    expect(drafts[unrelated]).toEqual({ body: "b", metadata: "" });
  });
});
