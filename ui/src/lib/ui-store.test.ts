import { beforeEach, describe, expect, it } from "vitest";
import { useUIStore, type Draft, type InvokeState, type OpenTab } from "./ui-store";

// renameItem is the one piece of ui-store.ts that answers the "identity hazard"
// (docs/design/tree-rewrite-plan.md; format.test.ts's own note on itemKey): itemKey
// is name-derived, so a rename changes it, and every piece of state keyed by the OLD
// key has to follow the item to the NEW one or it silently detaches — an open tab
// that quietly forgets its draft/response looks like data loss, not a bug.
// treeSelection/treeFocused are the two fields the tree rewrite (T0) added to this
// same rekeying contract; they're tested here alongside the pre-existing
// openTabs/activeKey/drafts/invokes fields renameItem already remapped, so the whole
// contract lives in one place rather than half-covered.

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
  });
};

describe("renameItem", () => {
  beforeEach(seed);

  it("remaps the open tab's key and display name", () => {
    useUIStore.getState().renameItem(OLD, NEW, "FetchUser");
    expect(useUIStore.getState().openTabs).toEqual([
      { key: NEW, name: "FetchUser" },
      { key: OTHER, name: "Ban" },
    ]);
  });

  it("remaps activeKey when the renamed item was the active tab", () => {
    useUIStore.getState().renameItem(OLD, NEW, "FetchUser");
    expect(useUIStore.getState().activeKey).toBe(NEW);
  });

  it("leaves activeKey alone when a different tab was active", () => {
    useUIStore.setState({ activeKey: OTHER });
    useUIStore.getState().renameItem(OLD, NEW, "FetchUser");
    expect(useUIStore.getState().activeKey).toBe(OTHER);
  });

  it("moves the draft and invoke state to the new key, leaving unrelated ones in place", () => {
    useUIStore.getState().renameItem(OLD, NEW, "FetchUser");
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
    useUIStore.getState().renameItem(OLD, NEW, "FetchUser");
    expect(useUIStore.getState().treeSelection).toEqual([NEW, OTHER]);
  });

  it("remaps treeFocused when the renamed item was focused", () => {
    useUIStore.getState().renameItem(OLD, NEW, "FetchUser");
    expect(useUIStore.getState().treeFocused).toBe(NEW);
  });

  it("leaves treeFocused alone when a different row was focused", () => {
    useUIStore.setState({ treeFocused: OTHER });
    useUIStore.getState().renameItem(OLD, NEW, "FetchUser");
    expect(useUIStore.getState().treeFocused).toBe(OTHER);
  });

  // treeExpanded holds bare ids too (a folder's own membership when it's open).
  // No UI path reaches this today — CollectionPanel's doRename bails unless the
  // item is a request, and a request is never expandable, so it can never be a
  // treeExpanded member — but renameItem itself has to already be correct for
  // whenever T4b wires folder rename through this same function. Testing the
  // store function directly, bypassing the UI restriction, is how to verify that
  // ahead of time rather than after the fact.
  it("remaps an id inside treeExpanded, leaving other ids alone", () => {
    useUIStore.setState({ treeExpanded: new Set([OLD, OTHER]) });
    useUIStore.getState().renameItem(OLD, NEW, "FetchUser");
    expect(useUIStore.getState().treeExpanded).toEqual(new Set([NEW, OTHER]));
  });

  it("leaves treeExpanded's reference untouched when the renamed id isn't a member", () => {
    useUIStore.setState({ treeExpanded: new Set([OTHER]) });
    const before = useUIStore.getState().treeExpanded;
    useUIStore.getState().renameItem(OLD, NEW, "FetchUser");
    expect(useUIStore.getState().treeExpanded).toBe(before);
  });

  it("is a no-op when the key doesn't actually change", () => {
    const before = useUIStore.getState();
    useUIStore.getState().renameItem(OLD, OLD, "GetUser");
    const after = useUIStore.getState();
    // Reference-equal, not just deep-equal: set({}) must never rebuild
    // equivalent-looking-but-new containers for state nothing actually renamed.
    expect(after.openTabs).toBe(before.openTabs);
    expect(after.treeSelection).toBe(before.treeSelection);
    expect(after.drafts).toBe(before.drafts);
  });
});
