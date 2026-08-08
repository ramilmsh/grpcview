// Pure mapping from workspace-relative TypeScript module paths to the Monaco model URIs the
// bundler's two import sigils resolve against (script-imports/decisions.md §8). Split out of
// gv-types.ts so the URI shape and the `compilerOptions.paths` object are testable without a
// Monaco instance.
import type * as Monaco from "monaco-editor";

// Every listed module is registered at `${WS_PREFIX}/<workspace-relative path>`.
export const WS_PREFIX = "file:///grpcview/ws";

export function workspaceModuleUri(path: string): string {
  return `${WS_PREFIX}/${path}`;
}

// The collection root segment `~/*` resolves against. "." (the workspace-root collection,
// decisions.md §8) maps to the workspace prefix itself — NOT `${WS_PREFIX}/.`, which Monaco's
// resolver would treat as a literal "." path segment rather than the root.
export function collectionModulePrefix(collectionId: string | null | undefined): string {
  if (!collectionId || collectionId === ".") return WS_PREFIX;
  return `${WS_PREFIX}/${collectionId}`;
}

// `@/*` always resolves against the workspace root; `~/*` against the ACTIVE collection's root,
// re-derived whenever it changes.
export function workspaceModulePaths(
  collectionId: string | null | undefined
): NonNullable<Monaco.languages.typescript.CompilerOptions["paths"]> {
  return {
    "@/*": [`${WS_PREFIX}/*`],
    "~/*": [`${collectionModulePrefix(collectionId)}/*`],
  };
}
