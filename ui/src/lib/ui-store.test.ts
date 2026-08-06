import { beforeEach, describe, expect, it } from "vitest";
import { useUIStore, type Draft, type InvokeState, type OpenTab } from "./ui-store";

// Keys are "<collection id>/<slug path>" (see format.ts's itemKey), so every fixture
// here is a slug key. Only a MOVE ever changes one — a rename leaves the slug alone,
// which is why moveSubtree has no rename caller left.
const OLD = "./users/get-user";
const NEW = "./archive/get-user";
const OTHER = "./users/admin/ban";

// Every fixture key lives in the "." collection, and OpenTab now carries that id
// alongside the key: a collection id may contain slashes, so it is never parsed out of
// the key.
const tab = (key: string, name: string): OpenTab => ({ key, name, collection: "." });

const seed = (): void => {
  const userTab = tab(OLD, "GetUser");
  const otherTab = tab(OTHER, "Ban");
  const draft: Draft = { body: "{}", metadata: "" };
  const invoke: InvokeState = { error: "boom" };
  useUIStore.setState({
    openTabs: [userTab, otherTab],
    activeKey: OLD,
    drafts: { [OLD]: draft, [OTHER]: draft },
    invokes: { [OLD]: invoke, [OTHER]: invoke },
    treeSelection: [OLD, OTHER],
    treeFocused: OLD,
    treeExpanded: new Set(),
  });
};

describe("the active collection", () => {
  beforeEach(() => {
    useUIStore.setState({ activeCollection: null, activeKey: null });
  });

  it("setActiveCollection records the choice", () => {
    useUIStore.getState().setActiveCollection("services/payments/requests");
    expect(useUIStore.getState().activeCollection).toBe("services/payments/requests");
  });

  it("setActiveKey switches the collection when the tab's one is supplied", () => {
    useUIStore.getState().setActiveKey("services/payments/requests/charge", "services/payments/requests");
    const s = useUIStore.getState();
    expect(s.activeKey).toBe("services/payments/requests/charge");
    expect(s.activeCollection).toBe("services/payments/requests");
  });

  it("setActiveKey leaves the collection alone when none is supplied", () => {
    useUIStore.setState({ activeCollection: "requests" });
    useUIStore.getState().setActiveKey(null);
    const s = useUIStore.getState();
    expect(s.activeKey).toBeNull();
    expect(s.activeCollection).toBe("requests");
  });
});

// A collection id is the FIRST segment of every key (itemKey), so moving the directory
// rewrites every one of them — the same prefix remap a move does, one level up.
describe("renameCollection", () => {
  const OLD_ID = "services/pay";
  const NEW_ID = "services/payments";
  const MINE = `${OLD_ID}/get-charge`;
  const MINE_DEEP = `${OLD_ID}/admin/refund`;
  const THEIRS = "web/list";

  const inCollection = (key: string, name: string, collection: string): OpenTab => ({
    key,
    name,
    collection,
  });

  beforeEach(() => {
    const draft: Draft = { body: "{}", metadata: "" };
    useUIStore.setState({
      activeCollection: OLD_ID,
      openTabs: [
        inCollection(MINE, "GetCharge", OLD_ID),
        inCollection(MINE_DEEP, "Refund", OLD_ID),
        inCollection(THEIRS, "List", "web"),
      ],
      activeKey: MINE,
      drafts: { [MINE]: draft, [THEIRS]: draft },
      invokes: { [MINE_DEEP]: { error: "boom" } },
      treeSelection: [MINE, THEIRS],
      treeFocused: MINE_DEEP,
      treeExpanded: new Set([`${OLD_ID}/admin`, "web"]),
    });
  });

  it("rewrites the collection prefix of every keyed slice", () => {
    useUIStore.getState().renameCollection(OLD_ID, NEW_ID);
    const s = useUIStore.getState();
    expect(s.activeKey).toBe(`${NEW_ID}/get-charge`);
    expect(s.drafts[MINE]).toBeUndefined();
    expect(s.drafts[`${NEW_ID}/get-charge`]).toEqual({ body: "{}", metadata: "" });
    expect(s.invokes[`${NEW_ID}/admin/refund`]).toEqual({ error: "boom" });
    expect(s.treeSelection).toEqual([`${NEW_ID}/get-charge`, THEIRS]);
    expect(s.treeFocused).toBe(`${NEW_ID}/admin/refund`);
    expect(s.treeExpanded).toEqual(new Set([`${NEW_ID}/admin`, "web"]));
  });

  it("fixes the collection each tab carries, and only for tabs in the renamed one", () => {
    useUIStore.getState().renameCollection(OLD_ID, NEW_ID);
    expect(useUIStore.getState().openTabs).toEqual([
      inCollection(`${NEW_ID}/get-charge`, "GetCharge", NEW_ID),
      inCollection(`${NEW_ID}/admin/refund`, "Refund", NEW_ID),
      inCollection(THEIRS, "List", "web"),
    ]);
  });

  it("makes the new id the active collection", () => {
    useUIStore.getState().renameCollection(OLD_ID, NEW_ID);
    expect(useUIStore.getState().activeCollection).toBe(NEW_ID);
  });

  // The only caller is UpdateCollection's onSuccess, i.e. the collection the user just
  // edited, so the renamed one becomes active even if another one was — deliberate, since
  // the id that was active may be the one that no longer exists.
  it("makes the renamed collection active even when another one was", () => {
    useUIStore.setState({ activeCollection: "web" });
    useUIStore.getState().renameCollection(OLD_ID, NEW_ID);
    expect(useUIStore.getState().activeCollection).toBe(NEW_ID);
  });

  it("never touches a collection whose id is a string prefix of the renamed one", () => {
    const sibling = `${OLD_ID}2/get-charge`;
    useUIStore.setState({
      openTabs: [inCollection(sibling, "GetCharge", `${OLD_ID}2`)],
      activeKey: sibling,
      drafts: { [sibling]: { body: "sibling", metadata: "" } },
      treeExpanded: new Set([`${OLD_ID}2`]),
    });
    useUIStore.getState().renameCollection(OLD_ID, NEW_ID);
    const s = useUIStore.getState();
    expect(s.openTabs).toEqual([inCollection(sibling, "GetCharge", `${OLD_ID}2`)]);
    expect(s.activeKey).toBe(sibling);
    expect(s.drafts[sibling]).toEqual({ body: "sibling", metadata: "" });
    expect(s.treeExpanded).toEqual(new Set([`${OLD_ID}2`]));
  });

  it("is a no-op when the id did not change — a name-only edit", () => {
    const before = useUIStore.getState();
    useUIStore.getState().renameCollection(OLD_ID, OLD_ID);
    const after = useUIStore.getState();
    expect(after.openTabs).toBe(before.openTabs);
    expect(after.drafts).toBe(before.drafts);
    expect(after.invokes).toBe(before.invokes);
    expect(after.treeSelection).toBe(before.treeSelection);
    expect(after.treeExpanded).toBe(before.treeExpanded);
    expect(after.activeCollection).toBe(OLD_ID);
  });

  it("leaves every untouched slice's reference alone when the collection had nothing open", () => {
    useUIStore.setState({
      openTabs: [inCollection(THEIRS, "List", "web")],
      activeKey: THEIRS,
      drafts: {},
      invokes: {},
      treeSelection: [THEIRS],
      treeFocused: THEIRS,
      treeExpanded: new Set(["web"]),
    });
    const before = useUIStore.getState();
    useUIStore.getState().renameCollection(OLD_ID, NEW_ID);
    const after = useUIStore.getState();
    expect(after.openTabs).toBe(before.openTabs);
    expect(after.drafts).toBe(before.drafts);
    expect(after.invokes).toBe(before.invokes);
    expect(after.treeSelection).toBe(before.treeSelection);
    expect(after.treeExpanded).toBe(before.treeExpanded);
    expect(after.activeCollection).toBe(NEW_ID);
  });
});

