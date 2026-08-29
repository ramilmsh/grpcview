// Imported for side effects from main.tsx before any editor mounts: points
// @monaco-editor/react at the bundled monaco. Each worker is its own esbuild entry
// point (see ui/BUILD.bazel) built to a fixed filename and served from "/", so
// getWorkerUrl just names the file — no CDN, no inlining.
import { loader } from "@monaco-editor/react";
import * as monaco from "monaco-editor";

self.MonacoEnvironment = {
  getWorkerUrl(_moduleId: string, label: string) {
    if (label === "typescript" || label === "javascript")
      return "/ts.worker.js";
    if (label === "json") return "/json.worker.js";
    return "/editor.worker.js";
  },
};

loader.config({ monaco });

export const NOCTURNE_MONACO_THEME = "nocturne";

monaco.editor.defineTheme(NOCTURNE_MONACO_THEME, {
  base: "vs-dark",
  inherit: true,
  rules: [
    { token: "", foreground: "cfd3e5", background: "1b1d2b" },
    { token: "string.key.json", foreground: "b9b2ee" },
    { token: "string.value.json", foreground: "c9cbd6" },
    { token: "string", foreground: "c9cbd6" },
    { token: "number", foreground: "c7bdf7" },
    { token: "keyword", foreground: "b9b2ee" },
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
    focusBorder: "#00000000",
  },
});
