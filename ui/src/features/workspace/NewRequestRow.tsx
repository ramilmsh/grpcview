import { useEffect, useRef } from "react";
import clsx from "clsx";
import { DraftKindTag } from "@/components/ui/Tag";
import { collisionMessage } from "./new-request-draft";
import { useRequestDraft } from "./useRequestDraft";

// The draft's placeholder row CONTENT (the shell is the tree's): its kind slot and an
// inline name box styled like the tree's rename box. Unlike a rename, blur keeps the
// draft: only Enter (create), Escape or the page's Cancel end it.
export function NewRequestRow() {
  const { draft, folder, collides, kind, ready, cancel, setName, submit } =
    useRequestDraft();
  const ref = useRef<HTMLInputElement>(null);
  const seq = draft?.seq;

  // On mount and on every re-trigger (seq): focus() also scrolls the row into view.
  // Not requestAnimationFrame, for the reason RenameInput gives.
  useEffect(() => {
    ref.current?.focus();
    ref.current?.scrollIntoView({ block: "nearest" });
  }, [seq]);

  if (!draft) return null;

  return (
    <>
      <DraftKindTag kind={kind} />
      <span
        style={{ position: "relative", flex: 1, minWidth: 0, display: "flex" }}
      >
        <input
          ref={ref}
          className={clsx("bare", collides && "rename-invalid")}
          style={{ flex: 1, minWidth: 0 }}
          value={draft.name}
          placeholder="Request name"
          aria-label="New request name"
          aria-invalid={collides ? "true" : undefined}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => {
            // No stopPropagation needed: Tree.tsx's container handler bails on an
            // editable target.
            if (e.key === "Enter") {
              e.preventDefault();
              if (ready) void submit();
            } else if (e.key === "Escape") {
              e.preventDefault();
              cancel();
            }
          }}
        />
        {collides && (
          <div
            role="alert"
            style={{
              position: "absolute",
              top: "calc(100% + 6px)",
              left: 0,
              zIndex: 30,
              width: "max-content",
              maxWidth: 220,
              padding: "6px 9px",
              borderRadius: 6,
              fontSize: 12,
              lineHeight: 1.4,
              whiteSpace: "normal",
              cursor: "default",
              color: "var(--err-fg)",
              background: "color-mix(in srgb, var(--err) 22%, var(--panel-2))",
              border: "1px solid var(--err-border)",
              boxShadow: "var(--shadow-md)",
            }}
          >
            {collisionMessage(draft.name, folder)}
          </div>
        )}
      </span>
    </>
  );
}
