import type { ReactNode } from "react";

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
