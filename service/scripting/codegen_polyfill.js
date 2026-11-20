// TextEncoder/TextDecoder over the shim's native UTF-8 codecs (qjs_wasm.c) — this QuickJS
// build has no WHATWG APIs, and @bufbuild/protobuf's wire/text-encoding.js needs them.
//
// MUST be eval'd as its own script, before the codegen bundle (see codegen_bundle.go) — never
// prepended into the same bundled module. @bufbuild/protobuf/wkt parses an embedded
// FileDescriptorProto at its own module top level, before a same-bundle polyfill assignment
// would run.
if (typeof globalThis.TextEncoder === "undefined") {
  globalThis.TextEncoder = function TextEncoder() {};
  globalThis.TextEncoder.prototype.encode = function (str) {
    return __grpcview_utf8_encode(str == null ? "" : String(str));
  };
}
if (typeof globalThis.TextDecoder === "undefined") {
  globalThis.TextDecoder = function TextDecoder(label, opts) {
    this.fatal = !!(opts && opts.fatal);
  };
  globalThis.TextDecoder.prototype.decode = function (bytes) {
    if (bytes == null) return "";
    if (!(bytes instanceof Uint8Array))
      bytes = new Uint8Array(
        bytes.buffer || bytes,
        bytes.byteOffset || 0,
        bytes.byteLength,
      );
    return __grpcview_utf8_decode(bytes, this.fatal);
  };
}
