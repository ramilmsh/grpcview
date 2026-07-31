import { useEffect, useRef, useState, type ReactNode } from "react";
import clsx from "clsx";

// The tree's OWN inline rename box (T4b — docs/design/tree-rewrite-plan.md's
// "Rename in-tree": F2/pencil, inline input, collision validation, commit on
// Enter, cancel on Escape, blur-commits). Its own module rather than inline in
// TreeRow.tsx so that file stays layout-only, and deliberately NOT
// components/ui/EditableName: that component is shared with MethodHeader and
// ScriptsView and has neither collision validation nor the focus behavior below,
// and bolting both onto a component two unrelated call sites depend on is worse
// than a purpose-built box here. EditableName keeps its other two callers
// untouched; the tree simply no longer uses it.

// The whole commit/cancel/refuse decision, as one pure function so it is
// unit-testable with no DOM (vitest runs `environment: "node"` — see
// vitest.config.ts; same reason Tree.tsx exports isEditableTarget). Enter and
// blur consume the SAME verdict and differ only in what they do with
// "collision": Enter stays in the box so the user can fix the name, blur has
// nowhere to stay and cancels.
//   - "cancel"    → leave rename mode, call nothing. Covers blank (a rename to
//                   nothing is not a rename) and unchanged (the server would
//                   take it, but there is nothing to persist).
//   - "collision" → the value equals a VISIBLE sibling's label. Refused here as
//                   a UX affordance only: the store rejects a colliding rename
//                   server-side regardless (T4a's FailedPrecondition), and this
//                   check cannot see siblings a filter box has hidden.
//   - "commit"    → non-blank, changed, collision-free.
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
  // The row's CURRENT label — both the box's initial value and the "unchanged"
  // comparison. TreeRow reads it from adapter.getTreeItem(node).label, even for a
  // rich adapter (see that file's comment on the narrow exception).
  current: string;
  // Labels of this row's other VISIBLE siblings (Tree.tsx computes them off
  // flat.rows by parentId). Empty is legitimate — an only child.
  siblings: readonly string[];
  // Called with the trimmed, validated name. The caller is what leaves rename
  // mode; this component never assumes it did.
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

  // A plain effect, deliberately NOT requestAnimationFrame. The browser-
  // verification harness runs its tab with visibilityState "hidden" (plan
  // §"Verification recipe", "Two traps in the browser harness itself"), where
  // rAF never fires at all — so an rAF-deferred focus is both unverifiable
  // there AND genuinely absent, which is exactly the latent bug
  // EditableName.tsx:41 still has and Dialog.tsx already avoids the same way.
  // Mount-only: this component only exists while its row is renaming, so its
  // mount IS the start of the rename, and re-running on `current` changing
  // would fight the user's caret mid-edit.
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
      // The ring (app-tokens.css) plus a refused Enter is the whole collision
      // affordance for this phase — no tooltip, no error popover — so this is
      // the only thing announcing it to assistive tech.
      aria-invalid={colliding ? "true" : undefined}
      onChange={(e) => setText(e.target.value)}
      onKeyDown={(e) => {
        // Neither branch needs stopPropagation, and that is load-bearing rather
        // than an omission: Tree.tsx's container keydown handler bails on
        // isEditableTarget (its own comment explains why a target check beats a
        // renamingId check), so every OTHER key typed in here — Space, Delete,
        // arrows, Home/End, F2 — is already safe from being reinterpreted as a
        // tree intent. preventDefault is still needed on both: Enter would
        // otherwise submit an enclosing form, and Escape is a native "revert
        // this input" gesture in some engines.
        if (e.key === "Enter") {
          e.preventDefault();
          if (verdict === "commit") onCommit(text.trim());
          else if (verdict === "cancel") onCancel();
          // "collision": stay in the box, with the ring on, so the name can be
          // fixed rather than the edit being silently thrown away.
        } else if (e.key === "Escape") {
          e.preventDefault();
          onCancel();
        }
      }}
      // Blur-commits (the plan's T4b spec line), but never a value the server
      // would reject: an invalid value on blur cancels. A blur has nowhere to
      // "stay put", so unlike Enter it cannot hold the box open for a fix.
      onBlur={() => {
        if (verdict === "commit") onCommit(text.trim());
        else onCancel();
      }}
    />
  );
}
