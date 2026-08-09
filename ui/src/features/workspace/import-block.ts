// The managed import block (D9, docs/design/planned/script-region.md) and the pruning of it
// (D7 + D8). Pure logic, testable without Monaco or a real TS worker — the worker's diagnostics
// are plain data (UnusedSpan) by the time they reach pruneEdits.
//
// D8: the TS worker is the oracle for "is this import unused", never a hand-rolled parser — a
// regex over the region can't tell `{ id: 1 }`'s KEY from a reference. But parsing the import
// BLOCK itself (not the region) is a much smaller, regular surface: one declaration per line,
// always of the form `import { ... } from "...";`. That much is safe to parse directly.
import type { LineEdit } from "./region-edits";
import { findRegion } from "./script-region";

// One `import { a, b as c } from "spec";` line, decomposed. `names` keeps each entry exactly as
// written (`"a"` or `"b as c"`) so re-rendering doesn't need to reconstruct alias syntax.
export interface NamedImport {
  specifier: string;
  names: string[];
}

// A line matching this, in full (after trim), is the ONLY form this module manages: a plain
// named-import declaration with no default and no namespace binding. `import type { ... }` does
// not match — the `\s*` between "import" and "{" cannot also consume the word "type" — so a
// type-only declaration falls through to `other`, untouched, same as a default or namespace one.
const PLAIN_NAMED_RE = /^import\s*\{([^}]*)\}\s*from\s*(['"])([^'"]+)\2\s*;?\s*$/;
// A single name entry inside the braces: `a` or `a as b`. Anything else — notably a per-name
// `type Foo` modifier in a mixed value/type list — fails this and the whole line is left
// unmanaged, on the same "don't guess" principle as D8.
const NAME_RE = /^\w+(?:\s+as\s+\w+)?$/;
// The regenerated skeleton line (D10) — `export default async (...): Promise<T> => (`. Shared by
// both BODY_SKELETON and META_SKELETON, so matched generically rather than by exact string: this
// module never receives the skeleton text itself, only the header that contains it.
const SKELETON_LINE_RE = /^\s*export\s+default\b/;

function localNameOf(entry: string): string {
  const m = entry.match(/\bas\s+(\w+)\s*$/);
  return m ? m[1] : entry.trim();
}

// Splits a header (the machine-owned block above the start marker — script-region.ts's
// regionHeader) into the named-import lines this module manages and everything else. The
// skeleton line and blank lines are pure noise here — the caller (pruneEdits) re-adds the
// skeleton itself — so neither ends up in `other`; `other` holds only the non-blank,
// non-skeleton lines that are not a managed plain-named import, preserved byte-for-byte
// (a default import, a namespace import, a side-effect import, or a type-only import).
export function parseImportBlock(header: string): { imports: NamedImport[]; other: string[] } {
  const imports: NamedImport[] = [];
  const other: string[] = [];
  if (header.length === 0) return { imports, other };
  for (const line of header.split("\n")) {
    const trimmed = line.trim();
    if (trimmed === "") continue;
    if (SKELETON_LINE_RE.test(trimmed)) continue;
    const m = trimmed.match(PLAIN_NAMED_RE);
    if (m) {
      const names = m[1]
        .split(",")
        .map((s) => s.trim())
        .filter((s) => s.length > 0);
      if (names.length > 0 && names.every((n) => NAME_RE.test(n))) {
        imports.push({ specifier: m[3], names });
        continue;
      }
    }
    other.push(line);
  }
  return { imports, other };
}

// D9: sorted by specifier, then by name within a specifier (lexically, by the entry as written —
// so `requestId` and `stamp as s` sort by their own name, "as" always sorting after it), merging
// duplicate specifiers and deduping identical names. Deterministic so the block, being
// regenerated wholesale, does not churn in git on every edit.
export function renderImportBlock(imports: readonly NamedImport[]): string[] {
  const bySpecifier = new Map<string, Set<string>>();
  for (const imp of imports) {
    const names = bySpecifier.get(imp.specifier) ?? new Set<string>();
    for (const name of imp.names) names.add(name);
    bySpecifier.set(imp.specifier, names);
  }
  const specifiers = [...bySpecifier.keys()].sort();
  return specifiers.map((specifier) => {
    const names = [...bySpecifier.get(specifier)!].sort((a, b) => a.localeCompare(b));
    return `import { ${names.join(", ")} } from "${specifier}";`;
  });
}

export interface UnusedSpan {
  start: number;
  length: number;
  code: number;
}

// Observed from monaco's vendored ts.worker.js (verified directly against the `typescript`
// package's language service, not assumed from the diagnostic message catalog): unused-import
// diagnostics come back as code 6133 OR 6192, and — this is the part the surrounding design doc
// does not anticipate — 6133's span is NOT always just the bound identifier:
//
//   import { a, b } from "./mod";   // only `b` unused → 6133, span = "b"            (narrow)
//   import { c } from "./mod2";     // `c` unused, sole binding → 6133, span = WHOLE DECL
//   import { c, d } from "./mod2";  // both unused → 6192, span = WHOLE DECL
//   import * as ns from "./mod";    // unused, sole binding → 6133, span = WHOLE DECL
//   import def from "./mod";        // unused, sole binding → 6133, span = WHOLE DECL
//
// So 6133 is whole-declaration whenever the declaration has exactly one binding and that binding
// is unused, and narrow-identifier only when it is one of SEVERAL bindings and the others are
// still used. Code alone does not distinguish these; the span's own text does — a whole
// declaration starts with "import", a bound name never does. Dispatch is on that shape, not on
// which of 6133/6192 fired.
function isWholeDeclarationSpan(spanText: string): boolean {
  return /^import\b/.test(spanText);
}

// Returns the edits that rewrite the machine-owned header with the unused named imports removed,
// or undefined when there is nothing to change. undefined for a document with no region — this
// never touches a plain script's visible imports (D7): those are the author's, never pruned on a
// timer.
//
// Only spans that land strictly above the start marker are eligible; a span inside the region
// (the author's code) is left alone. A span pointing at an import this module does not manage
// (default/namespace/side-effect/type-only, or already gone) is a no-op, not an error.
export function pruneEdits(
  text: string,
  skeleton: string,
  unused: readonly UnusedSpan[]
): LineEdit[] | undefined {
  const region = findRegion(text);
  if (!region) return undefined;

  const lines = text.split("\n");
  const headerLines = lines.slice(0, region.startLine - 1);
  const header = headerLines.join("\n");
  // Offset, in `text`, of the first character of the start-marker line: headerLines.length === 0
  // means the marker is line 1 and there is no header at all, so every span is "in the region".
  const startMarkerOffset = headerLines.length === 0 ? 0 : header.length + 1;

  const { imports, other } = parseImportBlock(header);
  const bySpecifier = new Map<string, Set<string>>();
  for (const imp of imports) {
    const names = bySpecifier.get(imp.specifier) ?? new Set<string>();
    for (const name of imp.names) names.add(name);
    bySpecifier.set(imp.specifier, names);
  }

  let changed = false;
  for (const span of unused) {
    if (span.code !== 6133 && span.code !== 6192) continue;
    if (span.start >= startMarkerOffset) continue; // region text — the author's, never pruned
    const spanText = text.slice(span.start, span.start + span.length).trim();
    if (isWholeDeclarationSpan(spanText)) {
      const m = spanText.match(PLAIN_NAMED_RE);
      if (m && bySpecifier.delete(m[3])) changed = true;
      continue;
    }
    // Narrow form: spanText is the bound name. Find whichever managed import declares it.
    for (const [specifier, names] of bySpecifier) {
      let hit: string | undefined;
      for (const entry of names) {
        if (localNameOf(entry) === spanText) {
          hit = entry;
          break;
        }
      }
      if (hit !== undefined) {
        names.delete(hit);
        changed = true;
        if (names.size === 0) bySpecifier.delete(specifier);
        break;
      }
    }
  }

  if (!changed) return undefined;

  const prunedImports: NamedImport[] = [...bySpecifier].map(([specifier, names]) => ({
    specifier,
    names: [...names],
  }));
  const headerBody = [...other, ...renderImportBlock(prunedImports)];
  const finalLines = headerBody.length === 0 ? [skeleton] : [...headerBody, "", skeleton];

  return [
    {
      range: { startLineNumber: 1, startColumn: 1, endLineNumber: region.startLine, endColumn: 1 },
      text: finalLines.join("\n") + "\n",
    },
  ];
}
