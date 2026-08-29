import {
  Fragment,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import clsx from "clsx";
import { Backdrop } from "./Backdrop";

export interface MenuItem {
  label: string;
  onSelect: () => void;
  danger?: boolean;
  disabled?: boolean;
  separatorBefore?: boolean;
}

// Moves `delta` items from `from` (null = just outside the array), skipping disabled,
// wrapping at both ends. Null only when no item is selectable.
// eslint-disable-next-line react-refresh/only-export-components
export function stepMenuIndex(
  items: readonly Pick<MenuItem, "disabled">[],
  from: number | null,
  delta: 1 | -1,
): number | null {
  const n = items.length;
  if (n === 0) return null;
  const start = from ?? (delta === 1 ? -1 : n);
  for (let step = 1; step <= n; step++) {
    const index = (((start + delta * step) % n) + n) % n;
    if (!items[index].disabled) return index;
  }
  return null;
}

export interface MenuPositionInput {
  x: number;
  y: number;
  width: number;
  height: number;
  viewportWidth: number;
  viewportHeight: number;
  margin?: number;
}

// Places the card at the click, flipping it back over the click point on overflow,
// then clamping into the viewport. Each axis independently.
// eslint-disable-next-line react-refresh/only-export-components
export function menuPosition(input: MenuPositionInput): {
  left: number;
  top: number;
} {
  const margin = input.margin ?? 6;
  const place = (at: number, size: number, viewport: number): number => {
    const flipped = at + size + margin > viewport ? at - size : at;
    return Math.max(margin, Math.min(flipped, viewport - size - margin));
  };
  return {
    left: place(input.x, input.width, input.viewportWidth),
    top: place(input.y, input.height, input.viewportHeight),
  };
}

export function Menu({
  x,
  y,
  items,
  onClose,
}: {
  x: number;
  y: number;
  items: readonly MenuItem[];
  onClose: () => void;
}): ReactNode {
  const cardRef = useRef<HTMLDivElement>(null);
  const idPrefix = useId();
  const domIdFor = (index: number): string => `${idPrefix}item-${index}`;

  const [highlighted, setHighlighted] = useState<number | null>(() =>
    stepMenuIndex(items, null, 1),
  );

  const [pos, setPos] = useState<{ left: number; top: number } | null>(null);
  useLayoutEffect(() => {
    const card = cardRef.current;
    if (!card) return;
    const rect = card.getBoundingClientRect();
    setPos(
      menuPosition({
        x,
        y,
        width: rect.width,
        height: rect.height,
        viewportWidth: window.innerWidth,
        viewportHeight: window.innerHeight,
      }),
    );
  }, [x, y, items.length]);

  // Holding focus is what keeps Escape from also reaching the tree's own handler.
  // Not rAF: rAF never fires in the hidden tab the browser-verification harness uses.
  useEffect(() => {
    cardRef.current?.focus();
  }, []);

  const invoke = (index: number): void => {
    const item = items[index];
    if (!item || item.disabled) return;
    // onSelect before onClose: React runs every effect cleanup before any create, so a
    // rename box mounted by onSelect focuses itself after Backdrop's focus restore.
    item.onSelect();
    onClose();
  };

  const onKeyDown = (ev: React.KeyboardEvent<HTMLDivElement>): void => {
    switch (ev.key) {
      case "ArrowDown":
      case "ArrowUp": {
        ev.preventDefault();
        setHighlighted((from) =>
          stepMenuIndex(items, from, ev.key === "ArrowDown" ? 1 : -1),
        );
        break;
      }
      case "Home":
      case "End": {
        ev.preventDefault();
        setHighlighted(stepMenuIndex(items, null, ev.key === "Home" ? 1 : -1));
        break;
      }
      case "Enter":
      case " ": {
        ev.preventDefault();
        if (highlighted !== null) invoke(highlighted);
        break;
      }
      case "Escape": {
        ev.preventDefault();
        ev.stopPropagation();
        onClose();
        break;
      }
    }
  };

  return (
    <Backdrop transparent onClose={onClose}>
      <div
        ref={cardRef}
        className="menu"
        role="menu"
        aria-activedescendant={
          highlighted !== null ? domIdFor(highlighted) : undefined
        }
        tabIndex={-1}
        style={{ outline: "none", left: pos?.left ?? x, top: pos?.top ?? y }}
        onKeyDown={onKeyDown}
        onClick={(ev) => ev.stopPropagation()}
        // A <button> takes focus on mousedown, which would move it off the card.
        onMouseDown={(ev) => ev.preventDefault()}
      >
        {items.map((item, index) => (
          <Fragment key={item.label}>
            {item.separatorBefore ? (
              <div className="menusep" role="separator" />
            ) : null}
            <button
              type="button"
              id={domIdFor(index)}
              role="menuitem"
              tabIndex={-1}
              className={clsx(
                "menuitem",
                index === highlighted && "hl",
                item.danger && "danger",
              )}
              aria-disabled={item.disabled ? "true" : undefined}
              onMouseEnter={() => {
                if (!item.disabled) setHighlighted(index);
              }}
              onClick={() => invoke(index)}
            >
              {item.label}
            </button>
          </Fragment>
        ))}
      </div>
    </Backdrop>
  );
}
