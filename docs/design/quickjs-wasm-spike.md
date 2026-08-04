# QuickJS → WASM under Bazel — spike findings

Status: **spike complete, working, green.** A pinned QuickJS is compiled to a single
`wasm32-wasi` module by Bazel (locally and analysis-clean on RBE) and executed from Go
by the pure-Go wazero runtime, keeping the release artifact a single static CGO-free
binary. All the bounds that goja cannot give us — a hard memory cap and a wall-clock
interrupt — are demonstrated by `//service/scripting:scripting_test`.

**Recommendation: GO.** The one thing that shelved this earlier ("a separate C→WASM
pipeline + build complexity") turned out to be small: ~3 in-tree config files, a 3-hunk
source patch, and one zero-dependency Go module. The threat-model win over goja
(enforceable CPU/memory bounds on untrusted code) is real and demonstrated. Proceed to
build the real engine on this foundation.

## TL;DR decisions

| Decision | Choice | Deciding reason |
| --- | --- | --- |
| Variant | **bellard/quickjs**, pinned `2026-06-04` | Eval is 5 `.c` files → one `zig cc` call. NG's CMake build needs a build-system driver (rules_foreign_cc/emscripten) we'd otherwise avoid. Conformance is a wash and esbuild down-levels anyway. |
| ABI | **`wasm32-wasi`, reactor** (`-mexec-model=reactor`) | libc for free (`malloc`, `gettimeofday`) ⇒ no syscall-shimming of the source; wazero speaks WASI p1. We grant **no** FS/args/env, so despite linking libc the sandbox can't touch the host. Freestanding buys a smaller import surface but costs a hand-rolled allocator for no win here. |
| Toolchain | **zig `cc` via hermetic_cc_toolchain (avenue 3a)**, invoked *directly* as a build tool | Already in-tree, hermetic, RBE-ready. No emsdk, no new toolchains. Zig 0.14.0 ships `wasm32-wasi-musl` libc + `wasm-ld`. |
| Host runtime | **wazero v1.12.0** | Pure Go, **zero dependencies**, no CGO. Enforces the outer memory ceiling and ctx-driven wall-clock interrupt. |

## What was built

- `//third_party/quickjs` — module extension pins the source (`http_archive`, sha256 +
  `-p1` patch), and a `genrule` compiles it to `quickjs.wasm`.
  - `extensions.bzl` — `http_archive` for `quickjs-2026-06-04.tar.xz`
    (`sha256 b376e839…ad2a`) + `quickjs-wasi.patch`.
  - `qjs_wasm.c` — the host↔guest ABI shim (the seam; see below).
  - `quickjs.BUILD.bazel` — exposes `core_srcs` (the 5 eval files; **excludes**
    `quickjs-libc.c`, the OS/fs/exec bindings) and `headers`.
  - `BUILD.bazel` — the `genrule` calling `zig cc`.
- `//service/scripting` — `engine.go` (wazero wrapper + `//go:embed quickjs.wasm`) and
  `wasm_smoke_test.go`. `copy_file` brings the `.wasm` into the package so it embeds by
  basename, exactly like `//service/cmd` embeds `index.html`.
- `MODULE.bazel` — `use_repo(toolchains, "zig_sdk")` note, the `quickjs` extension, and
  `com_github_tetratelabs_wazero` in `go_deps`.

### Build / test / bump

```bash
bazel build //third_party/quickjs:quickjs_wasm     # -> bazel-bin/.../quickjs.wasm (659 KiB)
bazel test  //service/scripting:scripting_test     # the smoke test + numbers (-test.v)
```

To bump QuickJS: change the three coupled values in `extensions.bzl`
(`QUICKJS_VERSION`, `QUICKJS_SHA256`) and the `-DCONFIG_VERSION` in
`third_party/quickjs/BUILD.bazel`, then re-verify the patch still applies.

## The source patch (`quickjs-wasi.patch`, 3 hunks, all `#if`-guarded no-ops off wasi)

Vanilla QuickJS does not build clean for `wasm32-wasi` out of the box. Each fix is
narrow and gated on the compiler-defined `__wasi__`:

1. **`quickjs.c`: `#include <malloc.h>` under `__wasi__`.** `malloc_usable_size` exists
   in wasi-libc but is only declared in `<malloc.h>`, which quickjs.c doesn't pull in.
   Including it keeps `JS_SetMemoryLimit` accounting **exact** (the alternative — the
   source's own `return 0` fallback — would degrade the cap).
2. **`quickjs.c`: exclude `__wasi__` from `CONFIG_ATOMICS`.** wasi is single-threaded
   with no pthreads (link failure otherwise), and Atomics/SharedArrayBuffer are unwanted
   in an untrusted single-instance sandbox.
3. **`quickjs.c`: exclude `__wasi__` from `CONFIG_STACK_CHECK`; `dtoa.c`: skip the
   (unused) `<setjmp.h>`.** The stack check's `js_get_stack_pointer()` probe assumes a
   native downward C stack; on wasm's shadow stack every `js_check_stack_overflow()`
   reads spurious, so *every* `JS_CallInternal` threw — the first symptom hit
   (`1+1` returned an uncatchable exception). Emscripten disables the same check for the
   same reason; wazero still contains runaway recursion by trapping on wasm stack
   exhaustion. wasi-musl's `<setjmp.h>` hard-`#error`s without the wasm exception
   proposal, and dtoa.c never uses it.

> Patch-format gotcha for the next person: Bazel's native patcher (`ctx.patch`) is
> **stricter than GNU `patch`/`git apply`** — a blank context line must be a single
> space, not empty. Generate the patch with `git diff`, never hand-write it.

## Toolchain-resolution notes (the part that needed care)

**We do not go through cc_toolchain resolution for wasm — we call `zig cc` directly**
from the genrule. Findings that drove this:

- hermetic_cc_toolchain 4.2.0 **defines** wasm target structs (`_target_wasm()` →
  `wasm32-wasi-musl`, `@platforms//cpu:wasm32`, `wasm-ld`) but the root module only
  `register_toolchains`es the host-platform cc toolchains. There is **no registered
  wasm cc_toolchain** to resolve.
- On a macOS host, `.bazelrc` force-selects the Apple cc toolchain
  (`build:macos --extra_toolchains=…apple…`). Even if a wasm toolchain were registered,
  we'd be steering resolution around that. Invoking the compiler directly sidesteps the
  whole question — the wasm build works identically on a macOS host and on Linux RBE.
- The genrule references the compiler as `@zig_sdk//:zig` (a clean alias that survives
  `bazel mod tidy`) plus its wasm support files
  `@@hermetic_cc_toolchain++toolchains+zig_config//:wasm32-wasi-musl_all_files`. The
  support-files filegroup lives only in the sibling `@zig_config` repo, which the
  extension does **not** advertise as a root dep — so it can't go in `use_repo` (tidy
  strips it) and is referenced by **canonical name** instead (buildifier
  `canonical-repository` suppressed at the rule, with a comment).
- zig's cache dir is set per-action via `mktemp -d`, which honours the per-platform
  sandbox `TMPDIR` (and the RBE executor) — hermetic on macOS and remote alike.

### RBE status

- The genrule builds fully sandboxed locally (`darwin-sandbox`), i.e. only declared
  inputs — strong evidence of hermeticity.
- `bazel build --config=remote //third_party/quickjs:quickjs_wasm` **analyzes cleanly,
  resolves the remote exec platform, creates the action, and reaches the executor**; it
  fails only on `UNAUTHENTICATED: User not found` (no BuildBuddy key on this dev box).
  It is **not** blocked by toolchain resolution or inputs.
- **Caveat — client/executor homogeneity.** hermetic_cc_toolchain fetches the zig SDK
  for the machine that *runs Bazel* (here `@zig_config/zig` is a Mach-O arm64 binary).
  That is correct for both real configurations: local = mac-client + local-exec, and CI
  = **linux-client + linux-RBE** (`buildbuddy.yaml` runs on `ubuntu-22.04`, so the
  fetched zig is the linux one). A heterogeneous mac-client → linux-RBE cross-build would
  ship the wrong zig; the repo doesn't use that path, and neither does the existing
  hermetic_cc_toolchain usage. If that path is ever needed, register a real per-exec
  wasm cc_toolchain (or cross-fetch the linux SDK) — noted as future work, not needed now.

## The bound that justifies all this (vs goja)

Demonstrated end-to-end in `wasm_smoke_test.go`:

- **Inner cap** — `JS_SetMemoryLimit`: `"x".repeat(50e6)` under a 4 MiB QuickJS heap cap
  is rejected as a catchable `out of memory`, host unharmed. Byte-accurate thanks to
  patch #1.
- **Outer backstop** — wazero `WithMemoryLimitPages`: with the inner limit **disabled**,
  a 20 MiB allocation still fails because wazero refuses to grow linear memory past the
  ceiling. This holds even if QuickJS's own accounting were bypassed. (Boot footprint is
  2 MiB; test ceiling 8 MiB.)
