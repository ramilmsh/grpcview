# Unblock `ui/src:app` on the Go launcher: `loader` attr, a switch flag, and measurement

Follows [`esbuild-go-launcher-experiment.md`](./esbuild-go-launcher-experiment.md) — Phases
0-3 there are done (fork vendored, launcher built and reviewed, `e2e/go_launcher/` proves
correct output and closes the sandbox-escape hole). This doc is that plan's Phase 4, made
concrete: `ui/src:app` cannot use the Go launcher today because it passes `config =
"esbuild.config.mjs"`, and the launcher rejects **every** config file, not just ones
declaring plugins — a JS config module is arbitrary code (`export default` can compute
anything, including a `plugins` array from elsewhere), so there is no safe static check for
"has no plugins" short of executing it, which is exactly the IPC/JS-runtime cost this
experiment exists to remove. `ui/src`'s actual config only sets a loader map
(`{".ttf": "dataurl"}` — a codicon-glyph fix, see `ui/src/esbuild.config.mjs`'s own
comment); it never needed to be a config file at all.

## 1. Add a `loader` rule attribute, retire the config file

`third_party/rules_esbuild/esbuild/private/esbuild.bzl`:
- Add `"loader": attr.string_dict(...)` next to `define` (`esbuild.bzl:15`).
- In `_esbuild_impl`, wire it into the args dict the same way `define` is (`esbuild.bzl:261-266`)
  — plain string values, no `expand_location` needed (define uses that because its values
  can reference other targets; a loader extension→name mapping never does).

