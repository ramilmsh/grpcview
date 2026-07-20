/*
 * qjs_wasm.c — host<->guest ABI shim for evaluating a JS source string inside
 * QuickJS compiled to wasm32-wasi (reactor), PLUS the capability seam: Layer B
 * (JS<->C marshalling) that reaches Layer A (Go host functions) through narrow,
 * purpose-built wasm imports. See docs/design/quickjs-wasm-capabilities-spike.md.
 *
 * Host-visible exports (host -> guest):
 *   void*    qjs_malloc(size_t n)                     - allocate n bytes in wasm memory
 *   void     qjs_free(void* p)                        - free a qjs_malloc'd / result ptr
 *   uint8_t* qjs_eval(const char* src, int len, uint64_t mem_limit) -> result ptr
 *
 * Capability imports (guest -> host); the ONLY things that cross to real I/O:
 *   uint8_t* host_fs_read(const uint8_t* req, int req_len)   -> result ptr
 *   uint8_t* host_net_fetch(const uint8_t* req, int req_len) -> result ptr
 *
 * THE UNIFORM ABI. Every capability import has the same shape and the same result
 * envelope as qjs_eval — one convention, reused in both directions:
 *   request  : (ptr,len) into linear memory (bytes the guest already holds; no copy)
 *   response : host allocates a result buffer IN GUEST MEMORY by calling the guest's
 *              own qjs_malloc (the wasm component-model cabi_realloc pattern), writes
 *              [tag u8][len u32 LE][payload], and returns that ptr. The guest reads it
 *              and qjs_free()s it — symmetric with how the Go host reads the qjs_eval
 *              result. tag 0 = value, tag 1 = error(message). A NULL return means the
 *              host could not even allocate (host OOM) -> we raise InternalError.
 *
 * Host failures therefore surface as CATCHABLE JS exceptions (tag 1 -> JS_ThrowTypeError
 * with the host's message), never a silent zero/empty string.
 *
 * Enforcement (grant check + path scope + the syscall) lives entirely in the Go host
 * functions, OUTSIDE the sandbox — this file only validates/marshals and never does I/O.
 */
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include "quickjs.h"

/* Result-buffer ABI, shared with the Go side (engine.go mirrors these). */
#define QJS_RESULT_HEADER 5 /* tag u8 (1) + payload length u32 LE (4) */
#define QJS_TAG_VALUE 0     /* payload is the String()-ified value / result bytes */
#define QJS_TAG_THROW 1     /* payload is an error / exception message */

/* ---- Layer A imports: the narrow, purpose-built host boundary --------------------
 * Declared with import_module/import_name so wasm-ld emits them as imports the Go host
 * satisfies at instantiation. They are ALWAYS declared; the grant decides at call time
 * whether the Go side does real I/O or refuses (a deny-stub). See the spike doc's
 * "two independent gates". */
__attribute__((import_module("env"), import_name("host_fs_read")))
extern uint8_t *host_fs_read(const uint8_t *req, int req_len);

__attribute__((import_module("env"), import_name("host_net_fetch")))
extern uint8_t *host_net_fetch(const uint8_t *req, int req_len);

__attribute__((export_name("qjs_malloc")))
void *qjs_malloc(size_t n) { return malloc(n); }

__attribute__((export_name("qjs_free")))
void qjs_free(void *p) { free(p); }

/* Build a [tag|len|payload] result buffer the host can read back in one shot. */
static uint8_t *pack_result(uint8_t tag, const char *data, size_t len) {
    if (len > SIZE_MAX - QJS_RESULT_HEADER) return NULL; /* header+len would overflow */
    uint8_t *buf = (uint8_t *)malloc(QJS_RESULT_HEADER + len);
    if (!buf) return NULL; /* host treats a null return as OOM */
    buf[0] = tag;
    uint32_t n = (uint32_t)len;
    memcpy(buf + 1, &n, sizeof n); /* wasm is little-endian, matching the Go decode */
    if (len) memcpy(buf + QJS_RESULT_HEADER, data, len);
    return buf;
}

/* Pack a string-literal error message; its length is taken from the literal so
 * it cannot drift out of sync the way a hand-counted length would. */
#define pack_err(msg) pack_result(QJS_TAG_THROW, (msg), sizeof(msg) - 1)

/* ---- Layer B: the ONE uniform helper every capability shim reuses -----------------
 * Turn a host-returned result buffer into a JS value, or throw a catchable JS
 * exception, then free the buffer. This is the seam where a Go-side failure becomes
 * a JS `throw` the script can catch. */
static JSValue unpack_host_result(JSContext *ctx, uint8_t *res) {
    if (!res) return JS_ThrowInternalError(ctx, "grpcview host: out of memory");
    uint8_t tag = res[0];
    uint32_t len;
    memcpy(&len, res + 1, sizeof len); /* little-endian, matching the Go encode */
    const char *payload = (const char *)(res + QJS_RESULT_HEADER);
    JSValue out;
    if (tag == QJS_TAG_THROW)
        out = JS_ThrowTypeError(ctx, "%.*s", (int)len, payload); /* -> catchable in JS */
    else
        out = JS_NewStringLen(ctx, payload, len);
    qjs_free(res);
    return out;
}

/* std/fs.readFile(path) Layer-B shim: validate + marshal, then exactly one import
 * call. Does NO I/O itself. The request is the path's bytes, which JS_ToCStringLen
 * already placed in linear memory, so we pass them straight through — no extra copy. */
