// Golden for service/scripting/codegen: real protoc-gen-es under node, which codegen.Pool (the
// same plugin under QuickJS) must reproduce byte-for-byte. Regenerate:
//   UPDATE_GOLDEN=1 bazel run //service/scripting/codegen/testdata:golden_test
const assert = require("node:assert/strict");
const { readFileSync, writeFileSync } = require("node:fs");
const { join } = require("node:path");
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

const ws = process.env.BUILD_WORKSPACE_DIRECTORY;
for (const set of ["example", "all"]) {
  const files = generate(new Uint8Array(readFileSync(join(__dirname, `${set}.binpb`))));
  const sorted = Object.fromEntries(
    Object.entries(files).sort(([a], [b]) => (a < b ? -1 : 1)),
  );
  const got = JSON.stringify(sorted, null, 2) + "\n";
  const golden = `${set}.golden.json`;
  if (process.env.UPDATE_GOLDEN && ws) {
    writeFileSync(join(ws, "service/scripting/codegen/testdata", golden), got);
    console.log(`wrote ${golden}`);
    continue;
  }
  assert.equal(got, readFileSync(join(__dirname, golden), "utf8"), `${golden} is stale`);
}
