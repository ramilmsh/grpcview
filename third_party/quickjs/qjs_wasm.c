/*
 * qjs_wasm.c — host<->guest ABI for evaluating JS inside QuickJS compiled to
 * wasm32-wasi (reactor). See docs/design/quickjs-wasm-spike.md and
 * docs/design/quickjs-wasm-capabilities-spike.md.
 *
 * Beyond the capability seam (Layer B <-> Layer A imports), this exposes a small
 * STATE-MACHINE ABI so the Go host can drive ASYNC execution step by step: eval,
 * then pump the microtask queue until the top-level promise settles (or the host's
 * wall-clock deadline fires), then marshal the settled value. Driving the pump from
 * Go — rather than looping inside one eval call — is what will let a future network
 * capability resolve a guest promise from off-thread work (the ticket pattern in the
 * capabilities spike doc). The wasm instance is single-threaded and the Go Instance
 * is never used concurrently, so one runtime + context + held value are kept as file
 * globals rather than threaded through a handle.
 *
 * Host-visible exports (host -> guest):
 *   void*    qjs_malloc(size_t n)                 - allocate n bytes of wasm memory
 *   void     qjs_free(void* p)                    - free a qjs_malloc'd / result ptr
 *   int      qjs_new(uint64_t mem_limit)          - create this instance's runtime+context,
 *                                                   set the inner heap cap, register the
 *                                                   capability + console globals. 0 / -1.
 *   void     qjs_dispose(void)                    - tear the runtime+context down.
 *   int      qjs_eval(const char* src, int len, int async) -> status
 *   int      qjs_pump(void)                                 -> status
 *   uint8_t* qjs_result(int as_json)                        -> [tag u8][len u32 LE][payload]
 *
 *   status : 0 QJS_DONE    - a settled value is held (fetch it with qjs_result)
 *            1 QJS_PENDING - the top-level promise is unsettled; pump again (or, later,
 *                            feed a resolved host ticket) — the host bounds the loop
 *            2 QJS_ERROR   - an exception / rejection is held (qjs_result tags it a throw)
 *
 *   result tag : 0 value (as_json ? JSON text : String()-ified)
 *                1 throw ("message" or "message\nstack", UTF-8)
 *                2 undefined (no payload; the value had no JSON form)
 *
 * Capability imports (guest -> host); the ONLY things that cross to real I/O:
 *   uint8_t* host_fs_read(const uint8_t* req, int req_len)   -> result ptr
 *   uint8_t* host_net_fetch(const uint8_t* req, int req_len) -> result ptr
 *   uint8_t* host_invoke(const uint8_t* req, int req_len)    -> result ptr
 *   uint8_t* host_random(int n)                              -> result ptr (JSON int array)
 *   void     host_console(int level, const uint8_t* msg, int len) - fire-and-forget sink
 *
 * Enforcement (grant + scope + syscall) lives entirely in the Go host functions,
 * OUTSIDE the sandbox; this file only validates/marshals and never does I/O.
 */
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include "quickjs.h"

/* Result-buffer ABI, shared with the Go side (engine.go mirrors these). */
#define QJS_RESULT_HEADER 5 /* tag u8 (1) + payload length u32 LE (4) */
#define QJS_TAG_VALUE 0     /* payload is the value (JSON text or String()-ified) */
#define QJS_TAG_THROW 1     /* payload is "message" or "message\nstack" for an exception */
#define QJS_TAG_UNDEFINED 2 /* no payload; the result had no JSON representation */

/* qjs_eval / qjs_pump status codes (mirrored in engine.go). */
#define QJS_DONE 0
#define QJS_PENDING 1
#define QJS_ERROR 2

/* ---- Layer A imports: the narrow, purpose-built host boundary --------------------
 * Declared with import_module/import_name so wasm-ld emits them as imports the Go host
 * satisfies at instantiation. They are ALWAYS declared; the grant decides at call time
 * whether the Go side does real I/O or refuses (a deny-stub). */
__attribute__((import_module("env"), import_name("host_fs_read")))
extern uint8_t *host_fs_read(const uint8_t *req, int req_len);

__attribute__((import_module("env"), import_name("host_net_fetch")))
extern uint8_t *host_net_fetch(const uint8_t *req, int req_len);

/* gv.invoke's bridge: req is the JSON {path, params} envelope the JS gv IIFE
 * (marshal.go's gvInvokeShim) marshalled; the resolved side of the pipeline lives in
 * service/workspace, reached via a ctx-carried Invoker (service/scripting/engine.go), not
 * this file — this shim only marshals/unmarshals, same as fs and fetch. */
