import { useEffect, useRef, useState, type CSSProperties } from "react";
import clsx from "clsx";

// EditableName renders a display name that swaps to a text input while editing.
// It is controlled: the parent owns the `editing` flag (so it can be triggered by
// a click on the text or by a separate affordance like a rename button) and gets
// the committed value via onCommit. Enter/blur commit; Escape cancels. A commit
// that is blank or unchanged is ignored — the parent still leaves edit mode.
export function EditableName({
  value,
  editing,
  onEditingChange,
  onCommit,
  activateOnClick,
  className,
  style,
  inputStyle,
  title,
  ariaLabel,
}: {
  value: string;
  editing: boolean;
  onEditingChange: (editing: boolean) => void;
  onCommit: (next: string) => void;
  // When set, clicking the text enters edit mode (used where the text isn't also
  // a row-select target). Otherwise editing is triggered externally.
  activateOnClick?: boolean;
  className?: string;
  style?: CSSProperties;
  inputStyle?: CSSProperties;
  title?: string;
  ariaLabel?: string;
}) {
  const [text, setText] = useState(value);
  const ref = useRef<HTMLInputElement>(null);

  // Seed from the current value on entering edit mode, then focus + select.
  useEffect(() => {
    if (!editing) return;
    setText(value);
    const id = requestAnimationFrame(() => {
      ref.current?.focus();
      ref.current?.select();
    });
    return () => cancelAnimationFrame(id);
    // Intentionally keyed on `editing` only: reseeding on `value` changes mid-edit
    // would clobber what the user is typing.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [editing]);

  const commit = () => {
    const next = text.trim();
    if (next && next !== value) onCommit(next);
    onEditingChange(false);
  };

  if (!editing) {
    return (
      <span
        className={className}
        style={activateOnClick ? { cursor: "text", ...style } : style}
        title={title}
        onClick={
          activateOnClick
            ? (e) => {
                e.stopPropagation();
                onEditingChange(true);
              }
            : undefined
        }
      >
        {value}
      </span>
    );
  }

  return (
    <input
      ref={ref}
      className={clsx("bare", className)}
      style={inputStyle ?? style}
      value={text}
      aria-label={ariaLabel}
      onChange={(e) => setText(e.target.value)}
      onClick={(e) => e.stopPropagation()}
      onKeyDown={(e) => {
        if (e.key === "Enter") {
          e.preventDefault();
          commit();
        } else if (e.key === "Escape") {
          e.preventDefault();
          onEditingChange(false);
        }
      }}
      onBlur={commit}
    />
  );
}
