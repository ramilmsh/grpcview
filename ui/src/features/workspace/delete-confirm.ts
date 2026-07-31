// The exact wording for CollectionPanel's delete-confirm dialog — pulled out
// into its OWN file (not left inline in CollectionPanel.tsx, where it was
// first written) for a reason beyond style: CollectionPanel.tsx transitively
// imports FolderMetadataDialog.tsx -> MetadataEditor.tsx -> `monaco-editor` /
// `@monaco-editor/react`, and under this repo's Bazel-sandboxed vitest run,
// resolving `monaco-editor` from a test's import graph fails outright
// ("Failed to resolve entry for package 'monaco-editor'" — verified by
// actually trying it: a CollectionPanel.test.ts importing `deleteConfirmCopy`
// straight from ./CollectionPanel failed the WHOLE suite with exactly that
// error, purely from evaluating CollectionPanel.tsx's module graph, nothing
// to do with this function's own logic). Per tree-rewrite-plan.md's T2 line
// ("Make delete multi-aware (confirm dialog pluralizes)") this needs to be
// unit-testable — "extract the message builder as a pure function if that is
// what makes it testable" was the brief's own instruction, and this file is
// what that turned out to require: not just "a named function", but a
// module with NO transitive dependency on Monaco (or React, or anything else
// CollectionPanel.tsx pulls in) at all. `format.ts`'s own header ("Pure,
// framework-free helpers") is the model this file follows, but it stays
// beside CollectionPanel rather than moving into lib/format.ts itself: this
// wording is delete-dialog-specific presentation copy, not a reusable
// ItemWithPath-tree primitive the way pruneNestedSelections (format.ts) is.
import type { ItemWithPath } from "@/lib/format";

// `items` is the ALREADY-PRUNED selection (lib/format.ts's
// pruneNestedSelections) — see CollectionPanel.tsx's onTreeDelete for why:
// the count and folder/request mix reported here must match what doDelete
// will actually fire one mutation per, not the raw (possibly
// ancestor-and-descendant-overlapping) selection the tree handed over.
//
// Returns the THREE pieces CollectionPanel's JSX needs, rather than a
// pre-built string or ReactNode (this file imports no React, per the header
// above): `emphasis` is the one bolded span (today: the single item's own
// name; N>1: "N folders"/"N requests"/"N items", by composition) and
// `suffix` is the plain text immediately after it. This keeps ONE JSX shape —
// `Delete <strong>{emphasis}</strong>{suffix}` — for every case, 1 item or N,
// rather than branching the markup itself on count.
export interface DeleteConfirmCopy {
  title: string;
  emphasis: string;
  suffix: string;
}

export function deleteConfirmCopy(items: readonly ItemWithPath[]): DeleteConfirmCopy {
  if (items.length === 0) {
    // Nothing to describe. Not reachable through the UI — the dialog is gated on
    // `open={confirm.length > 0}` (CollectionPanel.tsx) and Dialog.tsx returns
    // null when closed, so this copy is never rendered — but React still
    // evaluates this call on every CollectionPanel render, including the ones
    // where `confirm` is empty, so the function must return something rather
    // than throw. It used to fall through into the plural branch below, where
    // folderCount === items.length === 0 made `allFolders` vacuously true and
    // produced "Delete 0 folders … and everything inside them?" — a sentence
    // that asserts a count AND a kind AND a recursive consequence, none of which
    // are true of an empty list. Anyone who later makes this branch reachable
    // (or reads it while debugging) deserves copy that is merely useless rather
    // than actively wrong, so the empty case says nothing it cannot back up.
    // Kept as an early return rather than narrowing the parameter to a
    // non-empty-tuple type: the only caller has a plain ItemWithPath[] in state
    // and would need a cast plus a null-copy branch in its JSX to satisfy that
    // signature, which pushes complexity into the caller to delete one branch
    // here.
    return { title: "Delete", emphasis: "nothing", suffix: "?" };
  }

  if (items.length === 1) {
    // UNCHANGED wording from before this phase: names the one item, and only
    // a FOLDER gets the "and everything inside it" warning — a request has
    // nothing "inside" it to warn about.
    const [item] = items;
    const folder = item.item.content.case === "folder";
    return {
      title: folder ? "Delete folder" : "Delete request",
      emphasis: item.item.name,
      suffix: folder ? " and everything inside it?" : "?",
    };
  }

  // N > 1 — this phase's actual new case. Genuinely N > 1 by the time control
  // reaches here: both n===0 and n===1 returned above, so `allFolders` and
  // `noFolders` below are real statements about a real batch rather than the
  // vacuous truths an empty list used to make them.
  const folderCount = items.filter((it) => it.item.content.case === "folder").length;
  const allFolders = folderCount === items.length;
  const noFolders = folderCount === 0;
  const noun = allFolders ? "folders" : noFolders ? "requests" : "items";
  const nounPhrase = `${items.length} ${noun}`;
  return {
    title: `Delete ${nounPhrase}`,
    emphasis: nounPhrase,
    // Only a batch that includes at least one folder needs the "everything
    // inside" warning — mirrors the single-item rule above (folder -> warn,
    // request -> don't), just decided over the WHOLE selection instead of one
    // item. A bare "and everything inside them?" on a MIXED batch would read
    // as if every selected row were a folder, which actively misleads for
    // e.g. 1 folder + 4 requests — so a mixed batch gets a second, explicit
    // sentence instead, still inside the same suffix string (no new markup:
    // the dialog-body <p> already tolerates more than one sentence of plain
    // text).
    suffix: allFolders
      ? " and everything inside them?"
      : noFolders
        ? "?"
        : "? Folders in the selection will be deleted along with everything inside them.",
  };
}
