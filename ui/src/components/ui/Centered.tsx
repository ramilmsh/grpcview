import type { ReactNode } from "react";

// Centered fills its flex parent and centers a short muted message — the shared
// empty/placeholder state for the request and response panes.
export function Centered({ children }: { children: ReactNode }) {
  return (
    <div
      className="flex items-center justify-center text-muted"
      style={{ flex: 1, fontSize: 13 }}
    >
      {children}
    </div>
  );
}
