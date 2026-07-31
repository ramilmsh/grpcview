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

// A popup menu, positioned at a point in the viewport — the UI behind the
// collection tree's right-click menu (docs/design/tree-rewrite-plan.md's T5).
//
// It lives in components/ui/, beside Dialog/Backdrop, NOT in components/tree/.
// Enduring decision 1 says the tree component knows nothing about gRPC, and the
// same argument applies one level up: a menu is not tree-specific either, and
// the *items* this menu will carry (New Request, Folder metadata) are entirely
// gRPC-shaped. So the tree keeps its `onContextMenu(nodes, ev)` contract and the
// HOST renders this — which is also the plan's §Risks "Scope creep into the row
// renderer" boundary, honoured at the phase the plan predicted would test it.
//
// No new npm dependency (enduring decision 6 caps that at T5 explicitly): the
// keyboard model, the flip math and the outside-click dismissal are each a
// handful of lines, and Backdrop already owns the focus save/restore.

export interface MenuItem {
  label: string;
  onSelect: () => void;
  // The destructive item. Styled like `.rowbtn.danger` (app-tokens.css): neutral
  // at rest, `--err`/`--err-bg` only once highlighted, so a menu does not sit
  // there permanently shouting one red row at the user.
  danger?: boolean;
  // Rendered, announced, and skipped by every navigation key — never omitted.
  // `aria-disabled`, not the native `disabled` attribute: a disabled <button> is
  // removed from the tab/hit-test model entirely, which also kills the
  // onMouseEnter that drives the highlight, so the row would go quietly inert
  // instead of visibly unavailable.
  disabled?: boolean;
  // A divider immediately above this item. A property of the item rather than a
  // separate array entry so the items array stays a flat list of *actions* —
  // which is what stepMenuIndex below indexes into, and what a caller builds
  // conditionally (an omitted item must not leave a stray separator behind).
  separatorBefore?: boolean;
}

// ── pure logic, exported for the DOM-free suite ──────────────────────────────
// vitest runs `environment: "node"` with no jsdom (vitest.config.ts), so
// anything worth testing has to be reachable without rendering. Same reason
// Tree.tsx exports isEditableTarget and RenameInput.tsx exports renameVerdict.

// Highlight navigation: from `from`, move `delta` items, skipping disabled ones,
// wrapping at both ends. Returns null only when NO item is selectable at all.
//
// `from === null` means "no item highlighted yet", and is deliberately treated
// as a virtual position just outside the array — before index 0 going forward,
// past the end going backward — which is the same rule Tree.tsx's `move` intent
// uses for an unfocused tree (plan §"What T1 settled" #4). That is what makes
// Home/End fall out for free: the first enabled item is stepMenuIndex(items,
// null, 1) and the last is stepMenuIndex(items, null, -1), so there is one
// stepper rather than a stepper plus two edge-finders that could disagree about
// what "enabled" means.
export function stepMenuIndex(
  items: readonly Pick<MenuItem, "disabled">[],
  from: number | null,
  delta: 1 | -1
): number | null {
  const n = items.length;
  if (n === 0) return null;
  const start = from ?? (delta === 1 ? -1 : n);
  // At most one full lap: every index is visited exactly once, so a menu whose
  // every item is disabled terminates with null instead of spinning.
  for (let step = 1; step <= n; step++) {
    const index = (((start + delta * step) % n) + n) % n;
    if (!items[index].disabled) return index;
  }
  return null;
}

export interface MenuPositionInput {
  // The click, in viewport coordinates (clientX/clientY).
  x: number;
  y: number;
  // The MEASURED card, not an estimate — the caller reads it off the rendered
  // element (see the layout effect below), because item count and label lengths
  // decide both numbers and neither is knowable up front.
  width: number;
  height: number;
  viewportWidth: number;
  viewportHeight: number;
  margin?: number;
}

