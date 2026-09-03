// Intentionally-empty stub. @bufbuild/protoplugin's dist/{cjs,esm}/transpile.js unconditionally
// require()s "typescript" at module load, even though transpile() itself is only called for the
// target=js/transpileJs and .d.ts-stripping code paths — dead for this repo's
// target=ts,json_types=true codegen request. A real dependency isn't needed; this only has to
// resolve.
module.exports = {};
