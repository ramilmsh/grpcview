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
  // Every ts_project here emits with no_emit=False, so bazel-bin holds both a
  // compiled .js sibling AND rules_ts's own colocated copy of the raw
  // .ts/.tsx source, for every source file. An extensionless specifier
  // ("./ui-store", or a "@/..." alias esbuild resolves itself via tsconfig
  // `paths`) is therefore ambiguous -- esbuild's default resolveExtensions
  // tries ".tsx"/".ts" before ".js", so which one wins can vary by import
  // style (relative vs alias) and resolver codepath. A module reachable both
  // ways -- ui-store.ts via "@/lib/ui-store" everywhere but "./ui-store" from
  // its sibling workspace-query.ts, the case that surfaced this -- ends up
  // bundled twice: once from each source, each with its own top-level state
  // (two zustand `create()` calls, two unrelated store instances). Dropping
  // .tsx/.ts/.jsx here (the explicitly-named entry_point doesn't need them --
  // loader selection is by literal file extension, not resolveExtensions)
  // pins every extensionless specifier to the compiled .js sibling, closing
  // the whole class rather than just the one call site that got caught.
  resolveExtensions: [".js", ".css", ".json"],
};
