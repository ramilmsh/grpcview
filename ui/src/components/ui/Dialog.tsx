import { type ReactNode } from "react";
import { X } from "@/components/ui/icons";
import { IconButton } from "./Button";
import { Backdrop } from "./Backdrop";

// Dialog is the standard form modal: the shared Backdrop + the Nocturne .dialog
// card with a title/close header. Closes on Escape and on backdrop click.
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
  if (!open) return null;

  return (
    <Backdrop onClose={onClose}>
      <div
        className="dialog"
        style={width ? { width: `min(${width}px, 100%)` } : undefined}
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
