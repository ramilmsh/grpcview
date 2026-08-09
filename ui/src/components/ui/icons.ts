import type { ComponentType, SVGProps } from "react";
import * as Ph from "@phosphor-icons/react";

// Phosphor's published IconProps drops its inherited SVG props under pnpm's isolated
// store, so import icons from here rather than from "@phosphor-icons/react" directly.
export interface IconProps extends SVGProps<SVGSVGElement> {
  size?: number | string;
  weight?: "thin" | "light" | "regular" | "bold" | "fill" | "duotone";
  mirrored?: boolean;
  color?: string;
}
type Icon = ComponentType<IconProps>;

export const ArrowClockwise = Ph.ArrowClockwise as unknown as Icon;
export const ArrowSquareOut = Ph.ArrowSquareOut as unknown as Icon;
export const ArrowsSplit = Ph.ArrowsSplit as unknown as Icon;
export const BracketsCurly = Ph.BracketsCurly as unknown as Icon;
export const Broadcast = Ph.Broadcast as unknown as Icon;
export const CaretDown = Ph.CaretDown as unknown as Icon;
export const CaretUp = Ph.CaretUp as unknown as Icon;
export const ClockCounterClockwise = Ph.ClockCounterClockwise as unknown as Icon;
export const CaretRight = Ph.CaretRight as unknown as Icon;
export const CheckCircle = Ph.CheckCircle as unknown as Icon;
export const Code = Ph.Code as unknown as Icon;
export const Copy = Ph.Copy as unknown as Icon;
export const Cube = Ph.Cube as unknown as Icon;
export const DownloadSimple = Ph.DownloadSimple as unknown as Icon;
export const File = Ph.File as unknown as Icon;
export const FileArchive = Ph.FileArchive as unknown as Icon;
export const Flask = Ph.Flask as unknown as Icon;
export const FloppyDisk = Ph.FloppyDisk as unknown as Icon;
export const Folder = Ph.Folder as unknown as Icon;
export const FolderOpen = Ph.FolderOpen as unknown as Icon;
export const FolderPlus = Ph.FolderPlus as unknown as Icon;
export const Function = Ph.Function as unknown as Icon;
export const Gear = Ph.Gear as unknown as Icon;
export const GitBranch = Ph.GitBranch as unknown as Icon;
export const Hammer = Ph.Hammer as unknown as Icon;
export const HardDrives = Ph.HardDrives as unknown as Icon;
export const Link = Ph.Link as unknown as Icon;
export const LinkBreak = Ph.LinkBreak as unknown as Icon;
export const ListNumbers = Ph.ListNumbers as unknown as Icon;
export const LockSimple = Ph.LockSimple as unknown as Icon;
export const LockSimpleOpen = Ph.LockSimpleOpen as unknown as Icon;
export const MagnifyingGlass = Ph.MagnifyingGlass as unknown as Icon;
export const Package = Ph.Package as unknown as Icon;
export const PencilSimple = Ph.PencilSimple as unknown as Icon;
export const Play = Ph.Play as unknown as Icon;
export const PlugsConnected = Ph.PlugsConnected as unknown as Icon;
export const Plus = Ph.Plus as unknown as Icon;
export const Shield = Ph.Shield as unknown as Icon;
export const ShieldCheck = Ph.ShieldCheck as unknown as Icon;
export const Square = Ph.Square as unknown as Icon;
export const Stack = Ph.Stack as unknown as Icon;
export const Stop = Ph.Stop as unknown as Icon;
export const Trash = Ph.Trash as unknown as Icon;
export const TreeStructure = Ph.TreeStructure as unknown as Icon;
export const Warning = Ph.Warning as unknown as Icon;
export const X = Ph.X as unknown as Icon;
