// Typed generator signatures for the request body + metadata editors (ts-request-body-plan §P5 —
// the LAST phase). The workspace's saved GENERATORS are exposed to both editors as ambient
// globals: the backend injects each referenced generator as `globalThis.<name>` and the body /
// metadata module calls it directly (e.g. `mkid()`), so the editor mirrors those names to get
// autocomplete + type-checking instead of "Cannot find name". T3 declared them all as
// `(...args: any[]): any`; this module gives each its REAL inferred signature by round-tripping the
// generator's source through Monaco's own bundled TS worker (see registerGeneratorLibs).
import type * as Monaco from "monaco-editor";

// A workspace GENERATOR as the editors need it for typing: its display name AND its authored
// source. §P5 threads the SOURCE (not just the name, as T3 did) from RequestWorkspace down to both
// editors so each generator's real parameter + return types can be inferred and surfaced.
export interface GeneratorDef {
  name: string;
  source: string;
}

// JS reserved words that are ILLEGAL as a binding identifier. A generator so named (e.g.
// `default`) would emit `const default: …` (and `import … default`), a SYNTAX error that makes
// Monaco fail to parse the whole globals .d.ts and silently kills ambient autocomplete for EVERY
// generator — so such names are skipped WHOLE (both the per-generator module and the global decl).
// They still run server-side if the backend accepts them; they just don't autocomplete — the same
// graceful degradation as dotted names. (Names colliding with lib globals like `Date` only produce
// a self-contained duplicate-identifier squiggle in a never-displayed lib, so they are left alone.)
const GENERATOR_NAME_RESERVED = new Set([
  "break", "case", "catch", "class", "const", "continue", "debugger", "default", "delete",
  "do", "else", "enum", "export", "extends", "false", "finally", "for", "function", "if",
  "import", "in", "instanceof", "new", "null", "return", "super", "switch", "this", "throw",
  "true", "try", "typeof", "var", "void", "while", "with", "let", "static", "yield", "await",
  "implements", "interface", "package", "private", "protected", "public",
]);

// A generator name we can safely emit as a bare identifier: a simple JS identifier that is not a
// reserved word. Mirrors the backend's composition rule (compose.go injects only simple-identifier
// generators as `globalThis.<name>`), so a dotted / reserved-word name is skipped here too.
const isEmittableName = (name: string): boolean =>
  /^[A-Za-z_$][A-Za-z0-9_$]*$/.test(name) && !GENERATOR_NAME_RESERVED.has(name);

// registerGeneratorLibs gives the body / metadata editor ambient autocomplete for the workspace's
// saved generators WITH each generator's real inferred signature (§P5). For every emittable
// generator it registers two extra-libs on the (global) typescriptDefaults:
//
//   1. the RAW generator source verbatim as its OWN virtual TS MODULE at
//      file:///grpcview/request/gen/<scope>/<name>.ts — Monaco's TS worker type-checks it in
//      ISOLATION and infers its `export default`'s type; and
//   2. one shared globals .d.ts that declares each generator as
//      `const <name>: typeof import("./gen/<scope>/<name>").default;` inside a `declare global`
//      block, so the editor sees the generator with its inferred params + return.
//
// Why `typeof import(...).default` (full type inference) rather than an emitted `.d.ts`: isolated
// declarations / ts.transpileDeclaration would emit `any` for a casually-authored generator that
// lacks explicit annotations; the type query infers the real type from the implementation. So
// `export default (n: number): string => …` types as `(n: number) => string` (and a wrong-typed
// call errors), while `export default () => "id-42"` still infers `() => string`; an un-annotated
// parameter degrades to `any` but the return is still inferred. The relative extensionless import
// resolves under Monaco's node10 moduleResolution to the .ts module in (1) — the SAME resolution
// the T2 request-message alias already relies on (proto-types.ts: `import … from "./gen/<base>"`).
//
// Invariants (carried from T3):
//   1. Name safety — only emittable names get BOTH a module and a global decl; a reserved / dotted
//      name is skipped whole (never `const default: …`, which would break the entire globals parse
//      and kill autocomplete for every generator).
//   2. Failure isolation — each generator is its OWN module, so a source with a type error or an
//      unresolvable import (e.g. `import dayjs`, or composing a skipped generator) degrades to
//      `any` / shows its error only in its own never-displayed module. It cannot poison the other
//      generators, the globals .d.ts (which is syntactically valid regardless of any source), or
//      the body/metadata model itself. The generator modules' own markers land on their own URIs,
//      which the editors' footer never reads (it filters markers to its own model's resource).
//   3. Body vs metadata separation — `scope` namespaces BOTH the per-generator module URIs
//      (gen/body/… vs gen/metadata/…) and the globals .d.ts path (generators.d.ts vs
//      metadata-generators.d.ts), so one editor's dispose never yanks the other's libs.
//
// Returns every disposable it registered; the caller disposes them all (dispose-before-add when the
// generator set changes, and on unmount) since typescriptDefaults is global with no per-URI
// fileMatch and same-path re-add can throw "Duplicate definition".
export function registerGeneratorLibs(
  tsDefaults: Monaco.languages.typescript.LanguageServiceDefaults,
  generators: GeneratorDef[],
  scope: "body" | "metadata"
): Monaco.IDisposable[] {
  const emittable = generators.filter((g) => isEmittableName(g.name));
  const disposables: Monaco.IDisposable[] = [];

  // (1) Each generator's source as its own isolated module, so the worker infers its default
  // export's type and a broken one degrades in place instead of poisoning the shared env.
  for (const g of emittable) {
    disposables.push(
      tsDefaults.addExtraLib(g.source, `file:///grpcview/request/gen/${scope}/${g.name}.ts`)
    );
  }

  // (2) One globals .d.ts wiring each generator name to its module's inferred default-export type.
  // It is a MODULE (`export {};`) whose `declare global` block contributes the ambient globals;
  // being a module is what lets the relative `import(...)` type queries resolve from this file.
  // Always syntactically valid regardless of any generator's source, so no source can break it; an
  // empty set collapses to a bare `export {};` no-op.
  const decls = emittable
    .map((g) => `  const ${g.name}: typeof import("./gen/${scope}/${g.name}").default;`)
    .join("\n");
  const globalsDts = emittable.length
    ? `declare global {\n${decls}\n}\nexport {};\n`
    : "export {};\n";
  const globalsPath =
    scope === "body"
      ? "file:///grpcview/request/generators.d.ts"
      : "file:///grpcview/request/metadata-generators.d.ts";
  disposables.push(tsDefaults.addExtraLib(globalsDts, globalsPath));

  return disposables;
}
