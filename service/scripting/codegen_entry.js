// Server-side counterpart of ui/src/features/workspace/proto-types.ts's
// generateWorkspaceTypes. Bundled once per worker (see codegen_bundle.go) into a standalone
// IIFE that defines a global the Go side calls once per generate job.
import { protocGenEs } from "@bufbuild/protoc-gen-es/dist/cjs/src/protoc-gen-es-plugin.js";
import { fromBinary, create } from "@bufbuild/protobuf";
import {
  FileDescriptorSetSchema,
  CodeGeneratorRequestSchema,
} from "@bufbuild/protobuf/wkt";

function hexToBytes(hex) {
  var n = hex.length >> 1;
  var out = new Uint8Array(n);
  for (var i = 0; i < n; i++) out[i] = parseInt(hex.substr(i * 2, 2), 16);
  return out;
}

globalThis.__grpcview_codegen = function (hexDescriptorSet) {
  const fds = fromBinary(FileDescriptorSetSchema, hexToBytes(hexDescriptorSet));
  const fileToGenerate = fds.file
    .map((f) => f.name)
    .filter((n) => !n.startsWith("google/protobuf/"));
  const req = create(CodeGeneratorRequestSchema, {
    fileToGenerate,
    protoFile: fds.file,
    sourceFileDescriptors: [], // required: protocGenEs.run() calls .find() on it
    parameter: "target=ts,json_types=true",
  });
  const resp = protocGenEs.run(req);
  const files = {};
  for (const f of resp.file) files[f.name] = f.content ?? "";
  return files;
};