__attribute__((import_module("env"), import_name("host_invoke")))
extern uint8_t *host_invoke(const uint8_t *req, int req_len);

/* crypto.getRandomValues's bridge: n is the requested byte count; the host writes real
 * entropy (Go's crypto/rand, NOT the deterministic Math.random PRNG) and returns it as a
 * JSON array of byte values, same result envelope as fs/fetch/invoke. */
__attribute__((import_module("env"), import_name("host_random")))
extern uint8_t *host_random(int n);

/* Fire-and-forget log sink: no result envelope. console output cannot meaningfully
 * fail in a way a script should catch, so it returns void and the Go host buffers it. */
__attribute__((import_module("env"), import_name("host_console")))
extern void host_console(int level, const uint8_t *msg, int len);

__attribute__((export_name("qjs_malloc")))
void *qjs_malloc(size_t n) { return malloc(n); }

__attribute__((export_name("qjs_free")))
void qjs_free(void *p) { free(p); }

/* Build a [tag|len|payload] result buffer the host reads back in one shot. */
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

/* Pack a string-literal error whose length comes from the literal (can't drift). */
#define pack_err(msg) pack_result(QJS_TAG_THROW, (msg), sizeof(msg) - 1)

/* ---- Layer B: capability marshallers ---------------------------------------------
 * Turn a host-returned result buffer into a JS value, or throw a catchable JS
 * exception, then free the buffer. This is the seam where a Go-side failure becomes a
 * JS `throw` the script can catch. */
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
 * call. Does NO I/O itself. The path bytes are already in linear memory, so we pass
 * them straight through — no extra copy. */
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

/* std/net.fetch(url) Layer-B shim — identical shape to fs. The Go side is stubbed. */
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

/* gv.invoke(path, params) Layer-B shim — identical shape to fetch/fs. The JS-side gv IIFE
 * (marshal.go's gvInvokeShim) has already JSON-encoded {path, params} into one string;
 * this shim passes it straight through to the host import unchanged, and does no I/O
 * itself. */
static JSValue js_host_invoke(JSContext *ctx, JSValueConst this_val, int argc, JSValueConst *argv) {
    (void)this_val;
    if (argc < 1) return JS_ThrowTypeError(ctx, "invoke: request required");
    size_t len;
    const char *req = JS_ToCStringLen(ctx, &len, argv[0]);
    if (!req) return JS_EXCEPTION; /* ToString already threw */
    uint8_t *res = host_invoke((const uint8_t *)req, (int)len);
    JS_FreeCString(ctx, req);
    return unpack_host_result(ctx, res);
}

/* crypto.getRandomValues(n) Layer-B shim: n is a byte count (int), not a buffer — no
 * guest memory to pass through, unlike fs/fetch/invoke's string args. */
static JSValue js_host_random(JSContext *ctx, JSValueConst this_val, int argc, JSValueConst *argv) {
    (void)this_val;
    if (argc < 1) return JS_ThrowTypeError(ctx, "random: byte count required");
    int32_t n;
    if (JS_ToInt32(ctx, &n, argv[0]) < 0) return JS_EXCEPTION; /* ToInt32 already threw */
    uint8_t *res = host_random(n);
    return unpack_host_result(ctx, res);
}

/* console sink Layer-B shim: __grpcview_console(level:int, message:string). The JS
 * `console` object (level mapping + argument formatting) is assembled in the Go-side
 * prelude; this shim only forwards the already-formatted line to the host import. */
static JSValue js_console(JSContext *ctx, JSValueConst this_val, int argc, JSValueConst *argv) {
    (void)this_val;
    /* console is fire-and-forget: it must never throw or leave a pending exception on the
     * (possibly reused) context. The normal console.* object pre-formats args to an int
     * level + a string, but this global is reachable directly, so a throwing valueOf /
     * toString on an argument must be caught and cleared here. */
    int level = 0;
    if (argc >= 1 && JS_ToInt32(ctx, &level, argv[0]) < 0) {
        JS_FreeValue(ctx, JS_GetException(ctx)); /* clear + default the level */
        level = 0;
    }
    if (argc >= 2) {
        size_t len;
        const char *msg = JS_ToCStringLen(ctx, &len, argv[1]);
        if (msg) {
            host_console(level, (const uint8_t *)msg, (int)len);
            JS_FreeCString(ctx, msg);
        } else {
            JS_FreeValue(ctx, JS_GetException(ctx)); /* toString threw — clear, don't forward */
        }
    }
    return JS_UNDEFINED;
}

