#!/usr/bin/env bash
set -euo pipefail

dest=
base_url=
bazel_config=

usage() {
    cat >&2 <<'EOF'
usage: tools/stage_release.sh --dest DIR --base-url URL [--bazel-config NAME]

  --dest DIR           directory to stage into (created if absent)
  --base-url URL       release root the published install.sh will fetch from
  --bazel-config NAME  --config to pass to bazel, e.g. `ci` for remote execution
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
    --bazel-config)
        bazel_config=${2:-}
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
config=${bazel_config:+--config=$bazel_config}
bazel build $config --stamp -c opt //service/cmd:release

for artifact in $(bazel cquery $config --stamp -c opt --output=files //service/cmd:release 2>/dev/null); do
    install -m 0755 "$artifact" "$dest/$(basename "$artifact")"
done

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
