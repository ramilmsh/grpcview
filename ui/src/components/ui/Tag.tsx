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

// The amber "Draft" marker on an unsaved request or source (v4).
export function DraftBadge() {
  return (
    <span
      style={{
        fontSize: 11,
        fontWeight: 500,
        lineHeight: 1.5,
        padding: "1px 7px",
        borderRadius: "var(--v-r-badge)",
        background: "var(--v-warn-bg)",
        color: "var(--v-warn)",
      }}
    >
      Draft
    </span>
  );
}

// Unary, server-stream, client-stream, bidi.
export type MethodKind = "u" | "ss" | "cs" | "bd";

const KIND_NAME: Record<MethodKind, string> = {
  u: "Unary",
  ss: "Server stream",
  cs: "Client stream",
  bd: "Bidi stream",
};

const KIND_LABEL: Record<MethodKind, string> = {
  u: "U",
  ss: "S→",
  cs: "C←",
  bd: "B⇄",
};

// Never dimmed: colors are per-surface (app-tokens.css) so every tag holds 4.5:1.
export function MethodKindTag({
  kind = "u",
  className,
}: {
  kind?: MethodKind;
  className?: string;
}) {
  return (
    <span
      className={clsx("mtag", `mt-${kind}`, className)}
      role="img"
      aria-label={KIND_NAME[kind]}
      title={KIND_NAME[kind]}
    >
      {KIND_LABEL[kind]}
    </span>
  );
}

// A method-kind slot before any method is chosen (a draft request): a dashed "?".
export function DraftKindTag({ kind }: { kind: MethodKind | null }) {
  return kind ? (
    <MethodKindTag kind={kind} />
  ) : (
    <span className="mtag mt-none" aria-label="No method yet">
      ?
    </span>
  );
}
