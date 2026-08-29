// Merged into the esbuild build options for :app (see BUILD.bazel's `config`
// attr) — the only thing not expressible via a plain rule attribute: monaco's
// codicon.css references codicon.ttf via a plain url(...), and esbuild has no
// loader for binary files by default (mirrors the .woff2 fix in
// //ui/src/theme/esbuild.config.mjs).
//
// "dataurl", not "file", for the same reason as there: a "file" loader's
// content-hashed sibling cannot be declared as a bazel output and is discarded,
// which would leave every codicon (the folding chevrons, the suggest-widget
// kind icons) as a missing glyph.
export default {
  loader: {
    ".ttf": "dataurl",
  },
};
