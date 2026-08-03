import { beforeEach, describe, expect, it } from "vitest";
import { useUIStore, type Draft, type InvokeState, type OpenTab } from "./ui-store";

const OLD = "Users/GetUser";
const NEW = "Users/FetchUser";
const OTHER = "Users/Admin/Ban";

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

  it("remaps a selected id inside treeSelection, leaving other selected ids alone", () => {
    useUIStore.getState().moveSubtree(OLD, NEW, "FetchUser");
    expect(useUIStore.getState().treeSelection).toEqual([NEW, OTHER]);
  });

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
    expect(after.openTabs).toBe(before.openTabs);
    expect(after.treeSelection).toBe(before.treeSelection);
    expect(after.drafts).toBe(before.drafts);
    expect(after.invokes).toBe(before.invokes);
    expect(after.treeExpanded).toBe(before.treeExpanded);
  });

  it("leaves every collection's reference untouched when nothing at all matched", () => {
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
    expect(activeKey).toBe(DEEP_RENAMED);
    expect(treeFocused).toBe(DEEP_RENAMED);
  });

  it("moves a descendant id inside treeSelection", () => {
    useUIStore.getState().moveSubtree(FOLDER, RENAMED, RENAMED);
    expect(useUIStore.getState().treeSelection).toEqual([CHILD_RENAMED]);
  });

  it("moves BOTH the renamed folder's own treeExpanded id and its descendant folders'", () => {
    useUIStore.getState().moveSubtree(FOLDER, RENAMED, RENAMED);
    expect(useUIStore.getState().treeExpanded).toEqual(new Set([RENAMED, "People/Admin"]));
  });

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
