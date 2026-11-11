import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
} from "react";
import { MagnifyingGlass } from "@/components/ui/icons";
import type { Service, Method } from "@grpcview/v1/workspace_pb";
import { MethodKindTag } from "@/components/ui/Tag";
import { methodKind, serviceName } from "@/lib/format";
import {
  filterMethods,
  filterServices,
  KIND_NAME,
  moveIndex,
  type PickerPane,
} from "./method-picker";

const muted = { color: "var(--color-neutral-500)" };

// The two-pane service → method body: a search box over a services pane and the selected
// service's methods. Shared by MethodPickerModal (repointing a request) and
// NewRequestPage (creating one). Keyboard: Up/Down move within the active pane, Right/Left
// (outside the search box) or Tab/Shift+Tab switch panes, Enter picks.
export function MethodPicker({
  services,
  onPick,
  selected = null,
  query: controlledQuery,
  onQueryChange,
  searchVariant = "bar",
  autoFocus,
}: {
  services: Service[];
  onPick: (service: Service, method: Method) => void;
  // The method already chosen, outlined in the methods pane.
  selected?: { service: string; method: string } | null;
  // Controlled when given (the draft keeps its query across tab switches).
  query?: string;
  onQueryChange?: (q: string) => void;
  // "bar": an edge-to-edge strip (the modal); "field": a bordered input (the page).
  searchVariant?: "bar" | "field";
  autoFocus?: boolean;
}) {
  const [ownQuery, setOwnQuery] = useState("");
  const query = controlledQuery ?? ownQuery;
  const setQuery = onQueryChange ?? setOwnQuery;
  // By full name, not index: the query reshapes the list but must not drop the selection.
  const [service, setService] = useState<string | null>(
    selected?.service ?? null,
  );
  const [pane, setPane] = useState<PickerPane>("services");
  // Keyboard highlight in the methods pane; null = none, which is how it starts.
  const [methodIndex, setMethodIndex] = useState<number | null>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const servicesRef = useRef<HTMLDivElement>(null);
  const methodsRef = useRef<HTMLDivElement>(null);

  const shown = useMemo(
    () => filterServices(services, query),
    [services, query],
  );
  const serviceIndex = shown.findIndex((s) => serviceName(s) === service);
  const active = serviceIndex === -1 ? undefined : shown[serviceIndex];
  const methods = useMemo(
    () => (active ? filterMethods(active, query) : []),
    [active, query],
  );

  useEffect(() => {
    servicesRef.current
      ?.querySelector(`[data-index="${serviceIndex}"]`)
      ?.scrollIntoView({ block: "nearest" });
  }, [serviceIndex]);
  useEffect(() => {
    if (methodIndex === null) return;
    methodsRef.current
      ?.querySelector(`[data-index="${methodIndex}"]`)
      ?.scrollIntoView({ block: "nearest" });
  }, [methodIndex]);

  const toMethods = (): boolean => {
    if (!active || methods.length === 0) return false;
    setPane("methods");
    setMethodIndex((i) => i ?? 0);
    return true;
  };
  const toServices = (): boolean => {
    if (pane === "services") return false;
    setPane("services");
    setMethodIndex(null);
    return true;
  };

  const onKeyDown = (e: KeyboardEvent) => {
    const inSearch = e.target === searchRef.current;
    let handled = true;
    switch (e.key) {
      case "ArrowDown":
      case "ArrowUp": {
        const delta = e.key === "ArrowDown" ? 1 : -1;
        if (pane === "methods") {
          setMethodIndex(moveIndex(methodIndex, methods.length, delta));
        } else {
          const next = moveIndex(
            serviceIndex === -1 ? null : serviceIndex,
            shown.length,
            delta,
          );
          if (next !== null) setService(serviceName(shown[next]));
          setMethodIndex(null);
        }
        break;
      }
      case "ArrowRight":
        handled = !inSearch && toMethods();
        break;
      case "ArrowLeft":
        handled = !inSearch && toServices();
        break;
      case "Tab":
        handled = e.shiftKey
          ? toServices()
          : pane === "services" && toMethods();
        break;
      case "Enter":
        if (pane === "methods" && active && methodIndex !== null) {
          const m = methods[methodIndex];
          if (m) onPick(active, m);
        } else {
          handled = toMethods();
        }
        break;
      default:
        handled = false;
    }
    if (handled) e.preventDefault();
  };

  const search = (
    <input
      ref={searchRef}
      className={searchVariant === "field" ? "input" : "bare"}
      style={
        searchVariant === "field"
          ? { fontSize: 13, paddingLeft: 30 }
          : { fontSize: 13 }
      }
      placeholder="Search services or methods…"
      value={query}
      onChange={(e) => {
        setQuery(e.target.value);
        setMethodIndex(null);
      }}
      autoFocus={autoFocus}
    />
  );

  return (
    <div
      className="flex flex-col"
      style={{ flex: 1, minHeight: 0 }}
      onKeyDown={onKeyDown}
    >
      {searchVariant === "field" ? (
        <div style={{ position: "relative", padding: "0 24px 14px" }}>
          <MagnifyingGlass
            size={14}
            style={{
              ...muted,
              position: "absolute",
              left: 34,
              top: "calc(50% - 7px - 7px)",
            }}
          />
          {search}
        </div>
      ) : (
        <div
          className="flex items-center gap-[8px]"
          style={{
            padding: "10px 14px",
            borderBottom: "1px solid var(--line)",
          }}
        >
          <MagnifyingGlass size={14} style={muted} />
          {search}
        </div>
      )}

      <div
        className="flex"
        style={{
          flex: 1,
          minHeight: 0,
          ...(searchVariant === "field"
            ? { borderTop: "1px solid var(--line)" }
            : {}),
        }}
      >
        <div
          ref={servicesRef}
          role="listbox"
          aria-label="Services"
          tabIndex={-1}
          style={{
            width: "45%",
            overflowY: "auto",
            outline: "none",
            borderRight: "1px solid var(--line)",
          }}
        >
          {shown.length === 0 && (
            <div
              className="text-muted"
              style={{ padding: "16px", fontSize: 13, textAlign: "center" }}
            >
              {services.length === 0
                ? "No services. Add a definition source first."
                : "No service or method matches."}
            </div>
          )}
          {shown.map((s, i) => (
            <div
              key={serviceName(s)}
              data-index={i}
              role="option"
              aria-selected={i === serviceIndex}
              className="treerow"
              style={{
                borderRadius: 0,
                padding: "9px 14px",
                height: "auto",
                ...(i === serviceIndex
                  ? { background: "var(--color-accent-900)" }
                  : {}),
              }}
              onClick={() => {
                setService(serviceName(s));
                setPane("services");
                setMethodIndex(null);
              }}
            >
              <div style={{ minWidth: 0 }}>
                <div
                  className="truncate"
                  style={{ fontSize: 13, color: "var(--color-text)" }}
                >
                  {s.name}
                </div>
                <div
                  className="font-mono truncate"
                  style={{ fontSize: 11, ...muted }}
                >
                  {s.package}
                </div>
              </div>
            </div>
          ))}
        </div>

        <div
          ref={methodsRef}
          role="listbox"
          aria-label="Methods"
          tabIndex={-1}
          style={{ width: "55%", overflowY: "auto", outline: "none" }}
        >
          {active ? (
            methods.map((m, i) => {
              const picked =
                selected?.service === serviceName(active) &&
                selected.method === m.name;
              const kind = methodKind(m);
              return (
                <div
                  key={m.name}
                  data-index={i}
                  role="option"
                  aria-selected={picked}
                  className="treerow"
                  style={{
                    borderRadius: 6,
                    margin: "2px 6px",
                    padding: "8px 10px",
                    height: "auto",
                    boxShadow: picked
                      ? "inset 0 0 0 1px var(--color-accent)"
                      : undefined,
                    background:
                      pane === "methods" && i === methodIndex
                        ? "color-mix(in srgb, var(--color-text) 8%, transparent)"
                        : undefined,
                  }}
                  onClick={() => onPick(active, m)}
                >
                  <MethodKindTag kind={kind} />
                  <span
                    className="font-mono truncate"
                    style={{
                      fontSize: 12.5,
                      color: "var(--color-text)",
                      minWidth: 0,
                    }}
                  >
                    {m.name}
                  </span>
                  <span
                    style={{
                      marginLeft: "auto",
                      flex: "none",
                      fontSize: 11.5,
                      ...muted,
                    }}
                  >
                    {KIND_NAME[kind]}
                  </span>
                </div>
              );
            })
          ) : (
            <div
              className="flex items-center justify-center text-muted"
              style={{ height: "100%", fontSize: 13 }}
            >
              Select a service
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
