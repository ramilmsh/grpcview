// TextEncoder/TextDecoder polyfill — this QuickJS build has no WHATWG APIs, and
// @bufbuild/protobuf's wire/text-encoding.js needs them.
//
// MUST be eval'd as its own script, before the codegen bundle (see codegen_bundle.go) — never
// prepended into the same bundled module. @bufbuild/protobuf/wkt parses an embedded
// FileDescriptorProto at its own module top level, before a same-bundle polyfill assignment
// would run.
if (typeof globalThis.TextEncoder === "undefined") {
  globalThis.TextEncoder = function TextEncoder() {};
  globalThis.TextEncoder.prototype.encode = function (str) {
    str = String(str == null ? "" : str);
    var bytes = [];
    for (var i = 0; i < str.length; i++) {
      var code = str.codePointAt(i);
      if (code > 0xffff) i++;
      if (code < 0x80) {
        bytes.push(code);
      } else if (code < 0x800) {
        bytes.push(0xc0 | (code >> 6), 0x80 | (code & 0x3f));
      } else if (code < 0x10000) {
        bytes.push(
          0xe0 | (code >> 12),
          0x80 | ((code >> 6) & 0x3f),
          0x80 | (code & 0x3f),
        );
      } else {
        bytes.push(
          0xf0 | (code >> 18),
          0x80 | ((code >> 12) & 0x3f),
          0x80 | ((code >> 6) & 0x3f),
          0x80 | (code & 0x3f),
        );
      }
    }
    return Uint8Array.from(bytes);
  };
}
if (typeof globalThis.TextDecoder === "undefined") {
  globalThis.TextDecoder = function TextDecoder(label, opts) {
    this.fatal = !!(opts && opts.fatal);
  };
  globalThis.TextDecoder.prototype.decode = function (bytes) {
    if (bytes == null) return "";
    var arr =
      bytes instanceof Uint8Array
        ? bytes
        : new Uint8Array(
            bytes.buffer || bytes,
            bytes.byteOffset || 0,
            bytes.byteLength,
          );
    var out = "",
      i = 0;
    while (i < arr.length) {
      var b0 = arr[i++],
        cp,
        extra;
      if (b0 < 0x80) {
        cp = b0;
        extra = 0;
      } else if ((b0 & 0xe0) === 0xc0) {
        cp = b0 & 0x1f;
        extra = 1;
      } else if ((b0 & 0xf0) === 0xe0) {
        cp = b0 & 0x0f;
        extra = 2;
      } else if ((b0 & 0xf8) === 0xf0) {
        cp = b0 & 0x07;
        extra = 3;
      } else {
        if (this.fatal) throw new TypeError("invalid UTF-8");
        cp = 0xfffd;
        extra = 0;
      }
      for (var k = 0; k < extra; k++) {
        var b = arr[i++];
        if (b === undefined || (b & 0xc0) !== 0x80) {
          if (this.fatal) {
            throw new TypeError("invalid UTF-8");
          }
          cp = 0xfffd;
          break;
        }
        cp = (cp << 6) | (b & 0x3f);
      }
      if (cp > 0xffff) {
        cp -= 0x10000;
        out += String.fromCharCode(0xd800 + (cp >> 10), 0xdc00 + (cp & 0x3ff));
      } else {
        out += String.fromCharCode(cp);
      }
    }
    return out;
  };
}
