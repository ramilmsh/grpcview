# Lint and format via aspect_rules_lint; Aspect CLI for local UX

No lint or format tooling exists in this repo today: no `.eslintrc`/`eslint.config.*`, no
`.prettierrc`, no `golangci-lint`/`gofumpt`, no `.shellcheckrc`, and `buf.yaml` has no `lint`
section. This plans wiring five tools — shellcheck, buf lint, eslint, prettier, gofumpt —
through [`aspect_rules_lint`](https://github.com/aspect-build/rules_lint), and adopting the
[Aspect CLI](https://github.com/aspect-build/aspect-cli) as an optional, local wrapper around
`bazel` for the nicer `bazel lint` output.

## Decisions (confirmed with the user before writing this plan)

- **Enforcement lives in `.bazelrc`, unconditionally — no new command.** Lint aspects and
  format checks run as part of the existing `bazel build //...` / `bazel test //...` (what
  `buildbuddy.yaml` already invokes). There is no `--config=lint`, no `bazel lint` requirement
  for CI, and no second CI step.
- **CI stays on plain `bazel`.** Aspect CLI is a local, optional convenience layer for
  developers who want grouped findings and `--fix`; it changes nothing about what gets
  enforced, since that's all in `.bazelrc`/`BUILD.bazel`, not Aspect-CLI-specific config.
- **Go *linting* is out of scope — that's nogo's job, left alone.** Nothing here is a `go vet`
  replacement. `gofumpt` is wired purely as a **formatter** (`format_multirun`/`format_test`),
  which is the only role `aspect_rules_lint` gives it — there is no "gofumpt lint aspect".
- **Landing sequence is two commits**, once the tooling itself is verified green:
  1. All new/changed infra files (`.bazelrc`, `MODULE.bazel`, `tools/lint/**`,
     `tools/format/**`, `ui/eslint.config.mjs`, `ui/.prettierrc*`, `.shellcheckrc`, `buf.yaml`
     lint section, `ui/package.json`/lockfile) plus whatever those tools themselves need
     touched to pass (e.g. a `.shellcheckignore` or a couple of `disable=` comments), staged
     and committed once `bazel build //...` and `bazel test //...` are green.
  2. Everything else: the mechanical `gofumpt`/`prettier` reformat of the rest of the tree
     (and any eslint/shellcheck/buf-lint fixes outside the infra files), staged and committed
     separately, so the tooling commit's diff is reviewable on its own.

## Versions (checked against the registry/releases on 2026-08-29)

| Tool | Source | Version to pin |
|---|---|---|
| `aspect_rules_lint` | BCR (`bazel_dep`) | `2.8.0` (2026-08-21, latest) |
| Aspect CLI | `.aspect/version.axl` | latest release at implementation time (`v2026.35.9` as of 2026-08-26; it ships on a weekly calver cadence — re-check before pinning) |
| shellcheck | `rules_multitool` lockfile | `v0.11.0` (2026-01-05, latest stable) |
| buf | already pinned | `v1.61.0`, via the existing `rules_buf` `buf.toolchains()` extension — no new fetch |
| gofumpt | `mvdan.cc/gofumpt` via `go.mod` | latest tagged release at implementation time |
| eslint / prettier | `ui/package.json` devDependencies | latest majors (eslint 9.x flat config, prettier 3.x) |

## Per-tool wiring

### `tools/lint/linters.bzl` — the three real lint aspects

```starlark
load("@aspect_rules_lint//lint:eslint.bzl", "lint_eslint_aspect")
load("@aspect_rules_lint//lint:shellcheck.bzl", "lint_shellcheck_aspect")
load("@aspect_rules_lint//lint:buf.bzl", "lint_buf_aspect")

eslint = lint_eslint_aspect(
    binary = "//tools/lint:eslint",
    configs = ["//ui:eslint.config.mjs"],
)

shellcheck = lint_shellcheck_aspect(
    binary = "@multitool//tools/shellcheck",  # via TOOLS["shellcheck"], see below
    config = "//:.shellcheckrc",
)

buf = lint_buf_aspect(
    config = "//:buf.yaml",
)
```

These are the actual `aspect_rules_lint` factory signatures (`lint_eslint_aspect`,
`lint_shellcheck_aspect`, `lint_buf_aspect` — not the `eslint_aspect`/`shellcheck_aspect`
shorthand some of the prose docs use). `lint_buf_aspect` visits `proto_library` (default
`rule_kinds`), which is exactly what every `grpcview/**/BUILD.bazel` already declares via
`@protobuf//bazel:proto_library.bzl` — no new proto target shape needed. Its default
`toolchain` is `@rules_buf//tools/protoc-gen-buf-lint:toolchain_type`; **verify** that
`buf.toolchains(version = "v1.61.0")` in `MODULE.bazel` already registers it (it should, since
it's the same extension already providing the `buf` binary at `@rules_buf_toolchains//:buf`) —
if not, add the missing toolchain registration.

`buf.yaml` needs a `lint` section added (currently only `modules`/`includes`); start from
buf's own default ruleset (`DEFAULT` or `STANDARD`) and narrow from there once real findings
come in — don't hand-pick rules speculatively.

**eslint binary + config**: `ui/` has no eslint config or dependency at all today. Add as
`ui/package.json` devDependencies: `eslint`, `typescript-eslint`, `@eslint/js`,
`eslint-plugin-react-hooks`, `eslint-plugin-react-refresh` (the standard Vite+React+TS flat
config combo), then author `ui/eslint.config.mjs` (flat config, matching this being a fresh,
opinionated setup — no legacy `.eslintrc` needed since nothing depends on it yet). Wire the
binary as a `js_binary` over the npm package's own bin, matching how this repo already
resolves other npm-shipped executables through `aspect_rules_js`:

```starlark
load("@aspect_rules_js//js:defs.bzl", "js_binary")

js_binary(
    name = "eslint",
    data = ["//ui:node_modules/eslint"],
    entry_point = "//ui:node_modules/eslint/bin/eslint.js",
)

js_binary(
    name = "prettier",
    data = ["//ui:node_modules/prettier"],
    entry_point = "//ui:node_modules/prettier/bin/prettier.cjs",
)
```

**Verify at implementation time**: how `lint_eslint_aspect`'s sandboxed working directory
interacts with flat-config auto-discovery for `ts_project` targets that live under `ui/src/**`
— aspect_rules_lint's own `example/` (not fetchable while writing this plan) is the reference;
if auto-discovery doesn't find `eslint.config.mjs` from a nested sandbox cwd, pass an explicit
`--config` via the aspect's `args`.

**shellcheck binary**: only five scripts exist today (`.devcontainer/post-create.sh`,
`tools/{release,workspace_status,version,gopackagesdriver}.sh`), and `rules_multitool` is
already the pattern for prebuilt binaries in this repo (see `gh`, `starpls` in
`tools/multitool.lock.json`). Add a `shellcheck` entry there (4 platform binaries, `tar.xz`
archives, real `sha256`s computed from the downloaded artifacts — never hand-write a hash),
consume it in `tools/lint/BUILD.bazel` via `TOOLS["shellcheck"]` (same import as `TOOLS["gh"]`
in that file today), and add a root `.shellcheckrc`. These scripts use `sh_binary` targets
already (`tools/BUILD.bazel`'s `:release`) — confirm the others are declared as
`sh_binary`/`sh_library` too, or the aspect (which visits those rule kinds) sees nothing for
them; `.devcontainer/post-create.sh` in particular is likely not a Bazel target at all and
will need either a trivial `sh_library` wrapper or to be accepted as out of the aspect's
reach.

### `tools/format/BUILD.bazel` — gofumpt + prettier as formatters

```starlark
load("@aspect_rules_lint//format:defs.bzl", "format_multirun", "format_test")

format_multirun(
    name = "format",
    go = "@cc_mvdan_gofumpt//:gofumpt",   # confirm exact label, see below
    javascript = "//tools/lint:prettier",
)

format_test(
    name = "format_test",
    go = "@cc_mvdan_gofumpt//:gofumpt",
    javascript = "//tools/lint:prettier",
    srcs = ["//:all_formattable_srcs"],
)
```

with a root alias so `bazel run //:format` fixes and `bazel run //:format.check` checks
(the standard `format_multirun` surface). `format_test` needs an explicit `srcs` — add a
root `filegroup`:

```starlark
filegroup(
    name = "all_formattable_srcs",
    srcs = glob(
        ["**/*.go", "**/*.ts", "**/*.tsx", "**/*.js", "**/*.mjs", "**/*.json", "**/*.css"],
        exclude = [
            "bazel-*/**",
            "**/node_modules/**",
            "ui/dist/**",
            "**/*_pb.ts",
            "**/*_connect.ts",
            "**/*_connectquery.ts",
            "**/gen/**",
        ],
    ),
)
```

Because `format_test` becomes part of `//...`, `bazel test //...` — what `buildbuddy.yaml`
already runs — enforces formatting with no new step, matching the "no separate command"
decision. `bazel run //:format` remains the local one-shot fixer.

**gofumpt sourcing**: follow the existing precedent for tool-only Go deps —
`tools/tools.go` already blank-imports `connectrpc.com/connect/cmd/protoc-gen-connect-go`
purely so `go.mod` tracks it and gazelle's `go_deps.from_file(go_mod = "//:go.mod")` fetches
it. Add `_ "mvdan.cc/gofumpt"` there, add the module to `go.mod`, and add the resulting repo
name to `MODULE.bazel`'s `use_repo(go_deps, ...)` list. **Verify the exact generated repo/target
name** after running gazelle (expected `cc_mvdan_gofumpt`, following the `go_repository`
`<tld>_<domain>_<path>` convention, with a `go_binary` target gazelle names `gofumpt` from the
module's `cmd/gofumpt` package) — do not hand-guess this into `BUILD.bazel` without confirming
it built.

## Aspect CLI (local dev convenience, not required by CI)

Pin the launcher in `.aspect/version.axl`, the file Aspect CLI itself reads (parallel to how
`.bazelversion` already pins plain Bazel for whatever launches it — `.bazelversion` is
untouched and still names the underlying Bazel version Aspect CLI runs beneath itself).
Install the launcher (`curl -fsSL https://install.aspect.build | bash`, or via the project's
devcontainer — add one line to `.devcontainer/post-create.sh`, guarded the same way the
existing `bazel run //:bazel_env` line is, so a rebuild doesn't fail hard if the install step
is briefly unreachable). Once installed, `aspect` is a superset CLI: `aspect build`/`aspect
test` behave like plain `bazel`, and `aspect lint //...` additionally reads the `--aspects`
already declared in `.bazelrc` (see below) to print grouped, per-file findings and offer
`--fix` where a linter supports it. **Verify at implementation time** which config surface the
pinned CLI version actually wants for anything beyond the default `--aspects`-from-`.bazelrc`
behavior (recent Aspect CLI releases are converging on a Starlark `.aspect/config.axl` for
its task system; older ones used `.aspect/cli/config.yaml`) — this plan does not depend on
either, since enforcement is entirely in `.bazelrc`/`BUILD.bazel` and works identically with
plain Bazel.

This repo's `bazel_env` target (root `BUILD.bazel`) is unrelated and unaffected — it publishes
*Bazel-built* tool outputs onto `PATH` via direnv, whereas the Aspect CLI launcher is a
standalone binary that itself invokes Bazel, so it can't be one of `bazel_env`'s `tools`.

## `.bazelrc` additions

```
# Lint aspects run on every build/test; findings fail the action. `+` makes the extra
# output group additive — replacing the default output groups outright would mean e.g.
# `bazel build //service/cmd` stops producing the actual binary.
common --aspects=//tools/lint:linters.bzl%eslint
common --aspects=//tools/lint:linters.bzl%shellcheck
common --aspects=//tools/lint:linters.bzl%buf
common --output_groups=+rules_lint_human
common --@aspect_rules_lint//lint:fail_on_violation
```

`format_test` needs no flag at all — it is an ordinary test target and `bazel test //...`
already runs it.

## Implementation sequence

1. `bazel_dep(name = "aspect_rules_lint", version = "2.8.0")` in `MODULE.bazel`.
2. `tools/tools.go` + `go.mod` + `MODULE.bazel` `use_repo` for `mvdan.cc/gofumpt`; confirm the
   fetched repo builds a `gofumpt` `go_binary` via gazelle.
3. `tools/multitool.lock.json`: add `shellcheck` (4 platforms, real hashes); add
   `.shellcheckrc`.
4. `ui/package.json` devDependencies for eslint/prettier + plugins; regenerate
   `pnpm-lock.yaml`; author `ui/eslint.config.mjs` and `ui/.prettierrc.json` /
   `.prettierignore`.
5. `buf.yaml`: add a `lint` section.
6. `tools/lint/BUILD.bazel`: the two `js_binary` wrappers + the three `lint_*_aspect` calls in
   `tools/lint/linters.bzl`.
7. `tools/format/BUILD.bazel` + root `all_formattable_srcs` filegroup + root `//:format` /
   `//:format.check` aliases.
8. `.bazelrc` additions above.
9. `bazel build //... && bazel test //...` — fix whatever the new tooling needs from the infra
   files themselves (config tweaks, targeted `disable=`/`// nolint`-equivalents), **not** from
   reformatting the rest of the tree yet. Commit (commit 1, per the locked-in sequence).
10. `bazel run //:format` to reformat everything else, then resolve remaining eslint/shellcheck
    findings across the tree by hand. `bazel build //... && bazel test //...` green again.
    Commit (commit 2).
11. Optional: `.aspect/version.axl` + devcontainer install line, as a separate small commit —
    it changes nothing CI enforces.
12. Mention the new gates in `AGENTS.md`'s "Frontend gates" / Go sections only if they change
    what a contributor runs day-to-day — per the decisions above, they don't (`bazel build
    //...` / `bazel test //...` already cover it), so this may be a one-line addition at most.

## Open items to resolve during implementation, not guessed here

- Exact `mvdan_cc_gofumpt`-family repo/target name gazelle generates.
- Whether `buf.toolchains()` already registers the `protoc-gen-buf-lint` toolchain or needs an
  explicit extra registration.
- Whether `lint_eslint_aspect`'s sandbox resolves `ui/eslint.config.mjs` for nested
  `ts_project` targets without an explicit `--config` arg.
- Real `sha256`s for the shellcheck `v0.11.0` release artifacts (compute from the downloaded
  files — never copy hashes into a plan doc as fact).
- Aspect CLI's current config-file convention beyond `.bazelrc`-driven `--aspects`, if the
  install step wants to show off more than the default behavior.
