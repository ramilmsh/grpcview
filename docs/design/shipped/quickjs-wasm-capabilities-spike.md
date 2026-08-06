# QuickJS-in-WASM capability layer — spike findings

Status: **spike complete; the capability layer it proved is in the engine.** Note that
*grant management* — the UI for deciding what a script may reach — is a separate, unbuilt
concern (see [`scripting-ui-plan.md`](./scripting-ui-plan.md) S4–S6):
everything shipped today runs fully sandboxed with no grant.

Builds on [`quickjs-wasm-spike.md`](./quickjs-wasm-spike.md). The
capability / host-function layer is proven end-to-end: an inert Node-shaped module runs
with **no grant**; `node:fs.readFileSync` reaches a real file through **one narrow Go
import** with a **path allowlist**; and an ungranted script is denied by **two
independent gates**. All demonstrated by `//service/scripting:scripting_test` (8 new
capability tests alongside the 8 original bound tests, all passing).

**Recommendation: GO.** The right shape — narrow, Go-enforced imports instead of the WASI
all-or-nothing shortcut — is small and clean: two `__attribute__((import_name))` externs
+ two Layer-B marshallers in C, one uniform result envelope reused from the eval path, and
~150 lines of Go (host functions + grant + the esbuild-stand-in bundler). Capability I/O
never enters the module; the module never holds a privilege the script can reach.

## TL;DR decisions

