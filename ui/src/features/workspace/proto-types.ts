// Client-side proto type generation for the typed request body (ts-request-body-plan
// §T2/§2.5/§4.4). Runs the REAL `@bufbuild/protoc-gen-es` generator in-browser over the
// workspace's merged FileDescriptorSet (shipped on `Workspace.descriptorSet`) to produce
// the generated `_pb.ts` sources. Feeding those straight to Monaco (no `.d.ts` transpile
// step — plan risk #7 "lean") gives the body editor the correct protojson `<Message>Json`
// types: WKTs mapped to their string forms, int64 as string, oneofs, snake-case aliases —
// exactly what the lossy backend JSON Schema cannot express. The `typescript`/`@typescript/vfs`
// modules that protoc-gen-es statically imports are aliased to no-op stubs in vite.config.ts
// (never reached on the `target=ts` path).
//
// This module is dynamically imported by Editor.tsx (only when a request first enters TS
// mode), so protoc-gen-es lands in a lazy chunk off the main bundle. Generation is memoized
// by descriptorSet reference (workspace-global, stable) — every method reuses one run. A Web
// Worker is a T4 optimization (the 130KB FDS generates sub-second on the main thread).

// Deep CJS import is MANDATORY: @bufbuild/protoc-gen-es ships CJS-only with no `exports`/`main`
// field, so the package specifier alone does not resolve. The named export is `protocGenEs`.
import { protocGenEs } from "@bufbuild/protoc-gen-es/dist/cjs/src/protoc-gen-es-plugin.js";
import { fromBinary, create } from "@bufbuild/protobuf";
import { FileDescriptorSetSchema, CodeGeneratorRequestSchema } from "@bufbuild/protobuf/wkt";

// Memoize the generated file set by descriptorSet identity. The workspace ships one merged,
// deduped FDS; it is a stable reference until the workspace re-resolves, so a WeakMap keyed on
// it avoids regenerating for every method switch.
const cache = new WeakMap<Uint8Array, Map<string, string>>();

// generateWorkspaceTypes runs protoc-gen-es over every NON-well-known file in the descriptor
// set and returns a map of generated-file name (e.g. "proto/foo/v1/foo_pb.ts") → TS source.
// WKT files (google/protobuf/*) are excluded from fileToGenerate: protoc-gen-es references
// their Json types externally (imported from "@bufbuild/protobuf/wkt"), which the Monaco stub
// (vendor/bufbuild-stubs.ts) supplies — so we never generate them locally.
export function generateWorkspaceTypes(descriptorSet: Uint8Array): Map<string, string> {
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

// requestMessageAlias builds the tiny per-method ambient d.ts that aliases `RequestMessage`
// to the input message's generated `<Local>Json` type. Registered at a constant Monaco path
// and re-added on method change (Editor.tsx §3). The `declare global` makes `RequestMessage`
// visible inside the body module (which is a module via its `export default`), so the
// return-annotated template type-checks the returned object literal against the protojson shape.
//
// - importPath: the generated file relative to the alias's own path (file:///grpcview/request/
//   request-message.d.ts) — the generated files live under ./gen/<protopath>_pb (extensionless;
//   node10 substitutes .ts). Derived from `file` (the defining proto path, e.g.
//   "proto/foo/v1/foo.proto" → "./gen/proto/foo/v1/foo_pb").
// - symbol: resolved by scanning the generated file for the message whose FULL proto name
//   matches the input, then appending "Json". protoc-gen-es emits each message's runtime type
//   as `export type <Local> = Message…<"<fullName>"> & {…}` (e.g.
//   `Request_Response = Message$1<"grpcview.v1.Request.Response">`), so the literal is the
//   authoritative map from full proto name → emitted local symbol. This is what lets NESTED
//   input messages resolve: `Message.name` is only the SHORT name (`Response`), from which the
//   parent path (`Request_`) can't be re-derived — but the full-name literal carries it, and it
//   also absorbs `safeIdentifier`'s `$`-prefixing for free. Falls back to naive derivation if
//   the scan misses (a miss only makes the non-active generated lib error, not surfaced as a
//   marker, and the field degrades to untyped).
export function requestMessageAlias(
  files: Map<string, string>,
  pkg: string,
  name: string,
  file: string
): { symbol: string; importPath: string; dts: string } {
  const base = file.replace(/\.proto$/, "_pb");
  const importPath = `./gen/${base}`;
  const content = files.get(`${base}.ts`) ?? "";

  // Map every message's full proto name → its generated local symbol, from the runtime type
  // decls `export type <Sym> = Message…<"<fullName>">`. Any identifier is allowed before `<"`
  // so the `Message`/`Message$1` alias (protoc-gen-es renames it when a proto message is itself
  // named `Message`) doesn't matter.
  const byFullName = new Map<string, string>();
  const re = /export\s+(?:declare\s+)?type\s+(\$?\w+)\s*=\s*[A-Za-z_$][\w$]*\s*<\s*"([^"]+)"/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(content)) !== null) byFullName.set(m[2], m[1]);

  // Pick the input message: an exact top-level match first (the common case), else a nested
  // message whose full name is under this package and ends in the short name.
  const exact = pkg ? `${pkg}.${name}` : name;
  let local = byFullName.get(exact);
  if (!local) {
    for (const [full, sym] of byFullName) {
      if ((!pkg || full.startsWith(pkg + ".")) && (full === exact || full.endsWith("." + name))) {
        local = sym;
        break;
      }
    }
  }
  // Fall back to naive short-name derivation (preserves prior behavior when the scan misses).
  const symbol = (local ?? name.replace(/\./g, "_")) + "Json";

  const dts = `import type { ${symbol} } from "${importPath}";
declare global { type RequestMessage = ${symbol}; }
export {};
`;
  return { symbol, importPath, dts };
}
