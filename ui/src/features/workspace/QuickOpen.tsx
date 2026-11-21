import { useEffect, useId, useMemo, useState, type KeyboardEvent } from "react";
import { MagnifyingGlass } from "@/components/ui/icons";
import { Backdrop } from "@/components/ui/Backdrop";
import { MethodKindTag } from "@/components/ui/Tag";
import { useUIStore } from "@/lib/ui-store";
import {
  useActiveWorkspace,
  useCollections,
  useRootItems,
} from "@/lib/workspace-query";
import { methodKind, resolveMethod, type ItemWithPath } from "@/lib/format";
import { shortcutLabel, shortcutLabels } from "@/lib/shortcuts";
import { useShortcut } from "@/lib/use-shortcuts";
import { highlightSegments } from "./method-dropdown";
import { moveIndex } from "./method-picker";
import { panelTiered } from "./panel-tree";
import { ancestorKeys, filterRequests, flattenRequests } from "./quick-open";

const muted = { color: "var(--v-text-2)" };
const ellipsis = {
  minWidth: 0,
  overflow: "hidden",
  textOverflow: "ellipsis",
  whiteSpace: "nowrap",
} as const;

// ⌘K: a fuzzy finder over the active collection's requests. Opening one is what a click on
// its tree row does (tab + selection), plus expanding its folders so the row is on screen.
export function QuickOpen() {
  const [open, setOpen] = useState(false);
  useShortcut("quickOpen", () => setOpen((o) => !o));
  return open ? <QuickOpenPalette onClose={() => setOpen(false)} /> : null;
}

