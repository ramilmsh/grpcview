#!/usr/bin/env bash
# Proves two things about esbuild/go_launcher (the Go-native launcher added in
# esbuild/go_launcher/):
#
# 1. Output parity: bundling the same sources through the default JS launcher
#    and through `launcher = "@aspect_rules_esbuild//esbuild/go_launcher"`
#    produces the same JS (//:bundle-js vs //:bundle-go).
#
# 2. The Go port of the sandbox-guard plugin (esbuild/go_launcher/sandbox.go,
#    a port of esbuild/private/plugins/bazel-sandbox.js) actually closes the
#    symlink-escape hole described in
#    https://github.com/aspect-build/rules_esbuild/issues/58: //consumer's
#    entry point imports @e2e/escape-lib, which is reachable only through
#    node_modules/@e2e/escape-lib -- a symlink npm_link_package creates whose
#    realpath lands in the *real*, unsandboxed execroot, outside the
#    processwrapper-sandbox directory this action actually runs in (see
#    //escape-lib and //consumer:BUILD.bazel).
#
#    //consumer:bundle-unguarded builds the identical target with
#    bazel_sandbox_plugin = False to demonstrate the escape is real, not just
#    asserted: its metafile records the resolved import paths as literal
#    "../../../../../../../../execroot/_main/bazel-out/..." traversals --
#    esbuild followed the symlink straight out of the sandbox. //consumer:bundle
#    (the guarded target, bazel_sandbox_plugin defaults to True) must instead
#    record clean, sandbox-relative paths, and must still produce correct
#    output.
set -o errexit -o nounset -o pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"

echo "--- Check 1: output parity between the JS and Go launchers ---"

bazel build //:bundle-js //:bundle-go

js_out="$(bazel cquery --output=files //:bundle-js 2>/dev/null | grep -v '\.map$')"
go_out="$(bazel cquery --output=files //:bundle-go 2>/dev/null | grep -v '\.map$')"

# The only expected difference is the `//# sourceMappingURL=...` comment's
# filename, which mechanically follows from the two targets' distinct
# `output` attrs (bundle-js.js vs bundle-go.js) -- not from launcher
# behavior. Strip that one line before diffing so the check asserts on
# actual codegen, not on the two rule names we happened to pick.
if ! diff -u \
    <(grep -v '^//# sourceMappingURL=' "$js_out") \
    <(grep -v '^//# sourceMappingURL=' "$go_out"); then
    echo "FAIL: JS-launcher and Go-launcher output differ beyond the expected sourceMappingURL filename" >&2
    exit 1
fi

echo "PASS: JS-launcher and Go-launcher output match"

echo
echo "--- Check 2: symlink-escape sandbox-guard proof ---"

bazel build //consumer:bundle //consumer:bundle-unguarded

bundle_js="$(bazel cquery --output=files //consumer:bundle 2>/dev/null | grep '\.js$')"
if ! grep -q 'ESCAPED_ANSWER = 42' "$bundle_js"; then
    echo "FAIL: guarded build did not produce the expected bundled content" >&2
    exit 1
fi
echo "PASS: guarded build succeeds and resolves the symlinked import correctly"

metafile_guarded="$(bazel cquery --output=files //consumer:bundle 2>/dev/null | grep '_metadata\.json$')"
metafile_unguarded="$(bazel cquery --output=files //consumer:bundle-unguarded 2>/dev/null | grep '_metadata\.json$')"

# Without the guard, esbuild's resolver follows the node_modules symlink to
# its realpath -- the real, unsandboxed execroot -- which shows up in the
# metafile as a relative path climbing all the way out via "../"s into
# ".../execroot/_main/bazel-out/...". This is the literal escape, not an
# inference from success/failure: prove it happened in the unguarded build...
if ! grep -q 'execroot/_main/bazel-out' "$metafile_unguarded"; then
    echo "FAIL: expected the unguarded build to demonstrate the symlink escape (no 'execroot/_main/bazel-out' climb found in its metafile) -- this environment may not reproduce issue #58's escape the same way; investigate before trusting the guarded check below" >&2
    exit 1
fi
echo "PASS: unguarded build reproduces the escape (metafile shows the raw realpath climbing out to the real execroot)"

# ...and prove the guard corrects it: the guarded build's metafile must not
# contain that same escape.
if grep -q 'execroot/_main/bazel-out' "$metafile_guarded"; then
    echo "FAIL: guarded build's metafile still shows an escaped path -- the sandbox-guard plugin did not correct it" >&2
    exit 1
fi
echo "PASS: guarded build's metafile shows clean, sandbox-contained paths -- the Go sandbox-guard plugin corrected the escape"