/* Register the Layer-B marshallers + the console shim as globals. Surfaced to scripts
 * only via the injected prelude / node:* shims; registering unconditionally is safe —
 * the privilege lives in the import (Layer A), not here. */
static void register_globals(JSContext *ctx) {
    JSValue g = JS_GetGlobalObject(ctx);
    JS_SetPropertyStr(ctx, g, "__grpcview_fs_read",
                      JS_NewCFunction(ctx, js_host_fs_read, "__grpcview_fs_read", 1));
    JS_SetPropertyStr(ctx, g, "__grpcview_net_fetch",
                      JS_NewCFunction(ctx, js_host_net_fetch, "__grpcview_net_fetch", 1));
    JS_SetPropertyStr(ctx, g, "__grpcview_invoke",
                      JS_NewCFunction(ctx, js_host_invoke, "__grpcview_invoke", 1));
    JS_SetPropertyStr(ctx, g, "__grpcview_random",
                      JS_NewCFunction(ctx, js_host_random, "__grpcview_random", 1));
    JS_SetPropertyStr(ctx, g, "__grpcview_console",
                      JS_NewCFunction(ctx, js_console, "__grpcview_console", 2));
    JS_FreeValue(ctx, g);
}

/* ---- Per-instance runtime state -------------------------------------------------- */
static JSRuntime *g_rt;
static JSContext *g_ctx;
static JSValue g_completion;  /* the held value: a settled result, an error reason, or a
                               * still-pending top-level promise (see g_pending). */
static int g_have_completion; /* 1 if g_completion holds a live (freeable) value */
static int g_is_error;        /* 1 if g_completion is an exception / rejection reason */
static int g_pending;         /* 1 if g_completion is a top-level promise not yet settled */
static int g_async;           /* 1 if the current eval used JS_EVAL_FLAG_ASYNC (see below) */

/* Drop any held completion value. Safe to call repeatedly. */
static void clear_completion(void) {
    if (g_have_completion) JS_FreeValue(g_ctx, g_completion);
    g_completion = JS_UNDEFINED;
    g_have_completion = 0;
    g_is_error = 0;
    g_pending = 0;
}

__attribute__((export_name("qjs_dispose")))
void qjs_dispose(void) {
    if (g_ctx) clear_completion();
    if (g_ctx) { JS_FreeContext(g_ctx); g_ctx = NULL; }
    if (g_rt) { JS_FreeRuntime(g_rt); g_rt = NULL; }
}

__attribute__((export_name("qjs_new")))
int qjs_new(uint64_t mem_limit) {
    qjs_dispose(); /* defensively reset any prior state */

    g_rt = JS_NewRuntime();
    if (!g_rt) return -1;

    /* THE INNER BOUND. QuickJS refuses any allocation that would push total bytes past
     * mem_limit and throws, before the host's page ceiling is reached. size_t is 32-bit
     * on wasm32, so CLAMP rather than truncate the uint64: an unclamped cast turns e.g.
     * a 4 GiB request (0x1_0000_0000) into 0, and a limit of 0 makes QuickJS reject
     * EVERY allocation (0 is not a "disabled" sentinel — that is (size_t)-1), so
     * JS_NewContext below would fail. Clamping to SIZE_MAX means "the whole 4 GiB
     * wasm32 address space", i.e. effectively unbounded. */
    if (mem_limit) {
        size_t lim = mem_limit > (uint64_t)SIZE_MAX ? SIZE_MAX : (size_t)mem_limit;
        JS_SetMemoryLimit(g_rt, lim);
    }

    g_ctx = JS_NewContext(g_rt);
    if (!g_ctx) { JS_FreeRuntime(g_rt); g_rt = NULL; return -1; }

    register_globals(g_ctx);
    g_completion = JS_UNDEFINED;
    g_have_completion = 0;
    g_is_error = 0;
    g_pending = 0;
    g_async = 0;
    return 0;
}

/* Classify a freshly produced completion value into the state machine. Takes ownership
 * of v and returns the status. */