// Mounted only while open, so every open starts with an empty query.
function QuickOpenPalette({ onClose }: { onClose: () => void }) {
  const { workspace, services } = useActiveWorkspace();
  const { collections } = useCollections();
  const rootItems = useRootItems(workspace);
  const openTab = useUIStore((s) => s.openTab);
  const setTreeSelection = useUIStore((s) => s.setTreeSelection);
  const setTreeFocused = useUIStore((s) => s.setTreeFocused);
  const setTreeExpanded = useUIStore((s) => s.setTreeExpanded);
  const [query, setQuery] = useState("");
  // null = the first match.
  const [picked, setPicked] = useState<number | null>(null);
  const id = useId();
  const listId = `${id}list`;
  const optionId = (i: number) => `${id}opt-${i}`;

  const requests = useMemo(() => flattenRequests(rootItems), [rootItems]);
  const rows = useMemo(
    () => filterRequests(requests, query),
    [requests, query],
  );
  const active =
    rows.length === 0 ? null : Math.min(picked ?? 0, rows.length - 1);

  useEffect(() => {
    if (active === null) return;
    document
      .getElementById(optionId(active))
      ?.scrollIntoView({ block: "nearest" });
    // optionId is stable for this mount.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active]);

  const pick = (item: ItemWithPath, key: string) => {
    onClose();
    openTab(item);
    setTreeSelection([key]);
    setTreeFocused(key);
    const expanded = useUIStore.getState().treeExpanded;
    setTreeExpanded(
      new Set([...expanded, ...ancestorKeys(item, panelTiered(collections))]),
    );
  };

  const onKeyDown = (e: KeyboardEvent) => {
    switch (e.key) {
      case "ArrowDown":
      case "ArrowUp":
        setPicked(
          moveIndex(active, rows.length, e.key === "ArrowDown" ? 1 : -1),
        );
        break;
      case "Enter":
        if (active !== null) pick(rows[active].item, rows[active].key);
        break;
      case "Escape":
        // The method picker's rule: first Esc clears a typed query, the next closes.
        if (query !== "") {
          setQuery("");
          setPicked(null);
        } else onClose();
        break;
      default:
        return;
    }
    e.preventDefault();
    e.stopPropagation();
  };

  return (
    <Backdrop onClose={onClose}>
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Quick open"
        data-shortcut-owner="quickOpen"
        className="flex flex-col"
        onClick={(e) => e.stopPropagation()}
        style={{
          alignSelf: "start",
          marginTop: "12vh",
          width: 560,
          maxWidth: "calc(100vw - 32px)",
          borderRadius: "var(--v-r-panel)",
          background: "var(--v-raised)",
          boxShadow: "var(--v-popover)",
          overflow: "hidden",
        }}
      >
        <div style={{ padding: "10px 10px 6px" }}>
          <div
            className="flex items-center"
            style={{
              gap: 8,
              height: 36,
              padding: "0 12px",
              borderRadius: "var(--v-r)",
              background: "var(--v-well)",
              boxShadow: "var(--v-sunken)",
            }}
          >
            <MagnifyingGlass size={14} style={{ ...muted, flex: "none" }} />
            <input
              type="text"
              role="combobox"
              aria-label="Search requests"
              aria-controls={listId}
              aria-expanded
              aria-activedescendant={
                active === null ? undefined : optionId(active)
              }
              autoFocus
              spellCheck={false}
              autoComplete="off"
              className="bare"
              placeholder="Open request…"
              value={query}
              onChange={(e) => {
                setQuery(e.target.value);
                setPicked(null);
              }}
              onKeyDown={onKeyDown}
              style={{
                flex: 1,
                minWidth: 0,
                height: 34,
                border: "none",
                background: "transparent",
                color: "var(--v-text)",
                fontSize: 13,
                outline: "none",
              }}
            />
          </div>
        </div>

        <div
          id={listId}
          role="listbox"
          aria-label="Requests"
          style={{ maxHeight: 360, overflowY: "auto", padding: "0 6px 6px" }}
        >
          {rows.map((r, i) => {
            const req =
              r.item.item.content.case === "request"
                ? r.item.item.content.value
                : undefined;
            const kind = methodKind(
              resolveMethod(services, req?.service ?? "", req?.method ?? ""),
            );
            const on = i === active;
            return (
              <div
                key={r.key}
                id={optionId(i)}
                role="option"
                aria-selected={on}
                className="flex items-center"
                style={{
                  gap: 10,
                  height: 36,
                  flex: "none",
                  padding: "0 10px",
                  borderRadius: "var(--v-r)",
                  cursor: "pointer",
                  background: on ? "var(--v-hover)" : undefined,
                  boxShadow: on
                    ? "inset 0 1px 0 rgba(255, 255, 255, 0.06)"
                    : undefined,
                }}
                // Keep focus in the search box.
                onMouseDown={(e) => e.preventDefault()}
                onMouseMove={() => {
                  if (!on) setPicked(i);
                }}
                onClick={() => pick(r.item, r.key)}
              >
                <MethodKindTag kind={kind} />
                <span
                  style={{
                    ...ellipsis,
                    flexShrink: 0,
                    maxWidth: "60%",
                    fontSize: 13,
                    color: "var(--v-text)",
                  }}
                >
                  {highlightSegments(r.item.item.name, r.hits).map((s, j) =>
                    s.hit ? (
                      <span
                        key={j}
                        style={{
                          color: "var(--v-accent-ink)",
                          background: "rgba(99, 102, 241, 0.30)",
                          borderRadius: 3,
                        }}
                      >
                        {s.text}
                      </span>
                    ) : (
                      <span key={j}>{s.text}</span>
                    ),
                  )}
                </span>
                <span style={{ ...ellipsis, flex: 1, fontSize: 12, ...muted }}>
                  {r.folder}
                </span>
              </div>
            );
          })}
          {rows.length === 0 && (
            <div
              style={{
                padding: "20px 10px",
                textAlign: "center",
                fontSize: 13,
                ...muted,
              }}
            >
              {requests.length === 0
                ? "No requests in this collection."
                : `No requests match “${query}”`}
            </div>
          )}
        </div>

        <div
          className="flex"
          style={{
            gap: 14,
            padding: "8px 14px",
            borderTop: "1px solid var(--v-line)",
            fontSize: 11.5,
            ...muted,
          }}
        >
          <span>{shortcutLabels("pickerMove").join(" ")} to move</span>
          <span>{shortcutLabel("pickerPick")} to open</span>
          <span>{shortcutLabel("pickerClose")} to close</span>
        </div>
      </div>
    </Backdrop>
  );
}
