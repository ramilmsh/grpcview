// Merged into the esbuild build options for :theme_css (see BUILD.bazel's
// `config` attr) — the only thing not expressible via a plain rule attribute:
// vendored font files are referenced by fonts.css's plain url(...), and esbuild
// has no loader for binary files by default.
//
// "dataurl", not "file": the "file" loader emits a content-hashed sibling
// (inter-latin-wght-normal-NRMW37G5.woff2) whose name is not knowable at
// analysis time, so it cannot be declared as a rule output and bazel throws it
// away — leaving main.css pointing at a 404 and the app on fallback fonts.
// Inlining costs ~33% on ~88KB of woff2, in a bundle that is embedded into the
// Go binary and served from memory anyway.
export default {
  loader: {
    ".woff2": "dataurl",
  },
};
