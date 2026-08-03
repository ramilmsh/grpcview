// Build-time stub vite.config.ts aliases the `typescript` specifier to, dropping ~9.6MB.
// @bufbuild/protoplugin statically imports `typescript` for its `.d.ts` transpile path, which
// the `target=ts` path this app uses never reaches. Monaco's own TS worker is bundled
// separately and does not go through this alias.
export default {};
