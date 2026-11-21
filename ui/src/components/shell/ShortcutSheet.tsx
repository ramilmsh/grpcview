import { Fragment, useEffect, useRef, useState } from "react";
import { X } from "@/components/ui/icons";
import { Backdrop } from "@/components/ui/Backdrop";
import { IconButton } from "@/components/ui/Button";
import { Kbd } from "@/components/ui/Kbd";
import {
  SHORTCUT_GROUPS,
  SHORTCUT_IDS,
  shortcutDef,
  shortcutLabels,
} from "@/lib/shortcuts";
import { useShortcut } from "@/lib/use-shortcuts";

// "?": every registry shortcut, grouped, with this platform's labels.
export function ShortcutSheet() {
  const [open, setOpen] = useState(false);
  useShortcut("cheatSheet", () => setOpen((o) => !o));
  return open ? <Sheet onClose={() => setOpen(false)} /> : null;
}

function Sheet({ onClose }: { onClose: () => void }) {
  const cardRef = useRef<HTMLDivElement>(null);
  useEffect(() => cardRef.current?.focus(), []);
  return (
    <Backdrop onClose={onClose}>
      <div
        ref={cardRef}
        role="dialog"
        aria-modal="true"
        aria-label="Keyboard shortcuts"
        data-shortcut-owner="cheatSheet"
        tabIndex={-1}
        className="dialog"
        onClick={(e) => e.stopPropagation()}
        style={{
          outline: "none",
          width: "min(720px, 100%)",
          maxHeight: "calc(100vh - 64px)",
          overflowY: "auto",
        }}
      >
        <div className="flex items-center" style={{ gap: 8 }}>
          <div className="dialog-title" style={{ flex: 1 }}>
            Keyboard shortcuts
          </div>
          <IconButton onClick={onClose} title="Close">
            <X size={18} />
          </IconButton>
        </div>
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "repeat(auto-fit, minmax(300px, 1fr))",
            gap: "16px 28px",
          }}
        >
          {SHORTCUT_GROUPS.map((group) => (
            <section key={group}>
              <div
                style={{
                  fontSize: 11,
                  letterSpacing: ".08em",
                  textTransform: "uppercase",
                  color: "var(--v-text-2)",
                  marginBottom: 6,
                }}
              >
                {group}
              </div>
              {SHORTCUT_IDS.filter((id) => shortcutDef(id).group === group).map(
                (id) => {
                  const labels = shortcutLabels(id);
                  if (labels.length === 0) return null;
                  return (
                    <div
                      key={id}
                      className="flex items-center"
                      style={{ gap: 12, padding: "4px 0", fontSize: 13 }}
                    >
                      <span style={{ flex: 1, color: "var(--v-text)" }}>
                        {shortcutDef(id).description}
                      </span>
                      <span
                        className="flex items-center"
                        style={{
                          gap: 4,
                          flexWrap: "wrap",
                          justifyContent: "end",
                        }}
                      >
                        {labels.map((l, i) => (
                          <Fragment key={l}>
                            {i > 0 && (
                              <span style={{ color: "var(--v-text-muted)" }}>
                                /
                              </span>
                            )}
                            <Kbd>{l}</Kbd>
                          </Fragment>
                        ))}
                      </span>
                    </div>
                  );
                },
              )}
            </section>
          ))}
        </div>
      </div>
    </Backdrop>
  );
}
