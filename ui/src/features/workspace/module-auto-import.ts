// Registers a `typescript` completion provider that supplies ONLY auto-import candidates — names
// exported by other workspace modules or by the `grpcview:*` virtuals, that are not yet imported
// into the current buffer — leaving every other suggestion (in-scope identifiers, members,
// keywords) to monaco's own built-in provider (tsMode.js's SuggestAdapter). Registered once at
// module scope, like
// monaco-scripts.ts's typescriptDefaults setup: providers are global, so a per-component
// registration would stack up duplicates on every mount.
//
// See auto-import.ts's header for why this cannot be "call getCompletionsAtPosition with
// includeCompletionsForModuleExports and convert the resulting codeActions" as first sketched:
// the vendored ts.worker.js drops that option on the floor. What follows instead works off data
// already in the frontend (the workspace's module list + the `~/`/`@/` path-sigil mapping),
// never touching the TS worker.
import * as monaco from "monaco-editor";
import type * as Monaco from "monaco-editor";
import type { WorkspaceModule } from "@grpcview/v1/service_pb";
import { insertImportEdit, namesAlreadyInScope } from "./auto-import";
import { candidatesFrom } from "./resolve-imports";
import { specifierPrefixAt, workspacePathForUri } from "./module-specifier";

export interface AutoImportContext {
  modules: readonly WorkspaceModule[];
  collectionId: string | null | undefined;
}

// Written by useWorkspaceModuleTypes (gv-types.ts) whenever the workspace's module list or the
// active collection changes; read synchronously by the provider below. A plain module-level
// variable, not React state: the provider is a monaco-global singleton and cannot call a hook.
let context: AutoImportContext = { modules: [], collectionId: null };

export function setAutoImportContext(next: AutoImportContext): void {
  context = next;
}

// Read by module-specifier-complete.ts, which completes the specifier STRING off the same
// workspace module list this provider completes NAMES from.
export function getAutoImportContext(): AutoImportContext {
  return context;
}

monaco.languages.registerCompletionItemProvider("typescript", {
  provideCompletionItems(model, position) {
    // Inside an import's quotes the segment being typed is a path, not an identifier;
    // module-specifier-complete.ts owns that position.
    const lineUntilCursor = model.getValueInRange({
      startLineNumber: position.lineNumber,
      startColumn: 1,
      endLineNumber: position.lineNumber,
      endColumn: position.column,
    });
    if (specifierPrefixAt(lineUntilCursor)) return { suggestions: [] };

    const word = model.getWordUntilPosition(position);
    // Require a non-empty prefix: with none, every export in the workspace would qualify,
    // which is far noisier than what the built-in provider offers on an empty prefix.
    if (!word.word) return { suggestions: [] };

    const source = model.getValue();
    const inScope = namesAlreadyInScope(source);
    const currentPath = workspacePathForUri(model.uri.toString());
    const range = new monaco.Range(
      position.lineNumber,
      word.startColumn,
      position.lineNumber,
      word.endColumn,
    );

    const suggestions: Monaco.languages.CompletionItem[] = [];
    const seen = new Set<string>();
    // The same candidate index resolve-or-bail works off (resolve-imports.ts): every other
    // workspace module PLUS the `grpcview:*` virtuals. Those virtuals are also why the
    // empty-workspace case is not short-circuited above — `invoke`, `params`, `assert` and
    // `inherit` are importable with no workspace modules at all, and until they were listed here
    // they were reachable only through the TS worker's own quick fix, never while typing.
    for (const candidate of candidatesFrom(context, currentPath)) {
      const specifier = candidate.specifier;
      for (const name of candidate.names) {
        if (!name.startsWith(word.word) || inScope.has(name)) continue;
        const dedupeKey = `${name}\0${specifier}`;
        if (seen.has(dedupeKey)) continue;
        seen.add(dedupeKey);

        const edit = insertImportEdit(source, name, specifier);
        const insertPos = model.getPositionAt(edit.offset);
        suggestions.push({
          label: name,
          kind: monaco.languages.CompletionItemKind.Reference,
          insertText: name,
          range,
          detail: `Auto-import from "${specifier}"`,
          additionalTextEdits: [
            {
              range: monaco.Range.fromPositions(insertPos, insertPos),
              text: edit.insertText,
            },
          ],
        });
      }
    }
    return { suggestions };
  },
});
