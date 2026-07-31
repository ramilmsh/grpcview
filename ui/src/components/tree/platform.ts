// Resolves ONCE, at module load, into a plain boolean — never re-read live on
// every keystroke, and never exported as a function. The host platform cannot
// change mid-session, so there is nothing to gain from recomputing it, and doing
// the resolution HERE rather than inside keymap.ts is what lets that file stay
// the pure, DOM-free table the plan calls for (docs/design/tree-rewrite-plan.md's
// module table: "keymap.ts — key event -> intent, one table, no DOM"). keymap.ts's
// `keyToIntent(stroke, isMac)` takes the platform as a plain boolean PARAMETER
// instead of importing IS_MAC and reaching for `navigator` itself. That split is
// also exactly what makes both platforms' rows unit-testable from the ONE process
// vitest runs: a table that read the platform internally could only ever be
// exercised as whichever platform the test runner happens to be, but a table that
// takes it as data can be called once with `true` and once with `false` in the
// same file (keymap.test.ts does exactly that). Nothing imports this module yet —
// wiring it into Tree.tsx (alongside keymap.ts and navigate.ts) is the next phase.
//
// Guarded for a non-browser environment, and NOT merely as theoretical
// housekeeping: this module will be reachable (once wired up) from code that also
// runs under vitest's `node` test environment (vitest.config.ts) and under any
// SSR-style render (Tree.portable.test.tsx uses react-dom/server for exactly this
// reason) — neither is a real browser. The naive guard for that is
// `typeof navigator === "undefined"`, but that check is not guaranteed to be the
// branch that fires: modern Node (21+) ships its OWN global `navigator`, and on
// this very machine it reports a real-looking `userAgent` ("Node.js/24") AND —
// less expectedly — a `platform` string ("MacIntel") derived from the host OS,
// verified empirically while writing this file (`node -e "console.log(navigator)"`
// on this darwin box). A bundler-toolchain Node instance is exactly what runs
// `bazel test //ui:test`, so a guard that only asks "does `navigator` exist" can
// walk straight past this branch and read that fake-but-present object instead of
// ever reaching Node's real signal. `process.versions.node` is a marker
// `navigator` cannot fake, which is what makes checking it FIRST the right call —
// the point is reliably detecting Node/Electron at all, not whether the answer it
// yields happens to match the host OS (see below: under plain Node it always
// will, and that's fine, for reasons that have nothing to do with "accuracy").
//
// monaco-editor's own platform detection (vendored in this exact bundle, so this
// app already ships it) hits the identical hazard and resolves it the same way:
// check for a real Node/Electron `process` FIRST — `versions.node` is a marker
// `navigator` cannot fake — and only trust `navigator` once that's ruled out (see
// `esm/vs/base/common/platform.js`'s `nodeProcess` branch vs. its `navigator`
// branch). `typeof process !== "undefined"` is safe to evaluate in every
// environment, including a real browser that never declares `process` at all:
// `typeof` on a possibly-undeclared identifier never throws. This ordering isn't
// only a fake-navigator workaround, either: in an Electron / VS Code-webview
// host — which this repo is actively planning for, see docs/design/vscode/ — a
// real Node `process` is genuinely present, and `process.platform` there is the
// AUTHORITATIVE signal, not a heuristic the way sniffing `navigator.userAgent`
// ever is. monaco checks `process` first because, in the hosts it actually ships
// into, doing so is sometimes not just safer but strictly more correct.
//
// What this ordering does NOT claim is that `process.platform === "darwin"`
// under plain Node/vitest is somehow a MORE meaningful answer than trusting
// `navigator` would have been there — it is exactly as host-OS-derived either
// way, and under `bazel test //ui:test` this branch just reports whatever
// machine happens to run the suite. That's fine because it's harmless rather
// than meaningful: nothing user-facing ever consumes THIS module's IS_MAC inside
// a test. keymap.ts takes `isMac` as an explicit parameter for exactly that
// reason (see its own header comment, and keymap.test.ts's side-by-side
// `keyToIntent(stroke, true)` / `keyToIntent(stroke, false)` calls) — IS_MAC
// exists to resolve the real app's actual runtime platform once; it is not, and
// was never meant to be, the mechanism anything under test relies on to reach
// one platform's behavior or the other.
function detectIsMac(): boolean {
  if (typeof process !== "undefined" && typeof process.versions?.node === "string") {
    return process.platform === "darwin";
  }
  if (typeof navigator === "undefined") return false;
  const userAgent = navigator.userAgent ?? "";
  const platform = navigator.platform ?? "";
  // Both userAgent and platform are checked, OR'd rather than required
  // together, because either one alone is enough evidence and either can
  // independently go missing or get neutered: some browsers freeze
  // `navigator.platform` to a generic value for fingerprinting resistance, and a
  // future browser may drop it entirely in favor of `NavigatorUAData` — checking
  // `userAgent` too (matching monaco's own `indexOf('Macintosh')`) means losing
  // one signal doesn't silently flip every Mac user to the Windows/Linux table.
  return userAgent.includes("Macintosh") || platform.includes("Mac");
}

// The one value this module exists to produce.
export const IS_MAC = detectIsMac();
