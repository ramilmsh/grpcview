import { useEffect, useState } from "react";
import { useShortcut } from "@/lib/use-shortcuts";

const SHOW_MS = 1400;

// ⌘S in a view that autosaves: swallows the browser's "Save page" and briefly says why
// nothing else happens. Mount it inside a `position: relative` box of the autosaving view.
export function SavedHint() {
  const [shownAt, setShownAt] = useState<number | null>(null);
  useShortcut("save", () => setShownAt(Date.now()));
  useEffect(() => {
    if (shownAt === null) return;
    const t = window.setTimeout(() => setShownAt(null), SHOW_MS);
    return () => window.clearTimeout(t);
  }, [shownAt]);
  return (
    <div
      role="status"
      aria-live="polite"
      style={{
        position: "absolute",
        top: 10,
        left: "50%",
        transform: "translateX(-50%)",
        zIndex: 40,
        padding: "4px 10px",
        borderRadius: "var(--v-r-badge)",
        background: "var(--v-raised)",
        boxShadow: "var(--v-popover)",
        color: "var(--v-text-2)",
        fontSize: 12,
        pointerEvents: "none",
        opacity: shownAt === null ? 0 : 1,
        transition: "opacity .15s",
      }}
    >
      {shownAt === null ? null : "Saved automatically"}
    </div>
  );
}
