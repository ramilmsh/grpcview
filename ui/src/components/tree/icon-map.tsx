import type { ComponentType, ReactNode } from "react";
import {
  Cube,
  File,
  Folder,
  FolderOpen,
  Function,
  ListNumbers,
  Square,
  type IconProps,
} from "@/components/ui/icons";
import type { IconToken } from "./types";

interface IconEntry {
  Icon: ComponentType<IconProps>;
  weight?: IconProps["weight"];
}

const ICON_BY_TOKEN: Record<IconToken, IconEntry> = {
  folder: { Icon: Folder, weight: "fill" },
  // An OPEN folder outline, deliberately unfilled: a root row must not read as just
  // another folder, and outline-vs-fill plus the angled front panel are two clearly
  // different silhouettes at 14px.
  "root-folder": { Icon: FolderOpen },
  file: { Icon: File },
  "symbol-class": { Icon: Cube },
  "symbol-enum": { Icon: ListNumbers },
  "symbol-field": { Icon: Square, weight: "fill" },
  "symbol-method": { Icon: Function },
};

const DEFAULT_SIZE = 14;

export function TreeIcon({ token, size }: { token: IconToken; size?: number }): ReactNode {
  const { Icon, weight } = ICON_BY_TOKEN[token];
  return (
    <Icon size={size ?? DEFAULT_SIZE} weight={weight} style={{ color: "var(--color-neutral-500)" }} />
  );
}
