import { useId, useRef, useState, type KeyboardEvent } from "react";
import clsx from "clsx";
import { CaretDown } from "@/components/ui/icons";
import { Input } from "./Input";

// filterOptions narrows options to those matching query, VS Code's quick-pick rule at its
// simplest: every whitespace-separated term has to appear somewhere, case-insensitively, so
// "health proto" finds "//proto/health:health_proto" without the user typing the path in
// order. Options that START with the query come first — a user who typed a package prefix
// means that package — and the input's own order (the server sorts) breaks every other tie.
//
// Exported for its unit test, and because it is the whole behavior worth testing: the rest
// of this file is focus and keyboard plumbing a node-environment test cannot exercise.
export function filterOptions(options: readonly string[], query: string): string[] {
  const q = query.trim().toLowerCase();
  if (q === "") return [...options];
  const terms = q.split(/\s+/);
  const hits = options.filter((option) => {
    const lower = option.toLowerCase();
    return terms.every((term) => lower.includes(term));
  });
  // A stable partition, not a sort: two prefix matches keep the order they arrived in.
  const prefix = hits.filter((o) => o.toLowerCase().startsWith(q));
  return prefix.concat(hits.filter((o) => !o.toLowerCase().startsWith(q)));
}

// Combobox is an editable selector: a text field that accepts ANYTHING, with a filtered
// list of known values under it. Editable is the point — the list is a shortcut, never the
// set of legal answers — so nothing here ever rejects, rewrites or completes what is typed.
//
// It is deliberately not <datalist>: that renders unstyled, chrome-specific popups and
// gives no control over a pending or empty state, both of which this needs (the options
// arrive from a `bazel query` that can take seconds).
export function Combobox({
  value,
  onChange,
  options,
  placeholder,
  loading,
  emptyHint,
  autoFocus,
  onSubmit,
  ariaLabel,
}: {
  value: string;
  onChange: (value: string) => void;
  options: readonly string[];
  placeholder?: string;
  // The options are still on their way; the field is usable meanwhile.
  loading?: boolean;
  // Shown in place of the list when there is nothing to offer — why, in the caller's words.
  emptyHint?: string;
  autoFocus?: boolean;
  // Enter with no option highlighted: the caller's submit, same as a plain input's.
  onSubmit?: () => void;
  ariaLabel?: string;
}) {
  const [open, setOpen] = useState(false);
  const [highlighted, setHighlighted] = useState<number | null>(null);
  const wrapRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const idPrefix = useId();
  const listId = `${idPrefix}list`;
  const optionId = (index: number): string => `${idPrefix}opt-${index}`;

  const matches = filterOptions(options, value);
  // An open popup always has something in it: options, the no-match note that stands in
  // for them, "Loading…", or the caller's hint. With none of those it stays shut rather
  // than flashing an empty card.
  const showList = open && (loading === true || options.length > 0 || !!emptyHint);

  const pick = (index: number): void => {
    const option = matches[index];
    if (option === undefined) return;
    onChange(option);
    setOpen(false);
    setHighlighted(null);
    inputRef.current?.focus();
  };

  const step = (delta: 1 | -1): void => {
    if (matches.length === 0) return;
    setOpen(true);
    setHighlighted((from) => {
      const start = from ?? (delta === 1 ? -1 : 0);
      return (((start + delta) % matches.length) + matches.length) % matches.length;
    });
  };

  const onKeyDown = (ev: KeyboardEvent<HTMLInputElement>): void => {
    switch (ev.key) {
      case "ArrowDown":
      case "ArrowUp":
        ev.preventDefault();
        step(ev.key === "ArrowDown" ? 1 : -1);
        break;
      case "Enter":
        if (open && highlighted !== null) {
          // Picking consumes the Enter: the next one submits, so a keyboard user never
          // adds a source by accident on the keystroke that chose its label.
          ev.preventDefault();
          pick(highlighted);
          break;
        }
        setOpen(false);
        onSubmit?.();
        break;
      case "Escape":
        if (open) {
          // Stopped here, or the Dialog's own Escape would close the whole form on the
          // keystroke the user meant for the list.
          ev.preventDefault();
          ev.stopPropagation();
          setOpen(false);
          setHighlighted(null);
        }
        break;
    }
  };

  return (
    <div
      ref={wrapRef}
      className="combo"
      // Focus leaving the whole widget closes the list; moving between the input and its
      // toggle does not. Blur rather than a document listener: this lives inside a modal
      // whose backdrop already owns outside clicks.
      onBlur={(ev) => {
        if (!wrapRef.current?.contains(ev.relatedTarget as Node | null)) {
          setOpen(false);
          setHighlighted(null);
        }
      }}
    >
      <div className="combo-field">
        <Input
          ref={inputRef}
          value={value}
          placeholder={placeholder}
          autoFocus={autoFocus}
          role="combobox"
          aria-expanded={showList}
          aria-controls={listId}
          aria-autocomplete="list"
          aria-label={ariaLabel}
          aria-activedescendant={
            showList && highlighted !== null ? optionId(highlighted) : undefined
          }
          onChange={(ev) => {
            onChange(ev.target.value);
            setOpen(true);
            setHighlighted(null);
          }}
          onFocus={() => setOpen(true)}
          onKeyDown={onKeyDown}
        />
        <button
          type="button"
          className="combo-caret"
          tabIndex={-1}
          aria-label={showList ? "Hide suggestions" : "Show suggestions"}
          // The input keeps focus: the caret is a disclosure, not a destination.
          onMouseDown={(ev) => ev.preventDefault()}
          onClick={() => {
            setOpen((was) => !was);
            inputRef.current?.focus();
          }}
        >
          <CaretDown size={13} />
        </button>
      </div>

      {showList && (
        <div className="combo-list" id={listId} role="listbox">
          {matches.map((option, index) => (
            <button
              type="button"
              key={option}
              id={optionId(index)}
              role="option"
              title={option}
              aria-selected={index === highlighted}
              tabIndex={-1}
              className={clsx("menuitem", index === highlighted && "hl")}
              onMouseEnter={() => setHighlighted(index)}
              onMouseDown={(ev) => ev.preventDefault()}
              onClick={() => pick(index)}
            >
              {option}
            </button>
          ))}
          {matches.length === 0 && (
            <div className="combo-note">
              {loading
                ? "Loading…"
                : // Two different empty states: nothing to offer at all (the caller
                  // explains why) versus a filter that excluded everything on offer.
                  options.length === 0
                  ? emptyHint
                  : `No suggestion matches “${value.trim()}” — it can still be typed in full.`}
            </div>
          )}
          {matches.length > 0 && loading && <div className="combo-note">Loading…</div>}
        </div>
      )}
    </div>
  );
}
