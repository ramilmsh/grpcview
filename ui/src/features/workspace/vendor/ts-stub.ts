// Build-time stub for the `typescript` package (ts-request-body-plan §T2, risk #7).
//
// `@bufbuild/protoc-gen-es` → `@bufbuild/protoplugin` STATICALLY imports `typescript`
// (via protoplugin's transpile.js) for its `import_extension`/`.d.ts` transpile path.
// We only ever call `protocGenEs.run(req)` with `target=ts`, which emits the generated
// `_pb.ts` source verbatim and NEVER reaches transpile.js — so the real ~10MB
// `typescript` module is dead weight in the singlefile bundle. Vite aliases the
// `typescript` specifier to this empty default (see vite.config.ts), dropping ~9.6MB.
// Proven inert on the `target=ts` path by the de-risk spike (a throw-on-access poison
// stub survived a real run()). Monaco's own TS worker is bundled separately and does
// NOT go through this alias, so IntelliSense is unaffected.
export default {};
