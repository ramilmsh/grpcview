// Registers the `typescript` completion provider for MODULE SPECIFIERS — the `@/…` and `#/…`
// paths inside an import's quotes. See module-specifier.ts's header for why the TS worker answers
// nothing there. Registered once at module scope, like module-auto-import.ts: providers are
// monaco globals, so a per-component registration would stack duplicates on every mount.
import * as monaco from "monaco-editor";
import type * as Monaco from "monaco-editor";
import { getAutoImportContext } from "./module-auto-import";
import {
  specifierCompletions,
  specifierPrefixAt,
  workspacePathForUri,
} from "./module-specifier";

monaco.languages.registerCompletionItemProvider("typescript", {
  // `"`/`'` open a specifier, `/` opens its next segment; `@`/`#` cover the sigil typed before
  // any slash exists to trigger on.
  triggerCharacters: ['"', "'", "/", "@", "#"],
  provideCompletionItems(model, position) {
    const lineUntilCursor = model.getValueInRange({
      startLineNumber: position.lineNumber,
      startColumn: 1,
      endLineNumber: position.lineNumber,
      endColumn: position.column,
    });
    const prefix = specifierPrefixAt(lineUntilCursor);
    if (!prefix) return { suggestions: [] };

    const { modules, collectionId } = getAutoImportContext();
    const currentPath = workspacePathForUri(model.uri.toString());
    const range = new monaco.Range(
      position.lineNumber,
      prefix.segmentOffset + 1,
      position.lineNumber,
      position.column
    );

    const suggestions: Monaco.languages.CompletionItem[] = specifierCompletions(
      prefix.typed,
      modules,
      collectionId,
      currentPath
    ).map((c) => ({
      label: c.insertText,
      kind:
        c.kind === "folder"
          ? monaco.languages.CompletionItemKind.Folder
          : monaco.languages.CompletionItemKind.File,
      insertText: c.insertText,
      range,
      detail: c.specifier,
      // A folder is not an answer on its own: re-open the widget so the next segment can be
      // picked without retyping the trigger.
      command:
        c.kind === "folder"
          ? { id: "editor.action.triggerSuggest", title: "Suggest" }
          : undefined,
    }));
    return { suggestions };
  },
});
