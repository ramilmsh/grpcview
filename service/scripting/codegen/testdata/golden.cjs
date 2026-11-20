// Golden for service/scripting/codegen: real protoc-gen-es under node, which codegen.Pool (the
// same plugin under QuickJS) must reproduce byte-for-byte. Built by :golden_json.
// Usage: golden.cjs <in.binpb> <out.json> [<in.binpb> <out.json>...]
const { readFileSync, writeFileSync } = require("node:fs");
// Deep CJS path: the package ships no `exports`/`main`.
const {
  protocGenEs,
} = require("@bufbuild/protoc-gen-es/dist/cjs/src/protoc-gen-es-plugin.js");
const { fromBinary, create } = require("@bufbuild/protobuf");
const {
  FileDescriptorSetSchema,
  CodeGeneratorRequestSchema,
} = require("@bufbuild/protobuf/wkt");

// Keep in step with codegen_entry.js and codegen.CodegenParameter.
function generate(set) {
  const fds = fromBinary(FileDescriptorSetSchema, set);
  const req = create(CodeGeneratorRequestSchema, {
    fileToGenerate: fds.file
      .map((f) => f.name)
      .filter((n) => !n.startsWith("google/protobuf/")),
    protoFile: fds.file,
    sourceFileDescriptors: [], // required: protocGenEs.run() calls .find() on it
    parameter: "target=ts,json_types=true",
  });
  const files = {};
  for (const f of protocGenEs.run(req).file) files[f.name] = f.content ?? "";
  return files;
}

const argv = process.argv.slice(2);
if (argv.length === 0 || argv.length % 2) throw new Error("usage: golden.cjs (<in> <out>)...");
for (let i = 0; i < argv.length; i += 2) {
  const files = generate(new Uint8Array(readFileSync(argv[i])));
  const sorted = Object.fromEntries(
    Object.entries(files).sort(([a], [b]) => (a < b ? -1 : 1)),
  );
  writeFileSync(argv[i + 1], JSON.stringify(sorted, null, 2) + "\n");
}
