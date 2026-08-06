import { useRef, useState } from "react";
import { Broadcast, CaretDown, MagnifyingGlass, Gear } from "@/components/ui/icons";
import { Button, IconButton } from "@/components/ui/Button";
import { Kbd } from "@/components/ui/Kbd";
import { Menu } from "@/components/ui/Menu";
import { useActiveWorkspace, hostLabel, useCollections } from "@/lib/workspace-query";
import { useUIStore } from "@/lib/ui-store";
import { collectionSwitcherItems } from "@/features/workspace/collection-switcher";
import { NewCollectionDialog } from "@/features/workspace/NewCollectionDialog";
import { RenameCollectionDialog } from "@/features/workspace/RenameCollectionDialog";

export function TopBar() {
  const { collection, workspace, reflection, sources } = useActiveWorkspace();
  const { collections } = useCollections();
  const setActiveCollection = useUIStore((s) => s.setActiveCollection);
  const buttonRef = useRef<HTMLButtonElement>(null);
  const [menu, setMenu] = useState<{ x: number; y: number } | null>(null);
  const [creating, setCreating] = useState(false);
  const [renaming, setRenaming] = useState(false);
  // The NAME, disambiguated by the id in the tooltip: a collection is addressed by its
  // path and named separately, so five of them may all be called "requests".
  const collectionLabel = workspace?.name || collection || "";
  const connected = !!reflection;
  const sourceCount = `${sources.length} source${sources.length === 1 ? "" : "s"}`;

  return (
    <div
      className="flex items-center gap-[14px] px-[14px] bg-panel"
      style={{ height: 46, flex: "none", borderBottom: "1px solid var(--line)" }}
    >
      <div className="flex items-center gap-[9px]">
        <div
          className="flex items-center justify-center"
          style={{
            width: 22,
            height: 22,
            borderRadius: 6,
            background: "var(--color-accent)",
            color: "#161826",
            fontSize: 14,
          }}
        >
          <Broadcast weight="bold" />
        </div>
        <span
          className="font-heading"
          style={{ fontWeight: 600, fontSize: 15, letterSpacing: "-.01em" }}
        >
          grpcview
        </span>
      </div>

      <div style={{ width: 1, height: 20, background: "var(--line)" }} />

      {/* The workspace's collections, and the one always-reachable place a further collection
          is created: switching is UI state (every query key is built from the active id),
          creating writes to the repo. Anchored under the button, not at the pointer — this is
          a dropdown, not a context menu. */}
      <Button
        ref={buttonRef}
        className="text-neutral-200"
        style={{ padding: "4px 9px", fontSize: 13, gap: 7 }}
        title={collection ?? "No collection"}
        onClick={() => {
          const rect = buttonRef.current?.getBoundingClientRect();
          setMenu({ x: rect?.left ?? 0, y: (rect?.bottom ?? 0) + 4 });
        }}
      >
        <span className="text-accent" style={{ fontSize: 13 }}>❯</span>
        {collectionLabel || "No collection"}
        <CaretDown size={11} style={{ opacity: 0.5 }} />
      </Button>

      {menu ? (
        <Menu
          x={menu.x}
          y={menu.y}
          items={collectionSwitcherItems(collections, collection, {
            select: setActiveCollection,
            rename: () => setRenaming(true),
            createNew: () => setCreating(true),
          })}
          onClose={() => setMenu(null)}
        />
      ) : null}
      <NewCollectionDialog open={creating} onClose={() => setCreating(false)} />
      {collection !== null ? (
        <RenameCollectionDialog
          open={renaming}
          onClose={() => setRenaming(false)}
          collection={collection}
          // Empty, never the id, while the Get is still in flight: an unedited blank Name
          // field sends nothing, whereas one pre-filled with the id would rename the
          // collection to its own path on a stray Save.
          name={workspace?.name ?? ""}
        />
      ) : null}

      <div className="ml-auto flex items-center gap-[10px]">
        <Button
          variant="secondary"
          style={{ padding: "4px 10px", fontSize: 12, gap: 7 }}
          title="Search — not available in Phase 1"
          disabled
        >
          <MagnifyingGlass size={14} />
          Search
          <Kbd>⌘K</Kbd>
        </Button>
        <span
          className="flex items-center gap-[6px] font-mono"
          style={{ fontSize: 12, color: "var(--color-neutral-400)" }}
          title={
            connected
              ? `Reflection source: ${hostLabel(reflection)}`
              : sources.length > 0
                ? `${sourceCount}, none reflective — requests need a target of their own`
                : "No definition source added yet"
          }
        >
          <span
            className="dot"
            style={{ background: connected ? "var(--ok)" : "var(--color-neutral-600)" }}
          />
          {connected ? hostLabel(reflection) : sources.length > 0 ? sourceCount : "no source"}
        </span>
        <IconButton title="Settings — not available in Phase 1" disabled>
          <Gear />
        </IconButton>
      </div>
    </div>
  );
}
