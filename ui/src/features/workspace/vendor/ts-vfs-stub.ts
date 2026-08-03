// Build-time stub aliased over `@typescript/vfs`, the only Node `fs`/`os` reference in the
// protoc-gen-es import chain. The three exports must exist (protoplugin imports them by name)
// but are never invoked. Companion to ts-stub.ts.
export const createDefaultMapFromNodeModules = () => new Map();
export const createSystem = () => ({});
export const createVirtualCompilerHost = () => ({ compilerHost: {} });