| Decision | Choice | Deciding reason |
| --- | --- | --- |
| **ABI convention** | Tagged buffer `[tag u8][len u32 LE][payload]`; host allocates the result **in guest memory via a re-entrant `qjs_malloc`**; `tag 1` → catchable JS throw | Reuses the *exact* envelope `qjs_eval` already returns — one layout, one helper each side. Carries the error **message in-band**, so a host failure becomes `try/catch`-able with a reason. Packed-i64 has nowhere to put the message. |
| **`std/*` exposure** | Real ES **`import`** of **Node-named** modules (`node:fs`, `node:path`), resolved at bundle time; grant-gated injection | The `import` *is* the statically-visible capability request (drives consent + `.d.ts`). Node names/shapes let **third-party npm libraries resolve against our shims** (the operator's goal). A global object is invisible to static scanning. |
| **Crossing shape** | **Purpose-built import per op** (`host_fs_read`, `host_net_fetch`) | The wasm import section becomes an auditable manifest of what the module *can* request; each Go host func is narrow and readable. (Generic `host_call(capId,…)` dispatch was the considered alternative — rejected for weaker per-cap auditability.) |
| **Gates** | **Two independent**: (1) bundle-time module injection, (2) call-time host enforcement | Either alone denies. Gate 1 gives "no call site at all"; Gate 2 refuses in Go before any syscall. |
| **Grant → instance** | Compile once · **fresh instance per run** · grant carried on `context.Context` · bundle injection per grant | One runtime ⇒ one shared host module (wasm namespace reality); per-run scope rides on ctx (unforgeable) + fresh instance = per-run isolation in effect. |
| **Async** | **Synchronous fs** now; Promise + ticket + job-pump **designed** for net | A blocking Go host call is trivial and enough for fs; only a truly async cap (net) needs the event-loop machinery. |

## The two-layer architecture (+ the inert layer that needs neither)

```
Layer 0  INERT — pure computation, bundled into the JS, NO import, NO grant
  node:path (join/basename), node:buffer, crypto hashing, text codecs …
  → always injected; works with an empty grant. (Demo: node:path.)
──────────────────────────────────────────────────────────────────────────────────
JS      import fs from "node:fs";  fs.readFileSync("/allowed/x.txt")
          │  (the import IS the capability request — statically visible)
Layer B  QuickJS native fn via JS_NewCFunction  (qjs_wasm.c: js_host_fs_read)
          validate args + marshal into linear memory · does NO I/O
          │  exactly one narrow import crosses
          ▼
Layer A  host_fs_read(reqPtr, reqLen) -> resPtr        (wazero host function, Go)
          grant check + path allowlist + os.ReadFile   — the ONLY real I/O,
          outside the sandbox, where the script cannot reach it
          │  writes [tag|len|payload] back into guest memory (via qjs_malloc)
          ▼
JS      returns a string, or THROWS a catchable exception carrying the Go reason
```

- **Layer 0 (inert)** carries no privilege. Recommendation: **vendored JS polyfills**
  bundled by esbuild (C only where a strong C lib exists). Demoed with a pure-JS
  `node:path`; it runs under `Grant{}`.
- **Layer B (JS↔C)** is ergonomic C registered with `JS_NewCFunction`. It validates and
  marshals and calls **one** import; it never does I/O.
- **Layer A (Go↔wasm)** is the only place real I/O happens. Default **deny**.

## The uniform ABI convention

WebAssembly functions pass/return **only numbers** (i32/i64/f64) — no strings, no
objects, no host pointers. Bytes cross through the module's single **linear memory**:
write the bytes, pass `(offset, length)`. One convention covers every capability, in both
directions, and is the **same envelope `qjs_eval` already returns**:

```
import:   host_<cap>_<op>(reqPtr: i32, reqLen: i32) -> resPtr: i32

request   the guest passes bytes it already holds — JS_ToCStringLen hands back a pointer
          into linear memory, so the path/args cross as (ptr,len) with NO extra copy.

response  the Go host allocates the result BUFFER IN GUEST MEMORY by calling the guest's
          own exported qjs_malloc (re-entrant call — the wasm component-model
          `cabi_realloc` pattern), writes the envelope, and returns its pointer:

              [0]      u8    tag   (0 = value, 1 = error)
              [1..4]   u32   len   (little-endian)
              [5..]    bytes payload (result bytes, or a UTF-8 error message)

          The guest reads it and qjs_free()s it — symmetric with how the Go host reads +
          frees the qjs_eval result. resPtr == 0 is reserved for host-OOM.
```

**Failure → catchable exception (confirmed).** `tag 1` makes the Layer-B C shim call
`JS_ThrowTypeError(ctx, "%.*s", len, payload)` — a normal JS exception the script can
`try/catch` and read `.message` from. It is never a silent zero or empty string.
`TestHostFailureIsCatchable` reads an out-of-scope path inside `try/catch` and gets back
`"caught: fs: path … not in allowlist"`.

**One helper each side, reused by every capability.** C: `unpack_host_result(ctx,res)`.
Go: `writeResult(ctx, mod, tag, payload)`. Adding a capability adds a Layer-B shim + a Go
host func; the marshalling is already written.

**Why not packed-i64 `(ptr<<32)|len`?** It still needs the re-entrant `qjs_malloc` for the
payload (saving only the 5-byte header) and has **nowhere to carry an error message**,
which would split the catchable-exception path in two. The tagged buffer is already proven
in-tree.

## The two independent gates

Both derive from one `Grant`; **either alone denies** (spike constraint #3).

| Gate | Where | Granted | Ungranted |
| --- | --- | --- | --- |
| **1 — module injection** | `Bundle` (stand-in for esbuild's resolver) | injects the vendored `node:fs` shim; `import` resolves | **refuses to resolve** → the script can't be assembled → **no call site exists** (`TestFSUngrantedGate1Bundle`) |
| **2 — host enforcement** | the `env` host function, per call | runs `os.ReadFile` (in scope) | refuses in Go **before any syscall**, returns a `tag 1` "not granted" the guest throws (`TestFSUngrantedGate2DenyStub`) |

**Honest nuance — single `.wasm` ⇒ Gate 2 is a deny-stub, not a truly-absent import.**
Because we embed one `quickjs.wasm`, it *declares* `host_fs_read`/`host_net_fetch`
statically, and wazero requires **every declared import to be satisfied at instantiation**
or the whole module fails to link. So the per-run choice is *real impl vs deny-stub*, not
*present vs absent*. This is the "(or a deny-stub)" branch the brief anticipated. The
strongest "genuinely absent, not returning empty" is therefore **Gate 1** (no call site);
Gate 2 is defense-in-depth that **denies explicitly** (throws) and **performs no I/O**
(tests assert the file contents never appear in the error). A truly-omitted per-instance
import would require per-capability sub-modules or per-instance runtimes — out of scope,
and unnecessary given Gate 1.

## Grant → instantiation seam (the recommendation)

```
compile ONCE  ──►  fresh Instance per run  ──►  Bundle(src, grant)   [Gate 1]
(Runtime.New)      (Runtime.RunScript)          eval under WithGrant(ctx, grant) [Gate 2]
```

- **One compiled module, fresh instance per run.** Compilation (~137 ms) is the expensive
  step; instances are ~140 µs and disposable — matches the first spike's isolation model.
- **Grant travels on `context.Context`.** The host functions are registered **once** on
  the runtime (one wazero runtime ⇒ one `env` module namespace — you cannot register N
  distinct `env` modules, so a literal *per-instance host module* isn't possible without
  per-instance runtimes). Instead `RunScript` puts the `Grant` on the ctx and the host
  funcs read it (`grantFromContext`). The script cannot forge the ctx, and each run gets a
  fresh instance, so this is **per-run wiring in effect**. This is the pragmatic reading of
  "the import set is chosen from that script's grant": the C-level imports are fixed, but
  the **JS surface (Gate 1) and the enforcement (Gate 2) are both chosen per run** from the
  grant.
- **Production mapping (design):** a `Grant` is **pinned to the script's content hash**,
  resolved from a **local, per-capability** store, **off by default**. The consent prompt
  fires **only for new/changed requests**: statically scan the module graph for
  `import … from "node:*"` (capability modules), diff that set against the stored grant for
  this content hash, and prompt only on the delta. The static scan is the same one `Bundle`
  already does.

## Scope enforcement lives in Go

The fs grant carries `AllowedPaths`. `FSGrant.allows` permits an exact file or anything
under an allowed directory (prefix containment on `filepath.Clean`-ed paths). An
out-of-scope read is refused **in the host function** — `TestFSReadOutOfScopeRefusedInGo`
grants `<dir>/ok` and confirms `<dir>/secret.txt` is refused with the reason originating
Go-side, and that the secret's bytes never cross. **Hardening TODO (noted, not built):** a
production check should also `filepath.EvalSymlinks` and confirm the result stays under a
real root, to defeat symlink escapes.

## Inert-module packaging

Recommendation: **vendored JS polyfill**, bundled into the JS (not compiled into the wasm
C), for pure modules — it needs no import, no grant, and no wasm rebuild to edit. Reserve C
for cases with a strong existing C lib. Demoed with `node:path` (`join`/`basename`),
injected unconditionally and runnable under `Grant{}` (`TestInertModuleNoGrant`). Using
**Node names** means `path`, `buffer`, crypto-hashing, and text codecs — the pure Node
builtins many npm packages import — resolve for free without ever touching a capability.

## Async capabilities

- **Synchronous fs (implemented).** The Go host func blocks the (parked) wasm thread for
  `os.ReadFile` and returns — no event loop, no Promise. This is the simplest proof and the
  common case for fs.
- **Async net (designed, `host_net_fetch` stubbed on the same ABI).** A real network cap
  surfaces to JS as a **Promise**:
  1. the `node:net`/`fetch` Layer-B shim creates a promise via `JS_NewPromiseCapability`
     and stashes its resolve/reject;
  2. `host_net_fetch` **registers** the request in Go and returns a **ticket** immediately
     (non-blocking) — the HTTP runs off the wasm thread on the existing Go `net/http`/
     connectrpc machinery (reuse, don't reimplement in C);
  3. an engine **job pump** drives the loop: `JS_ExecutePendingJob` drains microtasks, and
     as tickets complete Go calls a guest `qjs_resolve(ticket, resPtr)` export to fulfill
     the stashed resolve fn; repeat until no pending jobs and no outstanding tickets.
  4. the existing wall-clock deadline now bounds the **whole loop**, not a single eval.

  `TestNetStubGeneralizes` proves the *ABI* generalizes to a second capability today; only
  the loop is deferred.

## Browser-host feasibility (design-only, confirmed portable)

The `.wasm` is host-agnostic — it declares imports (`env.host_fs_read`, …) and never names
Go. A browser host satisfies the identical interface in JS: read `(ptr,len)` from
`WebAssembly.Memory`, do the work, write the `[tag|len|payload]` result back through the
module's exported `qjs_malloc`, return the pointer. Flags:

- **No threads / shared memory / atomics.** Already patched out in the first spike; keep it
  that way — SharedArrayBuffer/threads would demand COOP/COEP headers and break the simple
  single-threaded host. This is the one wasm feature that would break portability.
- **Async nuance.** wazero can block Go for a synchronous import; a browser cannot block on
  real fs/net. So a browser host must use the **async pump** (above) even for "fs" unless
  the data is already in memory — the sync path is a wazero-desktop convenience, the async
  path is the portable default.
- The wall-clock interrupt is a wazero feature; in-browser you'd use `JS_SetInterruptHandler`
  with a deadline or run in a terminable Worker.

## What was built

- `third_party/quickjs/qjs_wasm.c` — added the two `import_name` externs, the two Layer-B
  marshallers (`js_host_fs_read`, `js_host_net_fetch`), the `unpack_host_result` helper, and
  `register_capabilities` (attaches `__grpcview_fs_read`/`__grpcview_net_fetch` per eval).
- `service/scripting/engine.go` — `Grant`/`FSGrant`/`NetGrant`, `WithGrant`/
  `grantFromContext`, the `env` host module (`registerHostModule` + `hostFSRead`/
  `hostNetFetch`), the `writeResult` marshalling helper, the `Bundle` esbuild-stand-in +
  `node:*` shim registry, and `RunScript`/`EvalWithGrant`. The raw `Eval`/`EvalIsolated`
  path is unchanged (empty grant ⇒ deny-stubs), so the first spike's 8 bound tests are
  untouched.
- `service/scripting/capabilities_test.go` — the 8 tests below.
- `service/scripting/BUILD.bazel` — adds `capabilities_test.go`.

### Tests (all green)

| Test | Proves |
| --- | --- |
| `TestInertModuleNoGrant` | inert `node:path` runs under `Grant{}` |
| `TestFSReadGrantedInScope` | granted + in-scope read returns file contents end-to-end |
| `TestFSReadOutOfScopeRefusedInGo` | out-of-scope path refused **in Go**; bytes don't leak |
| `TestFSUngrantedGate1Bundle` | ungranted → **Gate 1** resolve failure (no call site) |
| `TestFSUngrantedGate2DenyStub` | call site present but **Gate 2** denies before any syscall |
| `TestHostFailureIsCatchable` | host failure is a **catchable** JS exception with a reason |
| `TestNetStubGeneralizes` | the same ABI carries a second capability |
| `TestMarshallingCost` | per-call round-trip cost |

## Numbers (Apple arm64; `bazel test`)

| Metric | Value |
| --- | --- |
| `quickjs.wasm` size | **660 KiB** (675,675 B) — **+1,045 B** vs the first spike for two imports + Layer-B shims |
| Warm bare eval `1+1` (baseline) | ~843 µs/op |
| **`fs.readFileSync` round-trip** (marshal + grant/scope check + `os.ReadFile` + result) | **~897 µs/op** |
| **Per-call marshalling overhead** | **~50 µs** on top of a bare eval — a small fraction of the ~840 µs per-eval QuickJS context bootstrap |

The marshalling itself (two memory copies + a re-entrant `qjs_malloc`) is cheap; the fixed
cost remains QuickJS context bootstrap, exactly as the first spike found.

## Risks / open items

- **Deny-stub, not absent import** (single `.wasm`) — Gate 1 is the true "no call site";
  Gate 2 denies explicitly. Revisit only if a use case needs the import literally omitted.
- **`Bundle` is a stand-in**, not esbuild — a regex scan of default `import … from "…"`
  statements. It doesn't parse (won't see imports in comments/strings), handles only the
  default-import form, and knows a hardcoded 3-module registry. The real esbuild resolver +
  npm/content-addressed store remain out of scope; this proves only the grant→injection
  seam.
- **Grant on ctx, not a per-instance host module** — a deliberate concession to the
  one-runtime/one-namespace reality; safe because the ctx is unforgeable and each run is a
  fresh instance. Strict per-instance imports would need per-instance runtimes.
- **Symlink scope escape** — the allowlist is prefix-on-clean; add `EvalSymlinks` +
  real-root containment before trusting it with secrets.
- **Async loop unbuilt** — net is stubbed; the Promise/job-pump is designed, not proven. The
  microtask pump ties to the first spike's event-loop open item.
- **`fs.readFileSync` returns a string** (not a Node `Buffer`) and ignores the encoding arg
  — enough to prove the seam; a full shim would honour both.
