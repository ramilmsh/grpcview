"""Module extension that pins the bellard/quickjs source archive.

Kept out of the wider go_deps/npm flows on purpose: this is a plain C source
tarball, fetched by http_archive and patched for wasm32-wasi (see quickjs-wasi.patch).
Pinned by sha256 + a fixed patch, so it is fully reproducible / RBE-cacheable.
"""

load("@bazel_tools//tools/build_defs/repo:http.bzl", "http_archive")

# Bump these three together (URL date, sha256, and the -DCONFIG_VERSION in BUILD.bazel).
QUICKJS_VERSION = "2026-06-04"
QUICKJS_SHA256 = "b376e839b322978313d929fd20663b11ba58b75df5a46c126dd19ea2fa70ad2a"

def _quickjs_impl(_module_ctx):
    http_archive(
        name = "quickjs",
        urls = ["https://bellard.org/quickjs/quickjs-{}.tar.xz".format(QUICKJS_VERSION)],
        sha256 = QUICKJS_SHA256,
        strip_prefix = "quickjs-{}".format(QUICKJS_VERSION),
        build_file = "//third_party/quickjs:quickjs.BUILD.bazel",
        patches = ["//third_party/quickjs:quickjs-wasi.patch"],
        patch_args = ["-p1"],
    )

quickjs = module_extension(implementation = _quickjs_impl)
