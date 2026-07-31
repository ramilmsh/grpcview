import { useEffect, useState, type ReactNode } from "react";

// Backdrop is the shared modal overlay: the Nocturne .dialog-backdrop, a click
// outside to close, and Escape to close. Each modal supplies its own card (which
// should stopPropagation + set role="dialog"). Mount it only while open — the
// Escape listener lives for exactly the backdrop's lifetime, and so does the
// focus save/restore below.
export function Backdrop({
  onClose,
  children,
}: {
  onClose: () => void;
  children: ReactNode;
}) {
  // Whatever had DOM focus when this modal opened, so closing it can hand focus
  // back instead of dropping it on <body>. Two things this fixes, both observed:
  // a modal opened from the keyboard used to leave the collection tree
  // un-keyboard-drivable after it closed (focus was on <body>, so the tree's own
  // onKeyDown could not fire again until the tree was clicked), and — since
  // Dialog.tsx now pulls focus INTO the card on open — without this the focus
  // would simply be lost every time any modal closes.
  //
  // Captured with a lazy useState initializer rather than in the mount effect on
  // purpose: the initializer runs during the FIRST render, before React commits
  // anything, so document.activeElement is still the opener. A mount effect runs
  // after the commit phase, by which point React has already applied any
  // `autoFocus` inside the modal (several callers have one) and/or Dialog's own
  // initial focus — so it would capture an element INSIDE the modal and
  // "restore" focus to a node that is being unmounted in the same breath.
  const [opener] = useState<Element | null>(() => document.activeElement);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  // Restore on unmount only — an unmount is the only way this modal closes (the
  // contract in the header: mount it only while open). `isConnected` guards the
  // common case where the opener died with the action it triggered (a row's own
  // trash button, whose row the confirmed delete just removed); focus then stays
  // wherever the browser put it, exactly as before this change, rather than
  // throwing on a detached node.
  useEffect(
    () => () => {
      if (opener instanceof HTMLElement && opener.isConnected) opener.focus();
    },
    [opener]
  );

  return (
    <div className="dialog-backdrop" style={{ zIndex: 60 }} onClick={onClose}>
      {children}
    </div>
  );
}
