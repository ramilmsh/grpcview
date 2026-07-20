import type { ComponentType, SVGProps } from "react";
import * as Ph from "@phosphor-icons/react";

// Phosphor's published IconProps drops its inherited SVG props under pnpm's
// isolated store (its bundled .d.ts can't resolve @types/react, so
// `extends ComponentPropsWithoutRef<"svg">` collapses to nothing). The icon
// components accept style/className/onClick/etc. at runtime — only the types are
// wrong — so we re-export the icons we use with a correct prop type. Import
// Phosphor icons from here, not from "@phosphor-icons/react" directly.
export interface IconProps extends SVGProps<SVGSVGElement> {
  size?: number | string;
  weight?: "thin" | "light" | "regular" | "bold" | "fill" | "duotone";
  mirrored?: boolean;
  color?: string;
}
type Icon = ComponentType<IconProps>;

export const BracketsCurly = Ph.BracketsCurly as unknown as Icon;
export const Broadcast = Ph.Broadcast as unknown as Icon;
export const CaretDown = Ph.CaretDown as unknown as Icon;
export const CaretRight = Ph.CaretRight as unknown as Icon;
export const CheckCircle = Ph.CheckCircle as unknown as Icon;
export const Copy = Ph.Copy as unknown as Icon;
export const DownloadSimple = Ph.DownloadSimple as unknown as Icon;
export const FileArchive = Ph.FileArchive as unknown as Icon;
export const Folder = Ph.Folder as unknown as Icon;
export const FolderPlus = Ph.FolderPlus as unknown as Icon;
export const Gear = Ph.Gear as unknown as Icon;
export const HardDrives = Ph.HardDrives as unknown as Icon;
export const LockSimple = Ph.LockSimple as unknown as Icon;
export const LockSimpleOpen = Ph.LockSimpleOpen as unknown as Icon;
export const MagnifyingGlass = Ph.MagnifyingGlass as unknown as Icon;
export const Play = Ph.Play as unknown as Icon;
export const PlugsConnected = Ph.PlugsConnected as unknown as Icon;
export const Plus = Ph.Plus as unknown as Icon;
export const Stack = Ph.Stack as unknown as Icon;
export const Trash = Ph.Trash as unknown as Icon;
export const TreeStructure = Ph.TreeStructure as unknown as Icon;
export const Warning = Ph.Warning as unknown as Icon;
export const X = Ph.X as unknown as Icon;
