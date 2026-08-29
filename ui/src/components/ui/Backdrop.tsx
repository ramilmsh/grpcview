import { useEffect, useState, type ReactNode } from "react";

// The shared modal overlay: outside click to close, Escape to close, and focus
// save/restore. Mount it only while open — the listeners live for its lifetime.
export function Backdrop({
  onClose,
  transparent,
  children,
}: {
  onClose: () => void;
  transparent?: boolean;
  children: ReactNode;
}) {
  // A lazy initializer, not a mount effect: it runs before React commits, so
  // activeElement is still the opener rather than something inside the modal.
  const [opener] = useState<Element | null>(() => document.activeElement);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  // Restore only when focus went nowhere: anything that already owns focus is newer
  // than this unmount and must win (a dialog's autoFocus runs before this cleanup).
  useEffect(
    () => () => {
      const active = document.activeElement;
      const focusIsStray = active === null || active === document.body;
      if (focusIsStray && opener instanceof HTMLElement && opener.isConnected)
        opener.focus();
    },
    [opener],
  );

  return (
    <div
      className={transparent ? "dialog-backdrop clear" : "dialog-backdrop"}
      style={{ zIndex: 60 }}
      onClick={onClose}
    >
      {children}
    </div>
  );
}
