import type { ReactNode } from "react";

export function Kbd({ children }: { children: ReactNode }) {
  return <span className="kbd">{children}</span>;
}
