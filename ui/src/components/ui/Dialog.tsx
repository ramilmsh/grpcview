import { useEffect, useRef, type ReactNode } from "react";
import { X } from "@/components/ui/icons";
import { IconButton } from "./Button";
import { Backdrop } from "./Backdrop";

// Dialog is the standard form modal: the shared Backdrop + the Nocturne .dialog
// card with a title/close header. Closes on Escape and on backdrop click. On
// open it moves DOM focus into the card; Backdrop hands focus back to the opener
// when it unmounts.
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

  // Move focus into the dialog when it opens. This is the substance behind the
  // `aria-modal="true"` below, which until now promised something no caller
  // delivered: focus stayed on whatever was behind the modal, so that element's
  // own key handlers kept firing THROUGH the open dialog. The concrete bug
  // (observed in-browser): with a multi-row selection in the collection tree,
  // cmd+Backspace opened the delete-confirm dialog while DOM focus stayed on the
  // .tree div, so one Escape hit TWO independent listeners — the tree's own
  // onKeyDown ("clear the selection") and Backdrop's window keydown ("close") —
  // which cancelled the dialog and destroyed the very selection it was about to
  // delete, making cancel-then-retry impossible. The tree's preventDefault
  // cannot stop that: a window-level listener sees the event regardless. With
  // focus inside the card, the keydown's bubble path simply never includes the
  // tree, so only the Backdrop hears it and the selection survives. Same reason
  // a second Delete keypress no longer re-fires the host's delete handler and
  // rewrites the pending-confirm state underneath the open dialog.
  //
  // Only if focus is not ALREADY inside the card: several callers put `autoFocus`
  // on an inner input (AddSourceModal, MethodPickerModal, ScriptsView's new-script
  // dialog, CollectionPanel's new-folder dialog), React applies that during the
  // commit phase, i.e. before this effect runs — stealing it back to the
  // container would break every one of them.
  //
  // A plain useEffect, deliberately NOT requestAnimationFrame: rAF does not fire
  // in a hidden/background tab, which is exactly how the browser-verification
  // harness runs this app, so an rAF-deferred focus is both unverifiable and
  // genuinely absent there (see EditableName.tsx's rAF focus for the same trap).
  // The effect is keyed on `open` rather than living in a mounted-only inner
  // component so this stays one component; on the render where `open` flips true
  // the ref is already attached by the time effects run.
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
        // tabIndex -1 makes the card focusable programmatically without putting
        // it in the tab order, and `outline: none` keeps that from drawing
        // nocturne.css's :focus-visible ring around the WHOLE card — this is a
        // fallback focus holder, not a control the user aimed at.
        tabIndex={-1}
        style={{ outline: "none", ...(width ? { width: `min(${width}px, 100%)` } : null) }}
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
