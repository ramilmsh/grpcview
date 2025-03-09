"""A module defining the third party dependency valkey"""

load("@bazel_tools//tools/build_defs/repo:http.bzl", "http_archive")

def _download_impl(ctx):
    http_archive(
        name = "sqlite",
        build_file = Label("//third_party/sqlite:BUILD.sqlite.bazel"),
        sha256 = "106642d8ccb36c5f7323b64e4152e9b719f7c0215acf5bfeac3d5e7f97b59254",
        strip_prefix = "sqlite-autoconf-3490100",
        urls = [
            "https://www.sqlite.org/2025/sqlite-autoconf-3490100.tar.gz",
        ],
    )

download = module_extension(
    implementation = _download_impl,
)