static JSValue js_host_fs_read(JSContext *ctx, JSValueConst this_val, int argc, JSValueConst *argv) {
    (void)this_val;
    if (argc < 1) return JS_ThrowTypeError(ctx, "fs read: path required");
    size_t len;
    const char *path = JS_ToCStringLen(ctx, &len, argv[0]);
    if (!path) return JS_EXCEPTION; /* ToString already threw */
    uint8_t *res = host_fs_read((const uint8_t *)path, (int)len);
    JS_FreeCString(ctx, path);
    return unpack_host_result(ctx, res);
}

/* std/net.fetch(url) Layer-B shim — identical shape to fs, proving the one ABI
 * generalizes to a second capability. The Go side is stubbed (no real network). */
static JSValue js_host_net_fetch(JSContext *ctx, JSValueConst this_val, int argc, JSValueConst *argv) {
    (void)this_val;
    if (argc < 1) return JS_ThrowTypeError(ctx, "net fetch: url required");
    size_t len;
    const char *url = JS_ToCStringLen(ctx, &len, argv[0]);
    if (!url) return JS_EXCEPTION;
    uint8_t *res = host_net_fetch((const uint8_t *)url, (int)len);
    JS_FreeCString(ctx, url);
    return unpack_host_result(ctx, res);
}

/* Register the Layer-B marshallers as globals. They are surfaced to scripts only via
 * the injected node:* shims (see engine.go's Bundle); a script that never imports the
 * matching module never references them. Registering them unconditionally is safe: the
 * privilege lives in the import (Layer A), not here. */
static void register_capabilities(JSContext *ctx) {
    JSValue g = JS_GetGlobalObject(ctx);
    JS_SetPropertyStr(ctx, g, "__grpcview_fs_read",
                      JS_NewCFunction(ctx, js_host_fs_read, "__grpcview_fs_read", 1));
    JS_SetPropertyStr(ctx, g, "__grpcview_net_fetch",
                      JS_NewCFunction(ctx, js_host_net_fetch, "__grpcview_net_fetch", 1));
    JS_FreeValue(ctx, g);
}

__attribute__((export_name("qjs_eval")))
uint8_t *qjs_eval(const char *src, int src_len, uint64_t mem_limit) {
    /* src_len crosses the ABI as a signed int; a negative value would wrap the
     * (size_t)src_len math below into a huge malloc + ~4 GiB OOB memcpy. Reject it. */
    if (src_len < 0) return pack_err("negative source length");

    JSRuntime *rt = JS_NewRuntime();
    if (!rt) return pack_err("cannot create runtime");

    /* THE INNER BOUND: QuickJS refuses any allocation that would push total bytes
     * past mem_limit and throws, before the host's page ceiling is even reached.
     * size_t is 32-bit on wasm32, so CLAMP rather than truncate the uint64: an
     * unclamped cast turns e.g. a 4 GiB request (0x1_0000_0000) into 0, and a limit
     * of 0 makes QuickJS reject EVERY allocation (0 is not a "disabled" sentinel —
     * that is (size_t)-1), so JS_NewContext below would fail. Clamping to SIZE_MAX
     * means "the entire 4 GiB wasm32 address space", i.e. effectively unbounded. */
    if (mem_limit) {
        size_t lim = mem_limit > (uint64_t)SIZE_MAX ? SIZE_MAX : (size_t)mem_limit;
        JS_SetMemoryLimit(rt, lim);
    }

    JSContext *ctx = JS_NewContext(rt);
    if (!ctx) { JS_FreeRuntime(rt); return pack_err("cannot create context"); }

    register_capabilities(ctx); /* attach the Layer-B marshallers before eval */

    /* JS_Eval wants a NUL-terminated C string. */
    char *csrc = (char *)malloc((size_t)src_len + 1);
    if (!csrc) { JS_FreeContext(ctx); JS_FreeRuntime(rt); return pack_err("oom"); }
    if (src_len) memcpy(csrc, src, (size_t)src_len); /* src may be NULL when src_len==0 */
    csrc[src_len] = '\0';

    JSValue val = JS_Eval(ctx, csrc, (size_t)src_len, "<eval>", JS_EVAL_TYPE_GLOBAL);
    free(csrc);

    uint8_t tag;
    JSValue str_val;
    if (JS_IsException(val)) {
        tag = QJS_TAG_THROW;
        JSValue exc = JS_GetException(ctx);
        str_val = JS_ToString(ctx, exc);
        JS_FreeValue(ctx, exc);
    } else {
        tag = QJS_TAG_VALUE;
        str_val = JS_ToString(ctx, val);
    }

    uint8_t *out;
    if (JS_IsException(str_val)) {
        out = pack_err("<unstringifiable result>");
    } else {
        size_t plen = 0;
        const char *pstr = JS_ToCStringLen(ctx, &plen, str_val);
        out = pstr ? pack_result(tag, pstr, plen) : pack_err("<null cstring>");
        JS_FreeCString(ctx, pstr); /* NULL-safe: JS_FreeCString early-returns on NULL */
    }

    JS_FreeValue(ctx, str_val);
    JS_FreeValue(ctx, val);
    JS_FreeContext(ctx);
    JS_FreeRuntime(rt);
    return out;
}