- **Wall-clock interrupt** — wazero `WithCloseOnContextDone` + a `context` deadline:
  `while(true){}` is killed at **~251 ms** against a 250 ms deadline and surfaces as
  `ErrInterrupted`. This is the preemption goja's `Interrupt()` cannot guarantee against
  a tight allocation-free loop. (`JS_SetInterruptHandler` is the in-guest alternative;
  the host-ctx path is simpler and needs no clock import.)

goja runs in-process with no memory cap and cannot preempt a single large allocation —
that gap is the whole reason for this spike, and wazero closes it.

## Numbers (Apple arm64; `bazel test`, matches `-c opt` within noise)

| Metric | Value |
| --- | --- |
| `quickjs.wasm` size (`-Oz`, stripped) | **659 KiB** (674,630 B); ~252 KiB gzipped |
| Initial linear memory / instance | 2 MiB (32 pages), grows on demand to the ceiling |
| Cold: `CompileModule` (once per process) | ~140–155 ms |
| Instantiate a fresh instance | ~100 µs |
| Warm eval `1+1` (pooled instance) | ~810–840 µs/op |
| Per-run isolated (instantiate+eval+teardown) | ~910–950 µs/op |
| Footprint after 200 warm evals | flat at 2 MiB (no leak) |

**Per-execution cost nuance:** the shim creates a fresh `JSRuntime`+`JSContext` on every
`qjs_eval`, so instance *pooling* saves only the ~100 µs instantiate — the ~800 µs is the
QuickJS context bootstrap (building all intrinsics), paid per eval. For untrusted
per-run isolation that ~900 µs is the honest price. For trusted per-invoke middleware
that reuses a context, add a shim entry point that keeps a long-lived `JSContext` and
that cost largely disappears (at the cost of shared JS state) — future work.

