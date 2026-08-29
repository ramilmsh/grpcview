import type * as Monaco from "monaco-editor";

// Verification hook: app code only writes this map. There is no single global
// `monaco`, so each editor registers itself under its model URI.
declare global {
  interface Window {
    __grpcviewEditors?: Record<string, Monaco.editor.IStandaloneCodeEditor>;
  }
}

export function registerEditorForDebug(
  uri: string,
  editor: Monaco.editor.IStandaloneCodeEditor,
): void {
  (window.__grpcviewEditors ??= {})[uri] = editor;
}
