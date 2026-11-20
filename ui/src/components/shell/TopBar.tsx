import { useEffect, useRef, useState } from "react";
import {
  Broadcast,
  CaretDown,
  MagnifyingGlass,
  Gear,
  Stack,
} from "@/components/ui/icons";
import { Button, IconButton } from "@/components/ui/Button";
import { Kbd } from "@/components/ui/Kbd";
import { Menu } from "@/components/ui/Menu";
import {
  useActiveWorkspace,
  hostLabel,
  useCollections,
} from "@/lib/workspace-query";
import { useUIStore } from "@/lib/ui-store";
import { collectionSwitcherItems } from "@/features/workspace/collection-switcher";
import { NewCollectionDialog } from "@/features/workspace/NewCollectionDialog";
import { RenameCollectionDialog } from "@/features/workspace/RenameCollectionDialog";
import { SourcesPopover } from "@/features/sources/SourcesPopover";

export function TopBar() {
  const { collection, workspace, reflection, sources } = useActiveWorkspace();
  const { collections } = useCollections();
  const setActiveCollection = useUIStore((s) => s.setActiveCollection);
  // The definition-sources dropdown, hung right-aligned under its trigger.
  const [srcOpen, setSrcOpen] = useState(false);
  const srcWrapRef = useRef<HTMLDivElement>(null);
  const srcTriggerRef = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (!srcOpen) return;
    const onDown = (e: MouseEvent) => {
      if (!srcWrapRef.current?.contains(e.target as Node)) setSrcOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, [srcOpen]);
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
      className="flex items-center gap-[14px] px-[16px]"
      style={{
        height: 52,
        flex: "none",
        background: "var(--v-shell)",
      }}
    >
      <div className="flex items-center gap-[10px]">
        <div
          className="flex items-center justify-center"
          style={{
            width: 28,
            height: 28,
            borderRadius: "var(--v-r)",
            color: "var(--v-text-2)",
            fontSize: 18,
          }}
        >
          <Broadcast weight="bold" />
        </div>
        <span style={{ fontWeight: 600, fontSize: 16 }}>grpcview</span>
      </div>

      <div style={{ width: 1, height: 22, background: "var(--v-hover)" }} />

      {/* The workspace's collections, and the one always-reachable place a further collection
          is created: switching is UI state (every query key is built from the active id),
          creating writes to the repo. Anchored under the button, not at the pointer — this is
          a dropdown, not a context menu. */}
      <Button
        ref={buttonRef}
        style={{
          height: 32,
          padding: "0 10px",
          gap: 6,
          fontWeight: 400,
          boxShadow: "var(--v-raise)",
        }}
        title={collection ?? "No collection"}
        onClick={() => {
          const rect = buttonRef.current?.getBoundingClientRect();
          setMenu({ x: rect?.left ?? 0, y: (rect?.bottom ?? 0) + 4 });
        }}
      >
        <span>›</span>
        {collectionLabel || "No collection"}
        <CaretDown size={14} />
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
        {/* Sunken, not a button-looking button: a search field that opens a palette. */}
        <button
          type="button"
          className="flex items-center gap-[8px]"
          style={{
            height: 32,
            padding: "0 10px",
            border: "none",
            borderRadius: "var(--v-r)",
            background: "var(--v-well)",
            boxShadow: "var(--v-sunken)",
            color: "var(--v-text-2)",
            font: "inherit",
            cursor: "not-allowed",
          }}
          title="Search — not available in Phase 1"
          disabled
        >
          <MagnifyingGlass size={15} />
          Search
          <Kbd>⌘K</Kbd>
        </button>
        {/* Where this collection's services are reflected from, and the default invoke
            target. Not a live health check — so no status dot; the Sources icon instead. */}
        <div
          ref={srcWrapRef}
          style={{ position: "relative" }}
          onKeyDown={(e) => {
            if (e.key !== "Escape" || !srcOpen) return;
            e.stopPropagation();
            setSrcOpen(false);
            srcTriggerRef.current?.focus();
          }}
        >
          <button
            ref={srcTriggerRef}
            type="button"
            className="reflbtn"
            aria-haspopup="dialog"
            aria-expanded={srcOpen}
            onClick={() => setSrcOpen((o) => !o)}
            title={
              srcOpen
                ? undefined
                : sources.length === 0
                  ? "No definition source yet. Click to add one."
                  : `${sourceCount} — where this collection's services and methods are loaded from.\n` +
                    (connected
                      ? `Reflection: ${hostLabel(reflection)} — also where requests are sent unless they set their own target.`
                      : "None is a reflection server — requests need a target of their own.") +
                    "\nClick to see and refresh definition sources."
            }
          >
            <Stack size={15} />
            {sources.length === 0 ? (
              <span>No sources</span>
            ) : (
              <>
                <span style={{ color: "var(--v-text)" }}>{sourceCount}</span>
                <span style={{ color: "var(--v-text-muted)" }}>·</span>
                <span style={{ color: "var(--v-text-muted)" }}>
                  {connected ? "reflection" : "no reflection"}
                </span>
                {connected && (
                  <span
                    className="font-mono"
                    style={{ color: "var(--v-text)" }}
                  >
                    {hostLabel(reflection)}
                  </span>
                )}
              </>
            )}
          </button>
          {srcOpen && <SourcesPopover onClose={() => setSrcOpen(false)} />}
        </div>
        <IconButton title="Settings — not available in Phase 1" disabled>
          <Gear />
        </IconButton>
      </div>
    </div>
  );
}
