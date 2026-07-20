// Monaco: make it offline-safe, then theme it. Imported for side effects from
// main.tsx BEFORE anything mounts an editor.
//
// Offline-safe: @monaco-editor/react loads Monaco (and its language workers)
// from a CDN by default. We instead point its `loader` at the bundled
// `monaco-editor` and construct the workers from Vite `?worker&inline` imports —
// `&inline` embeds each worker as a base64 blob, so nothing is fetched at
// runtime and it survives the vite-plugin-singlefile release bundle.
import { loader } from "@monaco-editor/react";
import * as monaco from "monaco-editor";
import EditorWorker from "monaco-editor/esm/vs/editor/editor.worker?worker&inline";
import JsonWorker from "monaco-editor/esm/vs/language/json/json.worker?worker&inline";

self.MonacoEnvironment = {
  getWorker(_workerId: string, label: string) {
    if (label === "json") return new JsonWorker();
    return new EditorWorker();
  },
};

loader.config({ monaco });

// Nocturne editor theme — a dark theme tuned to the panel ground and the
// blurple accent, shared by the request editor and the response viewer.
export const NOCTURNE_MONACO_THEME = "nocturne";

monaco.editor.defineTheme(NOCTURNE_MONACO_THEME, {
  base: "vs-dark",
  inherit: true,
  rules: [
    { token: "", foreground: "cfd3e5", background: "1b1d2b" },
    { token: "string.key.json", foreground: "b9b2ee" }, // keys — accent-ish
    { token: "string.value.json", foreground: "c9cbd6" }, // string values
    { token: "string", foreground: "c9cbd6" },
    { token: "number", foreground: "c7bdf7" },
    { token: "keyword", foreground: "b9b2ee" }, // true/false/null
    { token: "delimiter", foreground: "75798c" },
    { token: "comment", foreground: "75798c", fontStyle: "italic" },
  ],
  colors: {
    "editor.background": "#1b1d2b",
    "editor.foreground": "#cfd3e5",
    "editorLineNumber.foreground": "#595d6c",
    "editorLineNumber.activeForeground": "#9397ab",
    "editorCursor.foreground": "#9184d9",
    "editor.selectionBackground": "#2b2741",
    "editor.inactiveSelectionBackground": "#232532",
    "editor.lineHighlightBackground": "#1f2130",
    "editorIndentGuide.background1": "#292b31",
    "editorIndentGuide.activeBackground1": "#3f424d",
    "editorWidget.background": "#232532",
    "editorWidget.border": "#3f424d",
    "editorSuggestWidget.background": "#232532",
    "editorSuggestWidget.selectedBackground": "#2b2741",
    "editorGutter.background": "#161826",
    "editorError.foreground": "#d1737d",
    "editorWarning.foreground": "#d9b46a",
    "scrollbarSlider.background": "#3a3d4d80",
    "scrollbarSlider.hoverBackground": "#4a4e60aa",
    "scrollbarSlider.activeBackground": "#4a4e60",
    "menu.background": "#232532",
    "menu.foreground": "#cfd3e5",
    "input.background": "#1f2130",
    "focusBorder": "#00000000",
  },
});
