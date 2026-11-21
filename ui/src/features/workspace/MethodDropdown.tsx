import {
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
} from "react";
import type { Service, Method } from "@grpcview/v1/workspace_pb";
import {
  CaretDown,
  CaretUp,
  Check,
  MagnifyingGlass,
} from "@/components/ui/icons";
import { Button } from "@/components/ui/Button";
import { MethodKindTag } from "@/components/ui/Tag";
import { methodKind, serviceName } from "@/lib/format";
import { shortcutLabel, shortcutLabels } from "@/lib/shortcuts";
import { KIND_NAME, moveIndex } from "./method-picker";
import {
  groupMethods,
  highlightSegments,
  initialActive,
  shortServiceName,
} from "./method-dropdown";

const muted = { color: "var(--v-text-2)" };
const ellipsis = {
  minWidth: 0,
  overflow: "hidden",
  textOverflow: "ellipsis",
  whiteSpace: "nowrap",
} as const;

// The request header's method picker: one trigger showing "pkg.Service / Method", opening
// a searchable list of every method grouped by service. Up/Down move, Enter picks, Esc
// closes; both return focus to the trigger. The wrapper shrinks first in the header row
// (flex-shrink 1000), and inside the trigger the service gives way before the method.
export function MethodDropdown({
  services,
  current,
  onPick,
}: {
  services: Service[];
  // The request's method, by full service name; null while none is chosen.
  current: { service: string; method: string } | null;
  onPick: (service: Service, method: Method) => void;
}) {
  const [open, setOpen] = useState(false);
  const wrapRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (!wrapRef.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, [open]);

  const close = () => {
    setOpen(false);
    triggerRef.current?.focus();
  };

  const full = current ? `${current.service}/${current.method}` : undefined;

  return (
    <div
      ref={wrapRef}
      style={{ position: "relative", minWidth: 0, flexShrink: 1000 }}
    >
      <Button
        ref={triggerRef}
        aria-haspopup="listbox"
        aria-expanded={open}
        title={full ?? "Select method"}
        onClick={() => setOpen((o) => !o)}
        style={{
          height: 36,
          padding: "0 10px 0 12px",
          gap: 8,
          borderRadius: 10,
          fontSize: 14,
          fontWeight: 400,
          maxWidth: "100%",
          minWidth: 0,
          ...(open ? { background: "var(--v-hover)" } : {}),
          boxShadow: `inset 0 1px 0 rgba(255, 255, 255, 0.07), 0 0 0 1px ${
            open ? "rgba(99, 102, 241, 0.6)" : "rgba(255, 255, 255, 0.06)"
          }`,
        }}
      >
        {current ? (
          <>
            <span style={{ ...ellipsis, ...muted, flexShrink: 1000 }}>
              {shortServiceName(current.service)}
            </span>
            <span style={{ color: "var(--v-text-muted)", flex: "none" }}>
              /
            </span>
            <span
              style={{
                ...ellipsis,
                color: "var(--v-text)",
                fontWeight: 500,
                flexShrink: 1,
              }}
            >
              {current.method}
            </span>
          </>
        ) : (
          <span style={{ ...ellipsis, ...muted }}>Select method</span>
        )}
        {open ? (
          <CaretUp size={13} style={{ ...muted, flex: "none" }} />
        ) : (
          <CaretDown size={13} style={{ ...muted, flex: "none" }} />
        )}
      </Button>

      {open && (
        <MethodPopover
          services={services}
          current={current}
          onClose={close}
          onPick={(s, m) => {
            close();
            onPick(s, m);
          }}
        />
      )}
    </div>
  );
}

