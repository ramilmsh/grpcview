import { useEffect, useState, type ReactNode } from "react";

// Backdrop is the shared modal overlay: the Nocturne .dialog-backdrop, a click
// outside to close, and Escape to close. Each modal supplies its own card (which
// should stopPropagation + set role="dialog"). Mount it only while open — the
// Escape listener lives for exactly the backdrop's lifetime, and so does the
// focus save/restore below.
export function Backdrop({
  onClose,
  transparent,
  children,
}: {
  onClose: () => void;
  // Keep the overlay's BEHAVIOR (outside click, Escape, focus save/restore) but
  // drop its appearance: no dimming wash, no centering grid, no padding. Added
  // for the T5 context menu (components/ui/Menu.tsx), which needs every one of
  // those behaviors verbatim but positions its own card at a point in the
  // viewport and must not tint the app behind it — a context menu that dimmed
  // the whole window would read as a modal dialog. A prop rather than a second
  // Backdrop-shaped component specifically so the focus save/restore logic below
  // is never forked: it is subtle enough (see the lazy-initializer comment) that
  // two copies would drift.
  transparent?: boolean;
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
  //
  // Restore only if focus is currently NOWHERE, which is exactly the state this
  // exists to repair: the card that held focus was just removed from the DOM, so
  // the browser reset activeElement to <body>. If something else already owns
  // focus, that something is more recent than this unmount and must win.
  //
  // Not defensive padding — T5's context menu (Menu.tsx) makes it reachable. A
  // menu item that opens a dialog unmounts this backdrop and mounts the dialog in
  // ONE commit, and React applies `autoFocus` on the dialog's input during the
  // LAYOUT phase, i.e. before any passive effect cleanup like this one. Without
  // the guard the input would be focused and then immediately un-focused, and
  // every "New folder"/"New request" opened from the context menu would land with
  // its name field dead. (The mirror case still works either way: a menu item that
  // starts an inline rename focuses via a passive effect CREATE, and React runs
  // every destroy before any create, so the rename box focuses after this and
  // wins.)
  useEffect(
    () => () => {
      const active = document.activeElement;
      const focusIsStray = active === null || active === document.body;
      if (focusIsStray && opener instanceof HTMLElement && opener.isConnected) opener.focus();
    },
    [opener]
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
