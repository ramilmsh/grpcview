// protoc-gen-es under QuickJS; codegen/testdata/golden.cjs is its node twin. Bundled once
// per worker (see codegen_bundle.go) into a standalone
// IIFE that defines a global the Go side calls once per generate job.
import { protocGenEs } from "@bufbuild/protoc-gen-es/dist/cjs/src/protoc-gen-es-plugin.js";
import { fromBinary, create } from "@bufbuild/protobuf";
import {
  FileDescriptorSetSchema,
  CodeGeneratorRequestSchema,
} from "@bufbuild/protobuf/wkt";

// fileToGenerate: names to emit (null = every non-WKT file); descriptorSet need only hold their
// transitive closure — each _pb.ts depends on nothing else (codegen/pool_test.go's gate).
globalThis.__grpcview_codegen = function (
  descriptorSet,
  fileToGenerate,
  parameter,
) {
  const fds = fromBinary(FileDescriptorSetSchema, descriptorSet);
  fileToGenerate ??= fds.file
    .map((f) => f.name)
    .filter((n) => !n.startsWith("google/protobuf/"));
  const req = create(CodeGeneratorRequestSchema, {
    fileToGenerate,
    protoFile: fds.file,
    sourceFileDescriptors: [], // required: protocGenEs.run() calls .find() on it
    parameter,
  });
  const resp = protocGenEs.run(req);
  const files = {};
  for (const f of resp.file) files[f.name] = f.content ?? "";
  return files;
};