// Mounted only while open, so every open starts with an empty query.
function MethodPopover({
  services,
  current,
  onClose,
  onPick,
}: {
  services: Service[];
  current: { service: string; method: string } | null;
  onClose: () => void;
  onPick: (service: Service, method: Method) => void;
}) {
  const [query, setQuery] = useState("");
  // null = derived from the query (current method, or first match).
  const [picked, setPicked] = useState<number | null>(null);
  const id = useId();
  const listId = `${id}list`;
  const searchId = `${id}search`;
  const optionId = (i: number) => `${id}opt-${i}`;

  const { groups, rows } = useMemo(
    () => groupMethods(services, query),
    [services, query],
  );
  const active =
    rows.length === 0
      ? null
      : picked !== null
        ? Math.min(picked, rows.length - 1)
        : initialActive(rows, query, current);

  useEffect(() => {
    if (active === null) return;
    document
      .getElementById(optionId(active))
      ?.scrollIntoView({ block: "nearest" });
    // optionId is stable for this mount.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active]);

  const onKeyDown = (e: KeyboardEvent) => {
    switch (e.key) {
      case "ArrowDown":
      case "ArrowUp":
        setPicked(
          moveIndex(active, rows.length, e.key === "ArrowDown" ? 1 : -1),
        );
        break;
      case "Enter":
        if (active !== null) onPick(rows[active].service, rows[active].method);
        break;
      case "Escape":
        // First Esc clears a typed query; Esc on an empty query closes.
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

  const isCurrent = (s: Service, m: Method) =>
    !!current &&
    current.method === m.name &&
    current.service === serviceName(s);

  return (
    <div
      className="flex flex-col"
      // Owns the keyboard while open: the global shortcut listener stands down.
      data-popover
      style={{
        position: "absolute",
        left: 0,
        top: 44,
        zIndex: 50,
        width: 480,
        maxWidth: "calc(100vw - 32px)",
        borderRadius: "var(--v-r-panel)",
        background: "var(--v-raised)",
        boxShadow:
          "inset 0 1px 0 rgba(255, 255, 255, 0.08), 0 16px 40px -8px rgba(0, 0, 0, 0.7), 0 0 0 1px rgba(255, 255, 255, 0.07)",
        overflow: "hidden",
      }}
    >
      <div style={{ padding: "10px 10px 6px" }}>
        <label htmlFor={searchId} className="sr-only">
          Search methods
        </label>
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
            id={searchId}
            type="text"
            role="combobox"
            aria-controls={listId}
            aria-expanded
            aria-activedescendant={
              active === null ? undefined : optionId(active)
            }
            autoFocus
            spellCheck={false}
            autoComplete="off"
            className="bare font-mono"
            placeholder="Search Service/Method…"
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
        aria-label="Methods"
        style={{ maxHeight: 360, overflowY: "auto", padding: "0 6px 6px" }}
      >
        {groups.map((g) => (
          <div
            key={g.full}
            role="group"
            aria-label={g.full}
            className="flex flex-col"
          >
            <div
              role="presentation"
              className="flex items-baseline"
              style={{ gap: 8, padding: "10px 10px 4px" }}
            >
              <span
                style={{
                  fontSize: 12,
                  fontWeight: 600,
                  color: "var(--v-text)",
                }}
              >
                {g.service.name}
              </span>
              <span
                className="font-mono"
                style={{ ...ellipsis, fontSize: 11.5, ...muted }}
              >
                {g.service.package}
              </span>
            </div>
            {g.rows.map((r) => {
              const kind = methodKind(r.method);
              const cur = isCurrent(r.service, r.method);
              const on = r.index === active;
              return (
                <div
                  key={r.method.name}
                  id={optionId(r.index)}
                  role="option"
                  aria-selected={cur}
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
                    if (!on) setPicked(r.index);
                  }}
                  onClick={() => onPick(r.service, r.method)}
                >
                  <MethodKindTag kind={kind} />
                  <span
                    className="font-mono"
                    style={{
                      ...ellipsis,
                      flex: 1,
                      fontSize: 13,
                      color: "var(--v-text)",
                    }}
                  >
                    {highlightSegments(r.method.name, r.hits).map((s, i) =>
                      s.hit ? (
                        <span
                          key={i}
                          style={{
                            color: "var(--v-accent-ink)",
                            background: "rgba(99, 102, 241, 0.30)",
                            borderRadius: 3,
                          }}
                        >
                          {s.text}
                        </span>
                      ) : (
                        <span key={i}>{s.text}</span>
                      ),
                    )}
                  </span>
                  <span style={{ fontSize: 12, ...muted, flex: "none" }}>
                    {KIND_NAME[kind]}
                  </span>
                  <span
                    className="flex justify-center"
                    style={{ width: 16, flex: "none", color: "#818CF8" }}
                  >
                    {cur && <Check size={14} weight="bold" />}
                  </span>
                </div>
              );
            })}
          </div>
        ))}
        {rows.length === 0 && (
          <div
            style={{
              padding: "20px 10px",
              textAlign: "center",
              fontSize: 13,
              ...muted,
            }}
          >
            {services.length === 0
              ? "No services. Add a definition source first."
              : `No methods match “${query}”`}
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
        <span>{shortcutLabel("pickerPick")} to pick</span>
        <span>{shortcutLabel("pickerClose")} to close</span>
      </div>
    </div>
  );
}
