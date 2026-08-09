// Resolve-or-bail (D6, docs/design/planned/script-region.md): after a PASTE into a wrapped
// region, every name the TS worker reports as unresolved is looked up in the workspace export
// index plus the `grpcview:*` virtuals. Exactly one module exporting it means the import is added
// to the hidden block; zero or two-or-more means the whole pass bails and the region is unwrapped
// into a plain script, leaving the unresolved names as ordinary red squiggles for the author.
//
// Paste only, never typing, and that exclusion is load-bearing: a diagnostic cannot tell a
// half-typed `requestI` from a name nothing exports, so a bail-on-every-edit rule would rip the
// wrapper out on the way to typing any new identifier.
//
// Pure logic, like import-block.ts: the worker's diagnostics arrive here as plain spans, so the
// interesting behaviour is unit-testable without Monaco or a real TS worker.
import { extractExportedNames, importSpecifierFor } from "./auto-import";
import {
  localNameOf,
  parseImportBlock,
  rewriteHeaderEdits,
  type NamedImport,
} from "./import-block";
import type { LineEdit } from "./region-edits";
import { findRegion, regionHeader } from "./script-region";

export interface ModuleExports {
  specifier: string;
  names: readonly string[];
}

// The `grpcview:*` modules declared in features/scripts/monaco-scripts.ts's GV_DTS. Per D2 the
// skeleton carries no standard imports, so these are ordinary candidates resolved exactly like a
// workspace module — and per D12 all four stay offered on both surfaces, because that is what the
// runtime actually does.
export const VIRTUAL_MODULES: readonly ModuleExports[] = [
  { specifier: "grpcview:assert", names: ["assert"] },
  { specifier: "grpcview:invoke", names: ["invoke"] },
  { specifier: "grpcview:metadata", names: ["inherit"] },
  { specifier: "grpcview:request", names: ["params"] },
];

// `Cannot find name 'x'`. Deliberately not 2552 (`Cannot find name 'x'. Did you mean 'y'?`): that
// one fires when the checker already found a near-miss IN SCOPE, which is the shape of a typo, not
// of a missing import.
const CANNOT_FIND_NAME = 2304;

const IDENTIFIER_RE = /^[A-Za-z_$][\w$]*$/;

export interface NameSpan {
  start: number;
  length: number;
  code: number;
}

// The unresolved names the region itself references, deduped, in first-seen order. Spans above the
// start marker are the machine-owned header's and are never resolved against — an import binding
// does not produce 2304, so a span up there is something this module does not manage. [] for a
// document with no region: plain mode is never touched by this pass at all.
export function unresolvedNamesIn(text: string, spans: readonly NameSpan[]): string[] {
  const region = findRegion(text);
  if (!region) return [];
  const lines = text.split("\n");
  const offsetOfLine = (line: number): number => {
    let offset = 0;
    for (let i = 0; i < line - 1; i++) offset += lines[i].length + 1;
    return offset;
  };
  const regionStart = offsetOfLine(region.startLine);
  const regionEnd = offsetOfLine(region.endLine);

  const names: string[] = [];
  const seen = new Set<string>();
  for (const span of spans) {
    if (span.code !== CANNOT_FIND_NAME) continue;
    if (span.start < regionStart || span.start >= regionEnd) continue;
    const name = text.slice(span.start, span.start + span.length).trim();
    if (!IDENTIFIER_RE.test(name) || seen.has(name)) continue;
    seen.add(name);
    names.push(name);
  }
  return names;
}

export interface ModuleSource {
  path: string;
  content: string;
}

// Structural parameter type on purpose (never the protobuf WorkspaceModule): this module stays
// unit-testable with plain objects, and the editors pass module-auto-import.ts's live context
// straight through.
export function candidatesFrom(
  ctx: { modules: readonly ModuleSource[]; collectionId: string | null | undefined },
  currentPath: string | undefined
): ModuleExports[] {
  const out: ModuleExports[] = [];
  for (const m of ctx.modules) {
    if (m.path === currentPath) continue;
    out.push({
      specifier: importSpecifierFor(m.path, ctx.collectionId),
      names: extractExportedNames(m.content),
    });
  }
  out.push(...VIRTUAL_MODULES);
  return out;
}

export interface ResolveInput {
  text: string;
  skeleton: string;
  unresolved: readonly string[];
  candidates: readonly ModuleExports[];
}

export type ResolveOutcome =
  | { kind: "none" }
  | { kind: "addImports"; edits: LineEdit[] }
  | { kind: "bail" };

const NONE: ResolveOutcome = { kind: "none" };

// Bail is all-or-nothing across the whole pass, never a partial import followed by a bail: after
// the unwrap the header becomes visible author-owned text, and a half-written machine block there
// is worse than none at all.
export function resolveOrBail(input: ResolveInput): ResolveOutcome {
  const { text, skeleton, unresolved, candidates } = input;
  const region = findRegion(text);
  if (!region) return NONE;
  if (unresolved.length === 0) return NONE;

  const { imports, other } = parseImportBlock(regionHeader(text));
  const alreadyBound = new Set<string>();
  for (const imp of imports) {
    for (const entry of imp.names) alreadyBound.add(localNameOf(entry));
  }

  const additions: NamedImport[] = [];
  for (const name of unresolved) {
    // TS can report 2304 for a name the header DOES import (an unresolvable specifier, say).
    // Treat that as resolved to what is already there rather than counting it as ambiguous.
    if (alreadyBound.has(name)) continue;
    const specifiers = new Set<string>();
    for (const candidate of candidates) {
      if (candidate.names.includes(name)) specifiers.add(candidate.specifier);
    }
    if (specifiers.size !== 1) return { kind: "bail" };
    additions.push({ specifier: [...specifiers][0], names: [name] });
  }
  if (additions.length === 0) return NONE;

  const merged = [...imports, ...additions];
  return { kind: "addImports", edits: rewriteHeaderEdits(region, skeleton, merged, other) };
}