static int set_completion(JSValue v) {
    clear_completion();

    if (JS_IsException(v)) {
        /* A synchronous throw / compile error: JS_EXCEPTION is not itself a heap value;
         * pull the pending exception object out of the context (owned). */
        g_completion = JS_GetException(g_ctx);
        g_have_completion = 1;
        g_is_error = 1;
        return QJS_ERROR;
    }

    /* Is v a promise? Only objects can be, and JS_PromiseState returns -1 otherwise —
     * but JSPromiseStateEnum is UNSIGNED, so that -1 wraps to a huge value and a naive
     * `ps < 0` never fires (it would then misclassify every plain value/object as a
     * settled promise and JS_PromiseResult it to undefined). Gate on JS_IsObject and
     * only act on an EXACT known promise state; anything else is a plain settled value. */
    int ps = -1;
    if (JS_IsObject(v)) ps = (int)JS_PromiseState(g_ctx, v);

    if (ps == JS_PROMISE_PENDING) {
        g_completion = v; /* keep the promise itself; pump advances it */
        g_have_completion = 1;
        g_pending = 1;
        return QJS_PENDING;
    }
    if (ps == JS_PROMISE_FULFILLED || ps == JS_PROMISE_REJECTED) {
        /* Already settled: unwrap to the value/reason and drop the promise. */
        JSValue inner = JS_PromiseResult(g_ctx, v); /* owned (a dup) */
        JS_FreeValue(g_ctx, v);
        g_completion = inner;
        g_have_completion = 1;
        if (ps == JS_PROMISE_REJECTED) { g_is_error = 1; return QJS_ERROR; }
        return QJS_DONE;
    }

    /* Not a promise: an already-settled plain value. */
    g_completion = v;
    g_have_completion = 1;
    return QJS_DONE;
}

__attribute__((export_name("qjs_eval")))
int qjs_eval(const char *src, int src_len, int async) {
    if (!g_ctx) return QJS_ERROR;
    /* src_len crosses the ABI as a signed int; a negative value would wrap the
     * (size_t)src_len math below into a huge malloc + OOB memcpy. Reject it. */
    if (src_len < 0) return set_completion(JS_ThrowTypeError(g_ctx, "negative source length"));

    /* JS_Eval wants a NUL-terminated C string. */
    char *csrc = (char *)malloc((size_t)src_len + 1);
    if (!csrc) return set_completion(JS_ThrowInternalError(g_ctx, "oom copying source"));
    if (src_len) memcpy(csrc, src, (size_t)src_len); /* src may be NULL when src_len==0 */
    csrc[src_len] = '\0';

    int flags = JS_EVAL_TYPE_GLOBAL;
    g_async = async ? 1 : 0;
    if (async) flags |= JS_EVAL_FLAG_ASYNC; /* allow top-level await; JS_Eval -> promise */
    JSValue v = JS_Eval(g_ctx, csrc, (size_t)src_len, "<script>", flags);
    free(csrc);
    return set_completion(v);
}

__attribute__((export_name("qjs_pump")))
int qjs_pump(void) {
    if (!g_ctx) return QJS_ERROR;

    /* Drain the job (microtask) queue. Each job may enqueue more, so loop until empty.
     * A job that throws leaves a pending exception on its context; clear it so it can't
     * corrupt the JS_PromiseState calls below — the top-level promise state is the
     * source of truth for the run's outcome, not an individual job's exception. */
    JSContext *jc;
    for (;;) {
        int r = JS_ExecutePendingJob(g_rt, &jc);
        if (r == 0) break; /* no more pending jobs */
        if (r < 0) {
            JSValue e = JS_GetException(jc);
            JS_FreeValue(jc, e);
        }
    }

    if (!g_pending) return g_is_error ? QJS_ERROR : QJS_DONE;

    /* Re-check the stored top-level promise now the queue is drained. */
    JSPromiseStateEnum ps = JS_PromiseState(g_ctx, g_completion);
    if (ps == JS_PROMISE_PENDING) return QJS_PENDING; /* nothing left to run it -> caller decides */

    JSValue inner = JS_PromiseResult(g_ctx, g_completion); /* owned (a dup) */
    JS_FreeValue(g_ctx, g_completion);
    g_completion = inner;
    g_pending = 0;
    if (ps == JS_PROMISE_REJECTED) { g_is_error = 1; return QJS_ERROR; }
    return QJS_DONE;
}

