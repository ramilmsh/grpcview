// The typed half of `require()`. monaco-scripts.ts declares
//
//   declare function require<S extends string>(s: S): S extends keyof GvModules ? GvModules[S] : any;
//
// with GvModules holding only the "grpcview:" capabilities; this generates the workspace's own
// half of that interface, one entry per importable module, and gv-types.ts merges it in.
//
// Why an interface rather than the obvious overload set: overloads that merge across FILES take
// the program's file order, which nothing here controls, so a `(specifier: string): any` sitting
// first would swallow every literal. A keyed lookup has no order to get wrong.
//
// Why generated at all: `paths` resolution works fine from an import STATEMENT, but the checker
// will not follow a specifier from a call expression — `typeof import("<literal>")` is the only
// construct that maps a specifier to a module type, and it needs the literal spelled out.
import {
  collectionPathPrefix,
  stripModuleExtension,
} from "./workspace-modules";

export interface WorkspaceModuleSource {
  path: string;
  content: string;
}

// A file with no export is a script, not a module: `typeof import()` of it is an error rather
// than a type. Cheap textual test — over-matching (the word inside a comment) only costs a dead
// entry, and the entry's type degrades to `any`, which is where it started.
const HAS_EXPORT_RE = /(^|[\s;}])export[\s{*]/;

// The specifiers a module answers to: `@/…` from anywhere, plus `#/…` when it lives under the
// active collection. For the workspace-root collection the two sigils name the same root, so the
// module earns both spellings of the same path.
export function moduleSpecifiers(
  path: string,
  collectionId: string | null | undefined,
): string[] {
  const rel = stripModuleExtension(path);
  const root = collectionPathPrefix(collectionId);
  const out = [`@/${rel}`];
  if (root === "") out.push(`#/${rel}`);
  else if (path.startsWith(root))
    out.push(`#/${stripModuleExtension(path.slice(root.length))}`);
  return out;
}

// Returns undefined when there is nothing to declare, so the caller registers no lib at all
// rather than an empty interface — same contract as gvRequestMapDts.
export function requireTypesDts(
  modules: readonly WorkspaceModuleSource[],
  collectionId: string | null | undefined,
): string | undefined {
  const specifiers = new Set<string>();
  for (const m of modules) {
    if (!HAS_EXPORT_RE.test(m.content)) continue;
    for (const s of moduleSpecifiers(m.path, collectionId)) specifiers.add(s);
  }
  if (specifiers.size === 0) return undefined;
  const entries = [...specifiers]
    .sort()
    .map((s) => `  ${JSON.stringify(s)}: typeof import(${JSON.stringify(s)});`)
    .join("\n");
  return `interface GvModules {\n${entries}\n}\n`;
}