`third_party/rules_esbuild/esbuild/go_launcher/args.go`:
- Add `Loader map[string]string` (`json:"loader"`) to `buildArgs`.
- In `toBuildOptions`, map through the existing `parseLoader` (already verified against
  esbuild's canonical loader table by the Phase 2 review) into `api.BuildOptions.Loader`.
- JS launcher needs no change — `loader` is already a first-class esbuild JS-API field;
  `launcher.js` just spreads the args JSON onto `build()`.

`ui/src/BUILD.bazel`:
- Replace `config = "esbuild.config.mjs"` with `loader = {".ttf": "dataurl"}` on `:app`.
- Delete `ui/src/esbuild.config.mjs`.
- Check `ui/src/theme/esbuild.config.mjs` (the sibling `.woff2` case the deleted file's
  comment pointed at) — same fix applies if that target's config is equally loader-only;
  confirm its actual shape before touching it, don't assume.

**Verify:** `bazel build //ui/src:app`, then the three `ui/AGENTS.md` gates — `tsc --noEmit`,
`bazel test //ui:test`, `bazel build //ui:ui` — all still green under the JS launcher
(nothing here changes which launcher runs yet). Confirm codicon glyphs still embed
(`dist/main.js` should still contain `data:font/ttf` URLs).

## 2. A Bazel flag to switch launchers

Use `bazel_skylib`'s `string_flag`/`config_setting` (already a `bazel_dep`) rather than
legacy `--define`.

New build setting (e.g. `ui/BUILD.bazel`):
```
string_flag(name = "esbuild_launcher", build_setting_default = "js", values = ["js", "go"])
config_setting(name = "use_go_launcher", flag_values = {":esbuild_launcher": "go"})
```

On `:app` in `ui/src/BUILD.bazel`:
```
launcher = select({
    "//ui:use_go_launcher": "@aspect_rules_esbuild//esbuild/go_launcher",
    "//conditions:default": None,  # rule's own default (JS)
})
```
Confirm the `launcher` attr (`esbuild.bzl:75`) tolerates `None`/unset as "use default"
before relying on it — if not, select both arms to explicit labels.

Usage: `bazel build //ui/src:app` (JS, default) vs.
`bazel build //ui/src:app --//ui:esbuild_launcher=go`.

**Verify:** both configs build; `bazel test //ui:test` / `bazel build //ui:ui` stay green
under the default (js) value — the flag's mere existence must not change default behavior.
Sanity-build once under `--//ui:esbuild_launcher=go`.

## 3. Measure it

- **Cold-build timing**, matching the original diagnosis (the ~65-70s/~75-79s critical-path
  claim in `esbuild-go-launcher-experiment.md` was cold-build):
  - `bazel clean` → `bazel build //ui/src:app --profile=<path>/profile-js-cold.json.gz` (JS,
    default) → record wall time.
  - `bazel clean` again → same build with `--//ui:esbuild_launcher=go` → record wall time.
  - Repeat each 2-3x, take median (cold-cache variance is real).
  - `bazel analyze-profile` on each `.json.gz` to isolate the `:app` esbuild action's own
    duration, not just total wall time (shared `ts_project`/deps cost should be equal in
    both and cancel out in the diff — isolate to confirm rather than assume).
- **Warm/incremental sanity check**: touch one `.tsx` under `ui/src`, rebuild under each
  flag value. Not the stated bottleneck, but the loop developers actually feel day to day —
  worth one data point.
- **Report**: cold JS, cold Go, delta, % — plus the incremental pair. Decide keep/revert
  against whether the delta approaches the ~65-70s the original diagnosis predicted.
- **One correctness pass on the real artifact**: build under the Go launcher, run the dev
  backend (`bazel run //service/cmd/dev`), confirm the app loads and codicon glyphs /
  Monaco render. Browser is last resort per root `AGENTS.md`, but this is the first time
  the Go launcher ships something a person looks at.

## Decision point

Keep `launcher = select(...)` wired into `ui/src:app` (default `js`, opt-in `go`) only if
Phase 3's measurement shows a real win here, not just in the isolated `e2e/go_launcher/`
case. Otherwise: leave the flag in place as a documented escape hatch, or revert per the
parent doc's own "Out of scope" backout path.

## Result (2026-08-29)

Measured on this repo, 3 cold-cache reps each (`bazel clean` between):

| | JS launcher (median) | Go launcher (median) | delta |
|---|---|---|---|
| Cold wall time | 75.3s | 119.3s | **+58%** |
| Cold critical path | 71.6s | 113.5s | +58% |
| Isolated `:app` esbuild action | 69.1s | 65.4s | -5.5%, inside Go's own 11s spread — noise |
| Warm/incremental (touch one `.tsx`) | 78.4s | 107.2s | +37% |

The Go build's extra wall time is almost entirely a `bazel clean`-only tax: compiling the
vendored esbuild fork's Go toolchain (`GoStdlib` + ~10 `GoCompilePkg` actions) from scratch.
That cost stays warm across ordinary incremental builds, but even so the Go launcher is
still slower warm, and the one number that mattered — the isolated bundling action, i.e.
the IPC overhead this experiment set out to remove — is a wash, not a win. The ~65-70s
critical-path cost the original diagnosis attributed to `bazel-sandbox.js` IPC turns out to
be inherent to bundling this much code (Monaco + workers) through esbuild at all; it doesn't
go away with an in-process Go launcher.

Correctness pass (`--//ui:esbuild_launcher=go`, served via `grpcview serve`, checked with
curl): `index.html`/`main.js`/`main.css`/`theme.css` all 200, `main.css` embeds the codicon
`.ttf` as `data:font/ttf`, `theme.css` embeds both `.woff2` fonts — the Go launcher produces
a correct artifact, it's just not faster.

**Decision: revert.** `launcher = select(...)` was removed from `ui/src:app` and the
`//ui:esbuild_launcher` flag/`use_go_launcher` config_setting were removed from
`ui/BUILD.bazel` — no reason to carry the Bazel-graph complexity for a lever that makes
both cold and warm builds worse. The `loader` rule attribute (§1) and config-file retirement
stay — real cleanup independent of which launcher runs. The vendored fork and Go launcher
themselves are untouched (still exercised by `e2e/go_launcher/`, still available as
`--//ui:esbuild_launcher=go` in history if someone wants to re-check after further Go-side
optimization).
