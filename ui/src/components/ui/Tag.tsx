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

const KIND_LABEL: Record<MethodKind, string> = {
  u: "U",
  ss: "S←",
  cs: "C→",
  bd: "B⇄",
};

export function MethodKindTag({
  kind = "u",
  className,
  dim,
}: {
  kind?: MethodKind;
  className?: string;
  // v4: a tree row that is not the open tab mutes its badge.
  dim?: boolean;
}) {
  return (
    <span
      className={clsx("mtag", `mt-${kind}`, className)}
      style={dim ? { opacity: 0.55 } : undefined}
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
