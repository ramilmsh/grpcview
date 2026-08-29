import { useEffect, useRef, type ReactNode } from "react";
import { X } from "@/components/ui/icons";
import { IconButton } from "./Button";
import { Backdrop } from "./Backdrop";

// The standard form modal: the shared Backdrop plus the Nocturne .dialog card.
export function Dialog({
  open,
  onClose,
  title,
  children,
  width,
}: {
  open: boolean;
  onClose: () => void;
  title: ReactNode;
  children: ReactNode;
  width?: number;
}) {
  const cardRef = useRef<HTMLDivElement>(null);

  // Skipped when focus is already inside: several callers autoFocus an inner input.
  // Not rAF: rAF never fires in the hidden tab the browser-verification harness uses.
  useEffect(() => {
    if (!open) return;
    const card = cardRef.current;
    if (card && !card.contains(document.activeElement)) card.focus();
  }, [open]);

  if (!open) return null;

  return (
    <Backdrop onClose={onClose}>
      <div
        ref={cardRef}
        className="dialog"
        tabIndex={-1}
        style={{
          outline: "none",
          ...(width ? { width: `min(${width}px, 100%)` } : null),
        }}
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
      >
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <div className="dialog-title" style={{ flex: 1 }}>
            {title}
          </div>
          <IconButton onClick={onClose} title="Close">
            <X size={18} />
          </IconButton>
        </div>
        {children}
      </div>
    </Backdrop>
  );
}
