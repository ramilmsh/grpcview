import type { ComponentType, ReactNode } from "react";
import {
  Cube,
  File,
  Folder,
  Function,
  ListNumbers,
  Square,
  type IconProps,
} from "@/components/ui/icons";
import type { IconToken } from "./types";

// The tree's visual vocabulary (docs/design/tree-rewrite-plan.md §"Second consumer",
// "one provider, two renderers"). A portable TreeAdapter names icons from the closed
// IconToken union in types.ts, never a Phosphor component directly, so one provider
// renders here (this table, Phosphor) or under VS Code's native tree — folder, file,
// symbol-class, symbol-enum, symbol-field and symbol-method are themselves valid
// ThemeIcon/codicon ids, so that renderer gets its icons for free and needs no table
// of its own. This file is the standalone half of that split; it has no counterpart
// to keep in sync beyond the vocabulary itself. Add a token to types.ts and to this
// map together — a token with no entry here is a bug, not a fallback.

interface IconEntry {
  Icon: ComponentType<IconProps>;
  weight?: IconProps["weight"];
}

const ICON_BY_TOKEN: Record<IconToken, IconEntry> = {
  // The tree's one "container" glyph; filled so it reads solid at a glance — matches
  // the Folder icon TreeView.tsx already renders (this table replaces that call site,
  // it doesn't reinvent its look).
  folder: { Icon: Folder, weight: "fill" },
  // VS Code's icon-theme-less default is a plain page outline; `File` is the direct
  // Phosphor equivalent. Left unfilled so it never gets mistaken for a folder.
  file: { Icon: File },
  // symbol-class (VS Code codicon): a message/class is a structured, composite type.
  // Outline, not fill — filling Cube collapses its edges into a blob at 13-14px and
  // loses the "structured type" read that is the reason to pick a cube at all.
  "symbol-class": { Icon: Cube },
  // symbol-enum: a closed set of named constants. A numbered list is the closest
  // literal Phosphor stand-in for "enumerated values", and its silhouette is
  // unmistakable next to Cube or Square at a glance.
  "symbol-enum": { Icon: ListNumbers },
  // symbol-field: one value slot on a message. VS Code's own codicon is a small
  // solid marker, not an outline — filled Square reads as that same solid dot;
  // unfilled it is too faint to register beside the others at this size.
  "symbol-field": { Icon: Square, weight: "fill" },
  // symbol-method: a callable. `Function` is already this app's glyph for "a
  // computed/callable value" (ScriptsView's generator rows) — one glyph, one
  // meaning, reused rather than picking a second icon for the same idea.
  "symbol-method": { Icon: Function },
};

// Matches the other row-content icons in this app (MagnifyingGlass, BracketsCurly,
// ArrowsSplit, …) and sits comfortably inside the plan's 22px default row height.
const DEFAULT_SIZE = 14;

export function TreeIcon({ token, size }: { token: IconToken; size?: number }): ReactNode {
  const { Icon, weight } = ICON_BY_TOKEN[token];
  // Colour from the same token the current tree already uses for its icons
  // (TreeView.tsx's Folder/CaretDown/CaretRight) — never a hardcoded hex, and fixed
  // rather than prop-driven: a row's selected/open/hover state changes the row's
  // background and text (.sel/.on in app-tokens.css), not its icon's tint, matching
  // how the current tree's Folder icon already ignores the "on" row state today.
  return (
    <Icon size={size ?? DEFAULT_SIZE} weight={weight} style={{ color: "var(--color-neutral-500)" }} />
  );
}
