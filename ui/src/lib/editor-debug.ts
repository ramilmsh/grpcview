import type * as Monaco from "monaco-editor";

// Dev/verification hook. There is no single global `monaco` (several editor instances — body,
// metadata, scripts — coexist, each with its own model), so each request editor registers itself
// here by its model URI. A verification harness (or the console) can then read a model's exact
// value and hidden-wrapper geometry without reaching into React or guessing which `.view-lines`
// belongs to which editor:
//
//   window.__grpcviewEditors["file:///grpcview/request/body.ts"].getValue()
//
// App code only ever WRITES this map; nothing reads it, so it is inert in normal use.
declare global {
  interface Window {
    __grpcviewEditors?: Record<string, Monaco.editor.IStandaloneCodeEditor>;
  }
}

export function registerEditorForDebug(
  uri: string,
  editor: Monaco.editor.IStandaloneCodeEditor
): void {
  (window.__grpcviewEditors ??= {})[uri] = editor;
}
