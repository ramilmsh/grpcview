import type { CSSProperties, ReactNode } from "react";
import clsx from "clsx";

// The v4 inset content panel: lighter than the shell, raised, floating inside the app
// card. Each view wraps its main area (not its sidebar) in one.
export function MainPanel({
  children,
  className,
  style,
}: {
  children: ReactNode;
  className?: string;
  style?: CSSProperties;
}) {
  return (
    <main
      className={clsx("flex flex-col", className)}
      style={{
        flex: 1,
        minWidth: 0,
        minHeight: 0,
        overflow: "hidden",
        margin: "0 12px 0 0",
        borderRadius: "var(--v-r-panel)",
        background: "var(--v-panel)",
        boxShadow: "var(--v-raise)",
        ...style,
      }}
    >
      {children}
    </main>
  );
}