// Where to actually put the card: at the click, flipped back over the click
// point when it would overflow, then clamped into the viewport. A menu hanging
// off the bottom of a 278px sidebar is the NORMAL case here, not an edge case —
// the collection panel's rows run to the bottom of the window.
//
// Flip-then-clamp, in that order, and both axes independently (a menu can flip
// up without flipping left). The clamp is what handles the degenerate case the
// flip cannot: a card taller than the viewport has no non-overflowing position
// at all, so it is pinned at the top margin and simply clips at the bottom
// rather than being flipped to somewhere even worse.
export function menuPosition(input: MenuPositionInput): { left: number; top: number } {
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

// ── the component ───────────────────────────────────────────────────────────

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

  // The highlighted item — the menu's equivalent of the tree's `focused`, and
  // like it a logical cursor rather than real DOM focus (see the aria note
  // below). Starts on the first ENABLED item so Enter is immediately meaningful
  // for a keyboard user, matching how VS Code's context menu opens.
  const [highlighted, setHighlighted] = useState<number | null>(() =>
    stepMenuIndex(items, null, 1)
  );

  // Measured placement. Null until the layout effect has run, which is also the
  // one render where the card is at the raw click point; useLayoutEffect (not
  // useEffect) is what keeps that intermediate position from ever being painted.
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
      })
    );
  }, [x, y, items.length]);

  // Take DOM focus, and this is load-bearing rather than an a11y nicety. Escape
  // is handled by Backdrop's WINDOW keydown listener, and the collection tree's
  // own onKeyDown also binds Escape ("clear the selection") — so a menu that did
  // not hold focus would let one Escape hit both listeners and wipe the very
  // selection the menu was opened to act on. That is verbatim the defect
  // review caught for Dialog (plan §"The review T2 was owed" #1), fixed there
  // the same way: with focus inside the card, the keydown's bubble path simply
  // does not include the tree. Backdrop hands focus back to the opener (the
  // `.tree` div, which a right-click has already focused) when this unmounts, so
  // the tree stays keyboard-drivable afterwards.
  //
  // A plain effect, deliberately NOT requestAnimationFrame: the browser-
  // verification harness runs its tab hidden, where rAF never fires (plan
  // §"Verification recipe"), which would make this focus both unverifiable and
  // genuinely absent there. Same call Dialog.tsx and RenameInput.tsx make.
  useEffect(() => {
    cardRef.current?.focus();
  }, []);

  const invoke = (index: number): void => {
    const item = items[index];
    if (!item || item.disabled) return;
    // Selection first, dismissal second, both inside one React batch. The order
    // matters for the rename item in particular: `onSelect` starts a rename, and
    // React runs every effect CLEANUP for a commit before any effect CREATE, so
    // Backdrop's focus-restore-to-`.tree` runs before the newly mounted rename
    // box focuses itself — the box wins, and it does not immediately blur-cancel.
    item.onSelect();
    onClose();
  };

  const onKeyDown = (ev: React.KeyboardEvent<HTMLDivElement>): void => {
    switch (ev.key) {
      case "ArrowDown":
      case "ArrowUp": {
        ev.preventDefault();
        setHighlighted((from) => stepMenuIndex(items, from, ev.key === "ArrowDown" ? 1 : -1));
        break;
      }
      case "Home":
      case "End": {
        ev.preventDefault();
        // Both are the stepper from the virtual out-of-array position — see
        // stepMenuIndex's own comment for why there is no separate edge-finder.
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
        // stopPropagation, so Backdrop's window listener does not ALSO fire and
        // call onClose a second time. Harmless if it did (the caller's state
        // setter is idempotent), but keeping exactly one close path per keypress
        // is what makes the tree's Escape and this one provably independent.
        // Backdrop's listener stays as the fallback for the case this handler
        // cannot see: focus somewhere outside the card entirely.
        ev.preventDefault();
        ev.stopPropagation();
        onClose();
        break;
      }
      // Every other key falls through untouched — a browser shortcut still
      // works with the menu open, exactly as Tree.tsx's handleKeyDown does for
      // an unclaimed key.
    }
  };

  return (
    // Transparent variant: a context menu dims nothing (see Backdrop's
    // `transparent` prop). Everything else about Backdrop is what this needs
    // verbatim — outside click to close, Escape, and the opener focus
    // save/restore — so none of it is forked here.
    <Backdrop transparent onClose={onClose}>
      <div
        ref={cardRef}
        className="menu"
        role="menu"
        // ONE focus container with aria-activedescendant, not per-item DOM
        // focus — the identical model Tree.tsx's FOCUS MODEL comment documents,
        // chosen here for the same reasons plus one specific to a menu: the card
        // must keep focus for the Escape isolation above to hold, and real
        // per-item focus would move it onto a child on every arrow press,
        // turning "did the menu still own focus" into a moving target.
        aria-activedescendant={highlighted !== null ? domIdFor(highlighted) : undefined}
        tabIndex={-1}
        // outline:none for the same reason Dialog's card sets it — this is a
        // fallback focus holder, not a control the user aimed at, so nocturne's
        // :focus-visible ring around the whole card would be noise.
        style={{ outline: "none", left: pos?.left ?? x, top: pos?.top ?? y }}
        onKeyDown={onKeyDown}
        // Keep the backdrop's own click-to-close from firing for a click that
        // landed INSIDE the menu (Dialog's card does the same).
        onClick={(ev) => ev.stopPropagation()}
        // Keeps DOM focus on the card when an item is clicked: a <button> takes
        // focus on mousedown, which would move it off the card mid-interaction.
        // VS Code's own context view suppresses mousedown for this reason.
        onMouseDown={(ev) => ev.preventDefault()}
      >
        {items.map((item, index) => (
          <Fragment key={item.label}>
            {item.separatorBefore ? <div className="menusep" role="separator" /> : null}
            <button
              type="button"
              id={domIdFor(index)}
              role="menuitem"
              tabIndex={-1}
              className={clsx(
                "menuitem",
                index === highlighted && "hl",
                item.danger && "danger"
              )}
              aria-disabled={item.disabled ? "true" : undefined}
              // Hover moves the same single highlight the keyboard moves, so
              // there is one cursor rather than a CSS :hover competing with a
              // keyboard highlight and both being visible at once.
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