## The capability seam (intentionally empty)

`Instance.Eval` (in `engine.go`) is where an already-transpiled JS **source string**
enters the engine. The guest ABI (`qjs_malloc`/`qjs_free`/`qjs_eval`, result buffer
`[tag u8][len u32 LE][payload]`) is in `qjs_wasm.c`. Host capability APIs (`std/http`,
`console`, …) will attach as **wasm host-function imports** surfaced onto the JS global
object in a later build; none are wired here. WASI is instantiated but granted no FS,
args, or env.

## Risks / open items

- **`CONFIG_STACK_CHECK` is off under wasi.** Deep JS recursion becomes a wasm stack-
  exhaustion **trap** (contained by wazero, instance discarded) rather than a catchable
  `RangeError`. Acceptable for per-run isolation; revisit if catchability matters.
- **Interrupt closes the instance.** A pooled instance that is interrupted must be
  discarded and replaced — pooling logic needs to handle that.
- **Patch maintenance on version bumps** — 3 small hunks; keep `-DCONFIG_VERSION` and the
  archive version in sync.
- **Canonical `@@…zig_config` reference** could break if hermetic_cc_toolchain
  restructures repo naming across a version bump (stable within pinned 4.2.0).
- **Not wired into `//service/cmd`** — out of scope; this is a standalone spike target.
- esbuild/TS transpile, npm store, capability/consent, Monaco type-acquisition — all out
  of scope, unchanged.
