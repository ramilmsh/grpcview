export interface KeyStroke {
  key: string;
  shiftKey: boolean;
  metaKey: boolean;
  ctrlKey: boolean;
  altKey: boolean;
}

export type TreeIntent =
  | { kind: "move"; to: "up" | "down" | "first" | "last" | "pageUp" | "pageDown" }
  | { kind: "extend"; to: "up" | "down" }
  | { kind: "collapseOrParent" }
  | { kind: "expandOrFirstChild" }
  | { kind: "toggle" }
  | { kind: "open" }
  | { kind: "rename" }
  | { kind: "delete" }
  | { kind: "selectAll" }
  | { kind: "clearSelection" };

function onlyMetaHeld(stroke: KeyStroke): boolean {
  return stroke.metaKey && !stroke.shiftKey && !stroke.ctrlKey && !stroke.altKey;
}

function noModifiersHeld(stroke: KeyStroke): boolean {
  return !stroke.shiftKey && !stroke.metaKey && !stroke.ctrlKey && !stroke.altKey;
}

function onlyCtrlHeld(stroke: KeyStroke): boolean {
  return stroke.ctrlKey && !stroke.shiftKey && !stroke.metaKey && !stroke.altKey;
}

function onlyShiftHeld(stroke: KeyStroke): boolean {
  return stroke.shiftKey && !stroke.metaKey && !stroke.ctrlKey && !stroke.altKey;
}

export function keyToIntent(stroke: KeyStroke, isMac: boolean): TreeIntent | null {
  const { key } = stroke;

  // Every modified chord is resolved before the bare-key gate below, which
  // rejects any modifier outright.
  if (isMac && onlyMetaHeld(stroke)) {
    if (key === "ArrowDown") return { kind: "open" };
    if (key === "Backspace") return { kind: "delete" };
  }

  // The uppercase form is reachable with caps lock, since shift is excluded.
  if (key === "a" || key === "A") {
    if (isMac ? onlyMetaHeld(stroke) : onlyCtrlHeld(stroke)) return { kind: "selectAll" };
  }

  if (onlyShiftHeld(stroke)) {
    if (key === "ArrowUp") return { kind: "extend", to: "up" };
    if (key === "ArrowDown") return { kind: "extend", to: "down" };
  }

  if (!noModifiersHeld(stroke)) return null;

  switch (key) {
    case "ArrowUp":
      return { kind: "move", to: "up" };
    case "ArrowDown":
      return { kind: "move", to: "down" };
    case "Home":
      return { kind: "move", to: "first" };
    case "End":
      return { kind: "move", to: "last" };
    case "PageUp":
      return { kind: "move", to: "pageUp" };
    case "PageDown":
      return { kind: "move", to: "pageDown" };
    case "ArrowLeft":
      return { kind: "collapseOrParent" };
    case "ArrowRight":
      return { kind: "expandOrFirstChild" };
    case " ":
      return { kind: "toggle" };
    case "F2":
      return { kind: "rename" };
    case "Escape":
      // Unconditional: whether anything is selected is the consumer's check.
      return { kind: "clearSelection" };
    case "Enter":
      return isMac ? { kind: "rename" } : { kind: "open" };
    case "Delete":
      // macOS binds cmd+Backspace instead, handled above.
      return isMac ? null : { kind: "delete" };
    default:
      return null;
  }
}
