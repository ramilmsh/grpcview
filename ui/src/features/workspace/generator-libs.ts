// Typed ambient globals for the workspace's saved generators, mirroring the backend's
// `globalThis.<name>` injection so the body / metadata editors type-check calls to them.
import type * as Monaco from "monaco-editor";

export interface GeneratorDef {
  name: string;
  source: string;
}

// A generator named with a reserved word would emit `const default: …`, a syntax error that kills
// the whole globals .d.ts — and with it ambient autocomplete for EVERY generator.
const GENERATOR_NAME_RESERVED = new Set([
  "break", "case", "catch", "class", "const", "continue", "debugger", "default", "delete",
  "do", "else", "enum", "export", "extends", "false", "finally", "for", "function", "if",
  "import", "in", "instanceof", "new", "null", "return", "super", "switch", "this", "throw",
  "true", "try", "typeof", "var", "void", "while", "with", "let", "static", "yield", "await",
  "implements", "interface", "package", "private", "protected", "public",
]);

// Mirrors compose.go, which injects only simple-identifier generators.
const isEmittableName = (name: string): boolean =>
  /^[A-Za-z_$][A-Za-z0-9_$]*$/.test(name) && !GENERATOR_NAME_RESERVED.has(name);

// registerGeneratorLibs wires each generator name to its module's inferred default-export type.
// Dispose the result before re-adding: typescriptDefaults is global and a same-path re-add throws.
export function registerGeneratorLibs(
  tsDefaults: Monaco.languages.typescript.LanguageServiceDefaults,
  generators: GeneratorDef[],
  scope: "body" | "metadata" | "scripts"
): Monaco.IDisposable[] {
  const emittable = generators.filter((g) => isEmittableName(g.name));
  const disposables: Monaco.IDisposable[] = [];

  for (const g of emittable) {
    disposables.push(
      tsDefaults.addExtraLib(g.source, `file:///grpcview/request/gen/${scope}/${g.name}.ts`)
    );
  }

  // Must be a MODULE (`export {};`) for the relative `import(...)` type queries to resolve.
  const decls = emittable
    .map((g) => `  const ${g.name}: typeof import("./gen/${scope}/${g.name}").default;`)
    .join("\n");
  const globalsDts = emittable.length
    ? `declare global {\n${decls}\n}\nexport {};\n`
    : "export {};\n";
  const globalsPath =
    scope === "body"
      ? "file:///grpcview/request/generators.d.ts"
      : scope === "metadata"
        ? "file:///grpcview/request/metadata-generators.d.ts"
        : "file:///grpcview/request/scripts-generators.d.ts";
  disposables.push(tsDefaults.addExtraLib(globalsDts, globalsPath));

  return disposables;
}
