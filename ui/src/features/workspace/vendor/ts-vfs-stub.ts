// Build-time stub for `@typescript/vfs` (ts-request-body-plan §T2, risk #7).
//
// protoplugin's transpile.js statically imports these three named exports from
// `@typescript/vfs`, which is the only thing in the protoc-gen-es import chain that
// touches Node `fs`/`os`. We never run the transpile path (`target=ts` emits source
// directly), so aliasing this away removes the last latent fs reference from the
// browser bundle. The three exports must exist (they are imported by name) but are
// never invoked — no-ops. See ts-stub.ts for the companion `typescript` stub.
export const createDefaultMapFromNodeModules = () => new Map();
export const createSystem = () => ({});
export const createVirtualCompilerHost = () => ({ compilerHost: {} });
