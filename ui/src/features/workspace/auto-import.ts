// Pure logic behind the auto-import completion provider (module-auto-import.ts,
// script-imports/decisions.md §8). Split out so it is testable without a Monaco instance or a
// real TS worker — see that file's header for why the TS worker cannot supply this itself.
//
// The vendored monaco-typescript worker (node_modules/monaco-editor/esm/vs/language/typescript/
// ts.worker.js) exposes `getCompletionsAtPosition(fileName, position)` and
// `getCompletionEntryDetails(fileName, position, entry)` with NO options parameter — every extra
// argument a caller passes over the RPC is silently dropped, because the exposed class methods
// only declare two (resp. three) formal parameters and hardcode `undefined` for the rest before
// calling into the real `ts.LanguageService`. That is the parameter list `includeCompletions-
// ForModuleExports` would need to ride in on, so the TS compiler's own auto-import machinery is
// unreachable through this worker version — confirmed by reading the file, not assumed. The
// functions below reimplement just enough of it, working off data the app already has (the
// workspace's module list and the `#/` / `@/` path-sigil mapping), rather than the compiler's.
import { maskLiterals } from "./module-sniff";

// Declarations that create a name in a module's OWN scope: found lines are scanned regardless of
// whether they carry `export`, because a name declared but not exported still shadows an
// auto-import candidate of the same name.
const DECL_RE = /^[ \t]*(?:export\s+)?(?:async\s+)?(?:function\*?|class)\s+(\w+)/gm;
const VAR_DECL_RE = /^[ \t]*(?:export\s+)?(?:const|let|var)\s+(\w+)/gm;

// `export function`/`export class`/`export const` — deliberately excludes `export default …`:
// a default export has no name to bind at the import site (the caller picks its own local name),
// so there is no single specifier-relative label to offer as a completion for it.
const EXPORT_DECL_RE = /^[ \t]*export\s+(?:async\s+)?(?:function\*?|class)\s+(\w+)/gm;
const EXPORT_VAR_RE = /^[ \t]*export\s+(?:const|let|var)\s+(\w+)/gm;
// `export { a, b as c };` — a local re-export list. `export { x as default }` is excluded for
// the same reason as `export default` above.
const EXPORT_LIST_RE = /^[ \t]*export\s*\{([^}]*)\}/gm;

const IMPORT_DEFAULT_RE = /\bimport\s+(\w+)\s*(?:,\s*\{[^}]*\})?\s*from\s*["']/g;
const IMPORT_NAMESPACE_RE = /\bimport\s*\*\s*as\s+(\w+)\s*from\s*["']/g;
// Named import list — matched independent of a preceding default binding (which
// IMPORT_DEFAULT_RE already captures), on the reasoning already used elsewhere in this track:
// `{ ... } from "…"` outside a string or comment is, in practice, always an import.
const IMPORT_NAMED_RE = /\{([^}]*)\}\s*from\s*["']/g;

function lastAliasOf(binding: string): string | undefined {
  const item = binding.trim();
  if (!item) return undefined;
  const asMatch = item.match(/\bas\s+(\w+)\s*$/);
  return asMatch ? asMatch[1] : item;
}

function addAllMatches(re: RegExp, masked: string, into: Set<string>): void {
  re.lastIndex = 0;
  let m: RegExpExecArray | null;
  while ((m = re.exec(masked))) into.add(m[1]);
}

// extractExportedNames returns every name a module makes importable by name — the auto-import
// candidate list for that module. `import { <name> } from "<specifier>"` is legal for each.
export function extractExportedNames(source: string): string[] {
  const masked = maskLiterals(source);
  const names = new Set<string>();
  addAllMatches(EXPORT_DECL_RE, masked, names);
  addAllMatches(EXPORT_VAR_RE, masked, names);
  EXPORT_LIST_RE.lastIndex = 0;
  let m: RegExpExecArray | null;
  while ((m = EXPORT_LIST_RE.exec(masked))) {
    for (const raw of m[1].split(",")) {
      const alias = lastAliasOf(raw);
      if (alias && alias !== "default" && /^\w+$/.test(alias)) names.add(alias);
    }
  }
  return [...names].sort();
}

// namesAlreadyInScope returns every name already bound in `source` — by an import or a local
// top-level declaration — so the auto-import provider can skip offering what is already usable
// (and what the built-in TS completion provider already suggests for that reason).
export function namesAlreadyInScope(source: string): Set<string> {
  const masked = maskLiterals(source);
  const names = new Set<string>();
  addAllMatches(DECL_RE, masked, names);
  addAllMatches(VAR_DECL_RE, masked, names);
  addAllMatches(IMPORT_DEFAULT_RE, masked, names);
  addAllMatches(IMPORT_NAMESPACE_RE, masked, names);
  IMPORT_NAMED_RE.lastIndex = 0;
  let m: RegExpExecArray | null;
  while ((m = IMPORT_NAMED_RE.exec(masked))) {
    for (const raw of m[1].split(",")) {
      const alias = lastAliasOf(raw);
      if (alias && /^\w+$/.test(alias)) names.add(alias);
    }
  }
  return names;
}

// importSpecifierFor computes the specifier an inserted `import` statement should use — always
// path-mapped (`#/` against the active collection, `@/` against the workspace root), NEVER a
// relative path: unlike the compiler's own import-specifier chooser (which can pick either), this
// is the only chooser in play here, so there is no relative-vs-mapped ambiguity to resolve.
// `#/` is preferred when the module lives inside the active collection; `@/` (workspace-relative)
// is the fallback, including for a workspace-root collection where the two are identical.
export function importSpecifierFor(
  modulePath: string,
  collectionId: string | null | undefined
): string {
  const withoutExt = modulePath.replace(/\.tsx?$/, "");
  const prefix = !collectionId || collectionId === "." ? "" : `${collectionId}/`;
  if (prefix === "" || withoutExt.startsWith(prefix)) {
    return `#/${withoutExt.slice(prefix.length)}`;
  }
  return `@/${withoutExt}`;
}

export interface ImportInsertion {
  offset: number;
  insertText: string;
}

// insertImportEdit computes where to splice a new `import { <name> } from "<specifier>";` line
// into `source`: right after the last top-level import statement, or at the very top of the file
// if there is none yet. Offsets are computed against the UNMASKED source (maskLiterals preserves
// length and newlines byte-for-byte, so positions found in the masked text still index correctly
// into the original).
export function insertImportEdit(source: string, name: string, specifier: string): ImportInsertion {
  const masked = maskLiterals(source);
  const importLineRe = /^[ \t]*import\b.*$/gm;
  let lastEnd = -1;
  let m: RegExpExecArray | null;
  while ((m = importLineRe.exec(masked))) {
    lastEnd = m.index + m[0].length;
  }
  const stmt = `import { ${name} } from "${specifier}";`;
  if (lastEnd === -1) {
    return { offset: 0, insertText: `${stmt}\n` };
  }
  let insertAt = lastEnd;
  if (source[insertAt] === "\r") insertAt++;
  if (source[insertAt] === "\n") insertAt++;
  return { offset: insertAt, insertText: `${stmt}\n` };
}
