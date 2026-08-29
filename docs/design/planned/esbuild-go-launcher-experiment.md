# Experiment: fork aspect_rules_esbuild, add a Go-native launcher

## Background

`ui/src:app`'s cold build spends ~65-70s of its ~75-79s critical path inside
aspect_rules_esbuild's `bazel-sandbox.js` plugin. That plugin runs through
esbuild's **JS API**, which drives the real Go esbuild binary as a child
process over a stdin/stdout IPC protocol (`stdio_protocol.ts`). The plugin's
`onResolve({filter: /./})` matches every module resolution and does a nested
`build.resolve()` inside the handler, so each resolution pays the IPC tax
twice — on a graph the size of monaco-editor's, that's the dominant cost, not
CPU-bound bundling work.

esbuild's **Go API** (`pkg/api`) runs the same engine in-process — a
`api.Plugin`'s `OnResolve` is a Go closure called directly, no serialization,
no process boundary. `service/scripting/bundler.go` already does exactly this
for an unrelated feature (bundling user scripts), including a structurally
near-identical sandbox-style path-containment plugin
(`pathResolverPlugin`/`withinDir`) — that's the template to adapt.

The cheap fixes (`bazel_sandbox_plugin = False`, or a narrower/memoized patch
to the existing JS plugin) stay available and untouched by this experiment.
This doc is for the alternative: eliminate the IPC architecture entirely by
reimplementing the sandbox-guard logic in Go.

## Scope and success criteria

This is contained to the ruleset, not to `ui/`:

- **Success = aspect_rules_esbuild's own `e2e/*` and `examples/*` all still
  build/test green, unmodified, plus one new e2e case proves the Go launcher
  produces correct output.**
- Pointing grpcview's own `ui/src:app` at the new launcher is optional
  follow-up (Phase 4), not part of the pass/fail bar.
- `examples/plugins` (uses `esbuild-plugin-svg`, an arbitrary JS plugin loaded
  via `esbuild.config.mjs`) is explicitly out of scope for the Go path — a Go
  launcher can't execute arbitrary `.mjs` plugin code without embedding a JS
  runtime. It must keep passing on the existing JS launcher, untouched.

## Phase 0 — Vendor the fork

1. `git ls-remote --tags https://github.com/aspect-build/rules_esbuild.git |
   grep 0.27.0` to confirm the exact tag matching grpcview's current pin.
2. `git subtree add --prefix=third_party/rules_esbuild
   https://github.com/aspect-build/rules_esbuild.git <tag> --squash` — keeps
   upstream history reachable via `git subtree pull` later, one commit in
   grpcview's log.
3. In root `MODULE.bazel`, add `local_path_override(module_name =
   "aspect_rules_esbuild", path = "third_party/rules_esbuild")` right after
   the existing `bazel_dep(name = "aspect_rules_esbuild", version =
   "0.27.0")` — leave that line in place; the override just redirects the
   fetch.
4. Verify no-op: `bazel clean && bazel build //ui/...` should behave
   identically to before (same output, comparable timing). Confirms the
   plumbing works before any real edits land.

## Phase 1 — Baseline the ruleset's own tests (before touching anything)

5. `third_party/rules_esbuild` is itself a bzlmod root, and each `e2e/*`
   subdir (`bundle`, `sourcemaps`, `smoke`, `path_mapping`, `tsconfig`,
   `npm-links`, `custom_version`, `toolchain_from_source`) is its **own
   separate workspace** with its own `MODULE.bazel`. Build/test each
   independently, plus `examples/` at the root:
   ```bash
   bazel test //...                      # root: examples/
   for d in e2e/*/; do (cd "$d" && bazel build //... && [ -x test.sh ] && ./test.sh); done
   ```
6. Record this as the pass/fail bar every later phase must not regress.

## Phase 2 — Add the Go launcher (additive only)

7. Add `bazel_dep(name = "rules_go", version = "<same as grpcview root>")` to
   `third_party/rules_esbuild/MODULE.bazel` — only the fork needs this, for
   its own BUILD files to reference `@rules_go//go:def.bzl`. grpcview's root
   already has rules_go + a hermetic Go SDK, so Bzlmod's MVS resolves both
   without conflict.
8. New package `esbuild/go_launcher/main.go`:
   - Parse `--esbuild_args=`, `--user_args=`, `--config_file=` (same contract
     as `esbuild/private/launcher.js`).
   - Unmarshal the args JSON into a struct, map onto `api.BuildOptions` — the
     fiddly part is `target` (a JS-API array, e.g. `["es2020"]` or multiple
     entries) splitting across Go's single `Target api.Target` field plus its
     separate `Engines []Engine` field, and the format/platform/logLevel/
     sourcemap string enums.
   - Port `resolveInExecroot`/`correctImportPath` from
     `esbuild/private/plugins/bazel-sandbox.js` almost line-for-line as a Go
     `api.Plugin{OnResolve: ...}` — `service/scripting/bundler.go`'s
     `pathResolverPlugin`/`withinDir` in this repo is a near-identical
     existing pattern to crib from directly.
   - **Explicitly reject** any config file that declares `plugins` — fail
     fast with a clear error pointing at the JS launcher, rather than
     silently misbehaving.
9. New `go_binary` target for the launcher. Check whether
   `launcher_kind_aspect`/`LauncherKindInfo` (in `esbuild/private/helpers.bzl`)
   hard-requires a `js_binary` — if so, extend that aspect to recognize a
   `go_binary` too (small, scoped change); otherwise accept the documented
   tradeoff of losing the `supports-path-mapping` execution requirement for
   targets that opt into this launcher.

## Phase 3 — Prove it, narrowly

10. New `e2e/go_launcher/` case, modeled on `e2e/bundle/` (no custom
    plugins), with its `esbuild()` target setting `launcher =
    "//esbuild/go_launcher"`.
11. Two checks:
    - Output diff against the equivalent JS-launcher build of the same
      sources — should match.
    - A source reachable only through a symlinked `node_modules` entry, to
      directly exercise the sandbox-escape guard that issue #58/
      `bazel-sandbox.js` exists for — proves the Go plugin actually closes
      that hole, not just that it builds.
12. Re-run the full Phase 1 baseline unmodified — zero regressions is the
    actual bar for calling the experiment successful.

## Phase 4 — Optional, only after Phase 3 is green

13. Point grpcview's own `ui/src:app` at `launcher =
    "@aspect_rules_esbuild//esbuild/go_launcher"`, `bazel clean` +
    `--profile` before/after to measure the real win in the actual repo, and
    decide keep/revert.

## Out of scope

RBE validation, config-file/arbitrary-plugin support in Go, and upstreaming
anything. This stays a local, revertible experiment — back out fully by
deleting the `local_path_override` line and the `third_party/rules_esbuild`
subtree directory.