describe("moveSubtree: the moved item itself", () => {
  beforeEach(seed);

  it("remaps the open tab's key, keeping its display name — a move never renames", () => {
    useUIStore.getState().moveSubtree(OLD, NEW);
    expect(useUIStore.getState().openTabs).toEqual([tab(NEW, "GetUser"), tab(OTHER, "Ban")]);
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
      openTabs: [tab(CHILD, "GetUser"), tab(DEEP, "Ban")],
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
    expect(openTabs[0]).toEqual(tab(CHILD_MOVED, "GetUser"));
    expect(drafts[CHILD]).toBeUndefined();
    expect(drafts[CHILD_MOVED]).toEqual({ body: "{}", metadata: "" });
    expect(invokes[CHILD_MOVED]).toEqual({ error: "boom" });
  });

  it("moves a TWO-level descendant, rewriting only the prefix and keeping the whole tail", () => {
    useUIStore.getState().moveSubtree(FOLDER, MOVED);
    const { openTabs, drafts, activeKey, treeFocused } = useUIStore.getState();
    expect(openTabs[1]).toEqual(tab(DEEP_MOVED, "Ban"));
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
      openTabs: [tab(sibling, "GetUser")],
      activeKey: sibling,
      drafts: { [sibling]: draft },
      invokes: {},
      treeSelection: [sibling],
      treeFocused: sibling,
      treeExpanded: new Set(["./users2"]),
    });
    useUIStore.getState().moveSubtree(FOLDER, MOVED);
    const s = useUIStore.getState();
    expect(s.openTabs).toEqual([tab(sibling, "GetUser")]);
    expect(s.activeKey).toBe(sibling);
    expect(s.drafts[sibling]).toEqual(draft);
    expect(s.treeSelection).toEqual([sibling]);
    expect(s.treeFocused).toBe(sibling);
    expect(s.treeExpanded).toEqual(new Set(["./users2"]));
  });

  it("leaves an unrelated subtree's keys untouched while remapping the moved one", () => {
    const unrelated = "./orders/list";
    useUIStore.setState({
      openTabs: [tab(CHILD, "GetUser"), tab(unrelated, "List")],
      drafts: { [CHILD]: { body: "a", metadata: "" }, [unrelated]: { body: "b", metadata: "" } },
    });
    useUIStore.getState().moveSubtree(FOLDER, MOVED);
    const { openTabs, drafts } = useUIStore.getState();
    expect(openTabs).toEqual([tab(CHILD_MOVED, "GetUser"), tab(unrelated, "List")]);
    expect(drafts[unrelated]).toEqual({ body: "b", metadata: "" });
  });
});
