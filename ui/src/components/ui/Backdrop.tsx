import { useEffect, type ReactNode } from "react";

// Backdrop is the shared modal overlay: the Nocturne .dialog-backdrop, a click
// outside to close, and Escape to close. Each modal supplies its own card (which
// should stopPropagation + set role="dialog"). Mount it only while open — the
// Escape listener lives for exactly the backdrop's lifetime.
export function Backdrop({
  onClose,
  children,
}: {
  onClose: () => void;
  children: ReactNode;
}) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div className="dialog-backdrop" style={{ zIndex: 60 }} onClick={onClose}>
      {children}
    </div>
  );
}
