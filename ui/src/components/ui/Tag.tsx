import clsx from "clsx";
import type { ReactNode } from "react";

type TagVariant = "accent" | "neutral";

export function Tag({
  variant = "neutral",
  className,
  // A one-word tag sometimes stands for a paragraph of meaning (Sources' "shared"), and the
  // native tooltip is where that paragraph goes.
  title,
  children,
}: {
  variant?: TagVariant;
  className?: string;
  title?: string;
  children: ReactNode;
}) {
  return (
    <span className={clsx("tag", `tag-${variant}`, className)} title={title}>
      {children}
    </span>
  );
}

// Unary, server-stream, client-stream, bidi.
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
