import { useEffect, useMemo, useState } from "react";
import { X, MagnifyingGlass } from "@/components/ui/icons";
import type { Service, Method } from "@grpcview/v1/workspace_pb";
import { IconButton } from "@/components/ui/Button";
import { Backdrop } from "@/components/ui/Backdrop";
import { MethodKindTag } from "@/components/ui/Tag";
import { methodKind, serviceName } from "@/lib/format";

// Two-pane service → method picker, for both creating a request and repointing one.
export function MethodPickerModal({
  open,
  services,
  onClose,
  onSelect,
}: {
  open: boolean;
  services: Service[];
  onClose: () => void;
  onSelect: (service: Service, method: Method) => void;
}) {
  const [filter, setFilter] = useState("");
  const [selected, setSelected] = useState<number | null>(null);

  // The component stays mounted while closed, so reset per open.
  useEffect(() => {
    if (open) {
      setFilter("");
      setSelected(null);
    }
  }, [open]);

  const filtered = useMemo(() => {
    if (!filter) return services;
    const q = filter.toLowerCase();
    return services.filter(
      (s) =>
        s.name.toLowerCase().includes(q) ||
        s.package.toLowerCase().includes(q) ||
        s.methods.some((m) => m.name.toLowerCase().includes(q)),
    );
  }, [services, filter]);

  if (!open) return null;

  const active = selected !== null ? filtered[selected] : undefined;

  return (
    <Backdrop onClose={onClose}>
      <div
        className="flex flex-col"
        style={{
          width: "min(640px, 100%)",
          height: 520,
          background: "var(--color-surface)",
          borderRadius: "var(--radius-lg)",
          boxShadow: "var(--shadow-lg)",
          overflow: "hidden",
        }}
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
      >
        <div
          className="flex items-center"
          style={{
            padding: "12px 14px",
            borderBottom: "1px solid var(--line)",
            gap: 8,
          }}
        >
          <span className="dialog-title" style={{ flex: 1, fontSize: 17 }}>
            Select method
          </span>
          <IconButton onClick={onClose} title="Close">
            <X size={18} />
          </IconButton>
        </div>

        <div
          className="flex items-center gap-[8px]"
          style={{
            padding: "10px 14px",
            borderBottom: "1px solid var(--line)",
          }}
        >
          <MagnifyingGlass
            size={14}
            style={{ color: "var(--color-neutral-500)" }}
          />
          <input
            className="bare"
            style={{ fontSize: 13 }}
            placeholder="Search services or methods…"
            value={filter}
            onChange={(e) => {
              // `selected` indexes into `filtered`, which the query reorders.
              setFilter(e.target.value);
              setSelected(null);
            }}
            autoFocus
          />
        </div>

        <div className="flex" style={{ flex: 1, minHeight: 0 }}>
          <div
            style={{
              width: "45%",
              overflowY: "auto",
              borderRight: "1px solid var(--line)",
            }}
          >
            {filtered.length === 0 && (
              <div
                className="text-muted"
                style={{ padding: "16px", fontSize: 13, textAlign: "center" }}
              >
                No services. Add a definition source first.
              </div>
            )}
            {filtered.map((s, i) => (
              <div
                key={serviceName(s)}
                className="treerow"
                style={{
                  borderRadius: 0,
                  padding: "9px 14px",
                  ...(selected === i
                    ? { background: "var(--color-accent-900)" }
                    : {}),
                }}
                onClick={() => setSelected(i)}
              >
                <div style={{ minWidth: 0 }}>
                  <div style={{ fontSize: 13, color: "var(--color-text)" }}>
                    {s.name}
                  </div>
                  <div
                    className="font-mono"
                    style={{ fontSize: 11, color: "var(--color-neutral-500)" }}
                  >
                    {s.package}
                  </div>
                </div>
              </div>
            ))}
          </div>

          <div style={{ width: "55%", overflowY: "auto" }}>
            {active ? (
              active.methods.map((m) => (
                <div
                  key={m.name}
                  className="treerow"
                  style={{ borderRadius: 0, padding: "9px 14px" }}
                  onClick={() => {
                    onSelect(active, m);
                    onClose();
                  }}
                >
                  <MethodKindTag kind={methodKind(m)} />
                  <div style={{ minWidth: 0 }}>
                    <div style={{ fontSize: 13, color: "var(--color-text)" }}>
                      {m.name}
                    </div>
                    <div
                      className="font-mono"
                      style={{
                        fontSize: 11,
                        color: "var(--color-neutral-500)",
                      }}
                    >
                      {m.input?.name}
                    </div>
                  </div>
                </div>
              ))
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
    </Backdrop>
  );
}
