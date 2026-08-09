// A script is a plain TypeScript module, and its identity is its path. Nothing
// binds it to a global: a body, a metadata script, another script or a
// middleware reaches it by importing it.
//
//   import { requestId } from "#/scripts/ids";   // from inside this collection
//   import { requestId } from "@/example/scripts/ids";  // from anywhere in the workspace
//
// `#/` resolves against the collection root, `@/` against the workspace root.
// Both are resolved by the same esbuild pass that resolves npm packages, and
// both are guarded against escaping their root.
//
// Every sandbox instance starts from the same seed, so Math.random() here is
// reproducible across runs rather than unique — fine for a demo id, not a
// substitute for a real uuid.

const hex = (n: number): string =>
  Array.from({ length: n }, () => Math.floor(Math.random() * 16).toString(16)).join("");

export const requestId = (prefix = "req"): string => `${prefix}_${hex(8)}${hex(4)}`;
