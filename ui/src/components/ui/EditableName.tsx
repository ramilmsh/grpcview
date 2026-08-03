import { useEffect, useRef, useState, type CSSProperties } from "react";
import clsx from "clsx";

// A display name that swaps to a text input while editing. Enter/blur commit,
// Escape cancels; a blank or unchanged commit is ignored but still exits edit mode.
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
  activateOnClick?: boolean;
  className?: string;
  style?: CSSProperties;
  inputStyle?: CSSProperties;
  title?: string;
  ariaLabel?: string;
}) {
  const [text, setText] = useState(value);
  const ref = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!editing) return;
    setText(value);
    const id = requestAnimationFrame(() => {
      ref.current?.focus();
      ref.current?.select();
    });
    return () => cancelAnimationFrame(id);
    // Keyed on `editing` only: reseeding on `value` mid-edit would clobber typing.
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
