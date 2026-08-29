# Patches

## rules_buf_extensions_buf_lock.patch / rules_buf_repo_proto_package_mode.patch

Adds a `buf.dependencies(buf_lock = [...])` tag class to the `rules_buf` bzlmod
extension, letting it read BSR module pins straight out of a `buf.lock` file
instead of repeating them by hand in `MODULE.bazel`. This keeps `buf.yaml`
(used by the Buf CLI/IDE) and the Bazel build from drifting out of sync.

Upstream discussion / patch source: https://github.com/bufbuild/rules_buf/issues/131
(the two files are split into separate patches because Bazel's built-in
`ctx.patch` — used for `single_version_override` patches — mis-applies a
single multi-file unified diff).

On top of the linked patch, `_parse_buf_lock` in `extensions.bzl` was fixed to
flush its last parsed entry: the original only emitted a pin when it saw the
*next* `-` list item in `buf.lock`, silently dropping the final (or only)
dependency in the file.

Applied to `rules_buf` 0.5.4 via `single_version_override` in `MODULE.bazel`.
Drop these once the feature lands upstream.