/* Marshal an error value into a throw buffer: "message" or "message\nstack". */
static uint8_t *pack_error(JSContext *ctx, JSValue err) {
    /* Both JS_ToCString (invokes err.toString) and the "stack" getter can themselves
     * throw; on failure they return NULL / an exception and leave a pending exception on
     * the context. Clear it each time, or it would linger on a reused long-lived context. */
    const char *msg = JS_ToCString(ctx, err); /* e.g. "TypeError: x is not a function" */
    if (!msg) JS_FreeValue(ctx, JS_GetException(ctx));

    const char *stack = NULL;
    JSValue stackv = JS_UNDEFINED;
    if (JS_IsObject(err)) {
        stackv = JS_GetPropertyStr(ctx, err, "stack");
        if (JS_IsException(stackv)) {
            JS_FreeValue(ctx, JS_GetException(ctx)); /* a throwing "stack" getter */
            stackv = JS_UNDEFINED;
        } else if (!JS_IsUndefined(stackv)) {
            stack = JS_ToCString(ctx, stackv);
            if (!stack) JS_FreeValue(ctx, JS_GetException(ctx));
        }
    }

    uint8_t *out = NULL;
    if (msg && stack && *stack) {
        size_t ml = strlen(msg), sl = strlen(stack);
        if (ml <= SIZE_MAX - 1 - sl) { /* mirror pack_result's overflow guard */
            char *buf = (char *)malloc(ml + 1 + sl);
            if (buf) {
                memcpy(buf, msg, ml);
                buf[ml] = '\n';
                memcpy(buf + ml + 1, stack, sl);
                out = pack_result(QJS_TAG_THROW, buf, ml + 1 + sl);
                free(buf);
            }
        }
    }
    if (!out && msg) out = pack_result(QJS_TAG_THROW, msg, strlen(msg));
    if (!out) out = pack_err("uncaught (throw with no usable message)");

    if (stack) JS_FreeCString(ctx, stack);
    JS_FreeValue(ctx, stackv);
    if (msg) JS_FreeCString(ctx, msg);
    return out;
}

__attribute__((export_name("qjs_result")))
uint8_t *qjs_result(int as_json) {
    if (!g_ctx) return pack_err("no context");
    if (!g_have_completion) return pack_result(QJS_TAG_UNDEFINED, "", 0);

    if (g_is_error) {
        /* A rejection reason is NOT wrapped by the async flag — marshal it directly. */
        uint8_t *out = pack_error(g_ctx, g_completion);
        clear_completion();
        return out;
    }

    /* An async eval (JS_EVAL_FLAG_ASYNC) fulfils its promise with { value: <completion> };
     * unwrap that one layer so the host marshals the actual value, not the wrapper. A sync
     * eval (legacy string path) is not wrapped, so use g_completion directly. */
    JSValue val = g_completion;
    JSValue unwrapped = JS_UNDEFINED;
    int did_unwrap = 0;
    if (g_async) {
        unwrapped = JS_GetPropertyStr(g_ctx, g_completion, "value");
        val = unwrapped;
        did_unwrap = 1;
    }

    uint8_t *out;
    if (as_json) {
        JSValue jsonv = JS_JSONStringify(g_ctx, val, JS_UNDEFINED, JS_UNDEFINED);
        if (JS_IsException(jsonv)) {
            JSValue e = JS_GetException(g_ctx); /* e.g. circular structure / throwing toJSON */
            out = pack_error(g_ctx, e);
            JS_FreeValue(g_ctx, e);
        } else if (JS_IsUndefined(jsonv)) {
            out = pack_result(QJS_TAG_UNDEFINED, "", 0); /* undefined / function / symbol top level */
        } else {
            size_t len;
            const char *s = JS_ToCStringLen(g_ctx, &len, jsonv);
            out = s ? pack_result(QJS_TAG_VALUE, s, len) : pack_err("<null cstring>");
            JS_FreeCString(g_ctx, s);
        }
        JS_FreeValue(g_ctx, jsonv);
    } else {
        JSValue sv = JS_ToString(g_ctx, val);
        if (JS_IsException(sv)) {
            JSValue e = JS_GetException(g_ctx);
            JS_FreeValue(g_ctx, e);
            out = pack_err("<unstringifiable result>");
        } else {
            size_t len;
            const char *s = JS_ToCStringLen(g_ctx, &len, sv);
            out = s ? pack_result(QJS_TAG_VALUE, s, len) : pack_err("<null cstring>");
            JS_FreeCString(g_ctx, s);
        }
        JS_FreeValue(g_ctx, sv);
    }

    if (did_unwrap) JS_FreeValue(g_ctx, unwrapped);
    clear_completion();
    return out;
}
