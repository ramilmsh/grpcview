// Imported for side effects from main.tsx before any editor mounts: points
// @monaco-editor/react at the bundled monaco. Each worker is its own esbuild entry
// point (see ui/BUILD.bazel) built to a fixed filename and served from "/", so
// getWorkerUrl just names the file — no CDN, no inlining.
import { loader } from "@monaco-editor/react";
import * as monaco from "monaco-editor";
// Vendored from monaco-themes (MIT, https://github.com/brijeshb42/monaco-themes)
// instead of imported from the package: its published "exports" map doesn't
// expose the themes/*.json subpaths the README's usage example relies on.
import theme from "./theme.json";

self.MonacoEnvironment = {
  getWorkerUrl(_moduleId: string, label: string) {
    if (label === "typescript" || label === "javascript")
      return "/ts.worker.js";
    if (label === "json") return "/json.worker.js";
    return "/editor.worker.js";
  },
};

loader.config({ monaco });

export const MONACO_THEME = "theme";

monaco.editor.defineTheme(
  MONACO_THEME,
  theme as monaco.editor.IStandaloneThemeData,
);
