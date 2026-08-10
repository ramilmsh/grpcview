#!/usr/bin/env bash
# Builds the multi-arch grpcview binaries and stages a complete release into a
# directory. Shared by tools/release.sh (Google Cloud Storage) and
# .github/workflows/release.yml (GitHub releases): the two differ only in where
# the staged tree is uploaded, and in the URL shape their installer fetches from.
#
#   tools/stage_release.sh --dest DIR --base-url https://host/path
#
# Written into DIR:
#
#   grpcview_<goos>_<goarch>   one per //service/cmd:release platform
#   SHA256SUMS
#   latest                     a text file holding <version>
#   install.sh                 with @BASE_URL@ substituted
#   uninstall.sh
#
# The version comes from tools/version.sh and is printed on stdout; everything
# else this script says goes to stderr, so a caller can capture it.
set -euo pipefail

dest=
base_url=

usage() {
    cat >&2 <<'EOF'
usage: tools/stage_release.sh --dest DIR --base-url URL

  --dest DIR       directory to stage into (created if absent)
  --base-url URL   release root the published install.sh will fetch from
EOF
    exit 2
}

while [[ $# -gt 0 ]]; do
    case "$1" in
    --dest)
        dest=${2:-}
        shift 2
        ;;
    --base-url)
        base_url=${2:-}
        shift 2
        ;;
    -h | --help) usage ;;
    *)
        echo "unknown argument: $1" >&2
        usage
        ;;
    esac
done

[[ -n "$dest" ]] || {
    echo "error: --dest is required" >&2
    exit 2
}
# The staged install.sh is useless without a root to fetch from, and this script
# is the only place that knows how to substitute one, so it is not optional.
[[ -n "$base_url" ]] || {
    echo "error: --base-url is required" >&2
    exit 2
}

cd "$(git rev-parse --show-toplevel)"

base_url=${base_url%/}
mkdir -p "$dest"
dest=$(cd "$dest" && pwd)

version=$(tools/version.sh)

echo "==> building $version" >&2
bazel build --stamp -c opt //service/cmd:release

for artifact in $(bazel cquery --stamp -c opt --output=files //service/cmd:release 2>/dev/null); do
    install -m 0755 "$artifact" "$dest/$(basename "$artifact")"
done

# `sha256sum` on Linux, `shasum` on macOS; both write the same format.
if command -v sha256sum >/dev/null; then
    sha256=(sha256sum)
else
    sha256=(shasum -a 256)
fi
(cd "$dest" && "${sha256[@]}" grpcview_* >SHA256SUMS)
printf '%s\n' "$version" >"$dest/latest"

for script in install.sh uninstall.sh; do
    sed "s|@BASE_URL@|$base_url|g" "tools/$script" >"$dest/$script"
    chmod 0755 "$dest/$script"
    if grep -q '@BASE_URL@' "$dest/$script"; then
        echo "error: $script still has an unsubstituted @BASE_URL@" >&2
        exit 1
    fi
done

echo "$version"
