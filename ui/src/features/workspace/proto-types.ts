// Runs the real @bufbuild/protoc-gen-es in-browser over the workspace's merged
// FileDescriptorSet to give Monaco the generated protojson `<Message>Json` types.

// Deep CJS import is MANDATORY: @bufbuild/protoc-gen-es ships CJS-only with no `exports`/`main`
// field, so the package specifier alone does not resolve.
import { protocGenEs } from "@bufbuild/protoc-gen-es/dist/cjs/src/protoc-gen-es-plugin.js";
import { fromBinary, create } from "@bufbuild/protobuf";
import {
  FileDescriptorSetSchema,
  CodeGeneratorRequestSchema,
} from "@bufbuild/protobuf/wkt";

const cache = new WeakMap<Uint8Array, Map<string, string>>();

// generateWorkspaceTypes maps generated-file name (e.g. "proto/foo/v1/foo_pb.ts") → TS source.
// WKTs are left out; their Json types come from the vendor/bufbuild-stubs.ts Monaco stub.
export function generateWorkspaceTypes(
  descriptorSet: Uint8Array,
): Map<string, string> {
  const memo = cache.get(descriptorSet);
  if (memo) return memo;

  const fds = fromBinary(FileDescriptorSetSchema, descriptorSet);
  const fileToGenerate = fds.file
    .map((f) => f.name)
    .filter((n) => !n.startsWith("google/protobuf/"));
  const req = create(CodeGeneratorRequestSchema, {
    fileToGenerate,
    protoFile: fds.file,
    // Required: protocGenEs.run() calls request.sourceFileDescriptors.find(...).
    sourceFileDescriptors: [],
    parameter: "target=ts,json_types=true",
  });
  const resp = protocGenEs.run(req);
  const files = new Map<string, string>();
  for (const f of resp.file) files.set(f.name, f.content ?? "");
  cache.set(descriptorSet, files);
  return files;
}

// resolveLocalSymbol reads the full-name literal in `export type <Sym> = Message…<"<fullName>">`,
// which is what lets a nested message resolve from its short name.
export function resolveLocalSymbol(
  content: string,
  pkg: string,
  name: string,
): string | null {
  const byFullName = new Map<string, string>();
  const re =
    /export\s+(?:declare\s+)?type\s+(\$?\w+)\s*=\s*[A-Za-z_$][\w$]*\s*<\s*"([^"]+)"/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(content)) !== null) byFullName.set(m[2], m[1]);

  const exact = pkg ? `${pkg}.${name}` : name;
  const local = byFullName.get(exact);
  if (local) return local;
  for (const [full, sym] of byFullName) {
    if (
      (!pkg || full.startsWith(pkg + ".")) &&
      (full === exact || full.endsWith("." + name))
    ) {
      return sym;
    }
  }
  return null;
}

// requestMessageAlias builds the per-method ambient d.ts aliasing `RequestMessage` to the input
// message's `<Local>Json` type. Import paths stay extensionless — node10 substitutes .ts.
export function requestMessageAlias(
  files: Map<string, string>,
  pkg: string,
  name: string,
  file: string,
): { symbol: string; importPath: string; dts: string } {
  const base = file.replace(/\.proto$/, "_pb");
  const importPath = `./gen/${base}`;
  const content = files.get(`${base}.ts`) ?? "";

  const local = resolveLocalSymbol(content, pkg, name);
  const symbol = (local ?? name.replace(/\./g, "_")) + "Json";

  const dts = `import type { ${symbol} } from "${importPath}";
declare global { type RequestMessage = ${symbol}; }
export {};
`;
  return { symbol, importPath, dts };
}

// A saved request `invoke()` can reach: its display-name path and its RESPONSE message.
export interface InvokeTarget {
  path: string;
  pkg: string;
  name: string;
  file: string;
}

// gvRequestMapDts builds the ambient d.ts that populates `GvRequestMap` — the path → response
// table `invoke()`'s generic signature reads (see gv.d.ts in monaco-scripts.ts). Null when
// nothing resolves, so the caller registers nothing and `invoke` stays at its `any` fallback.
//
// Every import is aliased positionally: two proto files may each export a `FooJson`, and the map
// is one flat file.
export function gvRequestMapDts(
  files: Map<string, string>,
  targets: InvokeTarget[],
): string | null {
  const imports: string[] = [];
  const entries: string[] = [];
  const seen = new Set<string>();

  for (const target of targets) {
    if (seen.has(target.path)) continue;
    // WKT responses are not generated (generateWorkspaceTypes skips google/protobuf/*), so
    // there is no module to import them from.
    if (target.file.startsWith("google/protobuf/")) continue;

    const base = target.file.replace(/\.proto$/, "_pb");
    const content = files.get(`${base}.ts`);
    if (!content) continue;
    const local = resolveLocalSymbol(content, target.pkg, target.name);
    if (!local) continue;

    const alias = `GvResponse${imports.length}`;
    imports.push(
      `import type { ${local}Json as ${alias} } from "./gen/${base}";`,
    );
    entries.push(`    ${JSON.stringify(target.path)}: { response: ${alias} };`);
    seen.add(target.path);
  }

  if (entries.length === 0) return null;
  return `${imports.join("\n")}
declare global {
  interface GvRequestMap {
${entries.join("\n")}
  }
}
export {};
`;
}

// messageTypeText returns the generated `<Message>Json` type text for the TypesModal, or null
// when there is nothing to show. Unlike requestMessageAlias it never guesses a symbol.
export function messageTypeText(
  files: Map<string, string>,
  pkg: string,
  name: string,
  file: string,
): { symbol: string; text: string } | null {
  if (file.startsWith("google/protobuf/")) return null;

  const base = file.replace(/\.proto$/, "_pb");
  const content = files.get(`${base}.ts`);
  if (!content) return null;

  const local = resolveLocalSymbol(content, pkg, name);
  if (!local) return null;

  const symbol = `${local}Json`;
  const text = sliceTypeBlock(content, symbol) ?? content;
  return { symbol, text };
}

function sliceTypeBlock(content: string, symbol: string): string | null {
  const re = new RegExp(
    `export\\s+(?:declare\\s+)?type\\s+${escapeRegExp(symbol)}\\s*=`,
  );
  const m = re.exec(content);
  if (!m) return null;

  const start = m.index;
  let depth = 0;
  for (let i = start + m[0].length; i < content.length; i++) {
    const c = content[i];
    if (c === "{") depth++;
    else if (c === "}") depth--;
    else if (c === ";" && depth <= 0) return content.slice(start, i + 1);
  }
  return null;
}

function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
