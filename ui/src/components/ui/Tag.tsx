import clsx from "clsx";
import type { ReactNode } from "react";

type TagVariant = "accent" | "neutral";

export function Tag({
  variant = "neutral",
  className,
  children,
}: {
  variant?: TagVariant;
  className?: string;
  children: ReactNode;
}) {
  return <span className={clsx("tag", `tag-${variant}`, className)}>{children}</span>;
}

// Method kind. Phase 1 is unary-only ("u"); the streaming kinds (server/client/
// bidi) arrive with the streaming backend (plan §5, Phase 3) but the labels are
// defined here so the tree/tabs can adopt them without a rename.
export type MethodKind = "u" | "ss" | "cs" | "bd";

const KIND_LABEL: Record<MethodKind, string> = {
  u: "U",
  ss: "S←",
  cs: "C→",
  bd: "B⇄",
};

export function MethodKindTag({
  kind = "u",
  className,
}: {
  kind?: MethodKind;
  className?: string;
}) {
  return <span className={clsx("mtag", `mt-${kind}`, className)}>{KIND_LABEL[kind]}</span>;
}
