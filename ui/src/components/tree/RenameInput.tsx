import { useEffect, useRef, useState, type ReactNode } from "react";
import clsx from "clsx";

// "collision" means the value equals a visible sibling's label: Enter refuses and
// stays in the box, blur cancels.
export type RenameVerdict = "commit" | "cancel" | "collision";

export function renameVerdict(
  value: string,
  current: string,
  siblings: readonly string[]
): RenameVerdict {
  const next = value.trim();
  if (!next || next === current) return "cancel";
  return siblings.includes(next) ? "collision" : "commit";
}

interface RenameInputProps {
  current: string;
  siblings: readonly string[];
  // The caller is what leaves rename mode; this component never assumes it did.
  onCommit: (next: string) => void;
  onCancel: () => void;
  ariaLabel?: string;
}

export function RenameInput({
  current,
  siblings,
  onCommit,
  onCancel,
  ariaLabel,
}: RenameInputProps): ReactNode {
  const [text, setText] = useState(current);
  const ref = useRef<HTMLInputElement>(null);

  // Not requestAnimationFrame: the browser-verification harness runs its tab
  // hidden, where rAF never fires.
  useEffect(() => {
    ref.current?.focus();
    ref.current?.select();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const verdict = renameVerdict(text, current, siblings);
  const colliding = verdict === "collision";

  return (
    <input
      ref={ref}
      className={clsx("bare", colliding && "rename-invalid")}
      style={{ flex: 1, minWidth: 0 }}
      value={text}
      aria-label={ariaLabel}
      aria-invalid={colliding ? "true" : undefined}
      onChange={(e) => setText(e.target.value)}
      onKeyDown={(e) => {
        // preventDefault on both: Enter would submit an enclosing form, and
        // Escape natively reverts the input in some engines. No stopPropagation
        // needed — Tree.tsx's container handler bails on an editable target.
        if (e.key === "Enter") {
          e.preventDefault();
          if (verdict === "commit") onCommit(text.trim());
          else if (verdict === "cancel") onCancel();
        } else if (e.key === "Escape") {
          e.preventDefault();
          onCancel();
        }
      }}
      onBlur={() => {
        if (verdict === "commit") onCommit(text.trim());
        else onCancel();
      }}
    />
  );
}
