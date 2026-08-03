import clsx from "clsx";
import type { ReactNode } from "react";

export function Subtab({
  active,
  onClick,
  children,
}: {
  active?: boolean;
  onClick?: () => void;
  children: ReactNode;
}) {
  return (
    <button className={clsx("subtab", active && "on")} onClick={onClick}>
      {children}
    </button>
  );
}
