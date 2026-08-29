// esbuild entry point for the standalone editor.worker.js build — see ui/BUILD.bazel.
// A dedicated source file so the worker bundle's entry_point is an ordinary,
// gazelle-tracked source label rather than a path reaching into node_modules.
import "monaco-editor/esm/vs/editor/editor.worker";
