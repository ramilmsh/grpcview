#!/usr/bin/env bash
set -uo pipefail
set +e
f=bazel_tools/tools/bash/runfiles/runfiles.bash
# shellcheck disable=SC1090
source "${RUNFILES_DIR:-/dev/null}/$f" 2>/dev/null ||
    source "$(grep -sm1 "^$f " "${RUNFILES_MANIFEST_FILE:-/dev/null}" | cut -f2- -d' ')" 2>/dev/null ||
    source "$0.runfiles/$f" 2>/dev/null ||
    source "$(grep -sm1 "^$f " "$0.runfiles_manifest" | cut -f2- -d' ')" 2>/dev/null ||
    source "$(grep -sm1 "^$f " "$0.exe.runfiles_manifest" | cut -f2- -d' ')" 2>/dev/null ||
    {
        echo >&2 "error: cannot find $f"
        exit 1
    }
f=
set -euo pipefail

gh=$(rlocation multitool/tools/gh/gh)

release_platform_binaries=()
if [ -n "${RUNFILES_DIR:-}" ]; then
    release_platform_binaries=("$RUNFILES_DIR"/grpcview/service/cmd/grpcview_*)
else
    while IFS=' ' read -r _ path; do
        release_platform_binaries+=("$path")
    done < <(grep '^grpcview/service/cmd/grpcview_' "$RUNFILES_MANIFEST_FILE")
fi

dest=dist
dry_run=false

usage() {
    cat >&2 <<'EOF'
usage: bazel build --stamp -c opt //tools:release && bazel run //tools:release -- [options]

  --dest DIR   directory to stage into (created if absent; default: dist)
  --dry-run    stage, print what would publish, publish nothing

--stamp -c opt on the outer bazel invocation is what makes the staged
binaries release binaries: unstamped, they'd embed no version, and
without -c opt they'd be unoptimized.
EOF
    exit 2
}

while [[ $# -gt 0 ]]; do
    case "$1" in
    --dest)
        dest=${2:-}
        shift 2
        ;;
    --dry-run)
        dry_run=true
        shift
        ;;
    -h | --help) usage ;;
    *)
        echo "unknown argument: $1" >&2
        usage
        ;;
    esac
done

cd "${BUILD_WORKING_DIRECTORY:-$PWD}"
cd "$(git rev-parse --show-toplevel)"

repo=$(git remote get-url origin 2>/dev/null | sed -E 's#^https://[^@]*@#https://#; s#^.*github\.com[:/]##; s#\.git$##' || true)
sha=$(git rev-parse HEAD 2>/dev/null || true)
[ -n "$repo" ] && [ -n "$sha" ] || {
    echo "error: could not read the repository from origin" >&2
    exit 1
}

[ -z "${REPO_TOKEN:-}" ] || export GH_TOKEN="$REPO_TOKEN"

log=$(mktemp)
exec > >(tee "$log") 2>&1
report_failure() {
    code=$?
    set +x
    if [ "$code" = 0 ]; then
        return 0
    fi
    if [ -n "${REPO_TOKEN:-}" ]; then
        body=$(tail -n 250 "$log" |
            sed "s|$REPO_TOKEN|REDACTED|g" |
            tr -d '\000-\010\013-\037')
        "$gh" api "repos/$repo/commits/$sha/comments" \
            -f "body=Release action failed (exit $code).

\`\`\`
$body
\`\`\`" >/dev/null 2>&1 || true
    fi
    [ -z "${BUILDBUDDY_ARTIFACTS_DIR:-}" ] || cp "$log" "$BUILDBUDDY_ARTIFACTS_DIR/release.log" || true
}
trap report_failure EXIT
set -x

git fetch --tags --force origin

base_url="https://github.com/$repo/releases/download"
version=$(tools/version.sh)
case "$version" in
*+dirty)
    echo "error: worktree is modified; version.sh gave $version" >&2
    exit 1
    ;;
esac
echo "==> $repo $version"

if "$gh" release view "$version" --repo "$repo" >/dev/null 2>&1; then
    echo "$version is already published; nothing to do"
    exit 0
fi

mkdir -p "$dest"
dest=$(cd "$dest" && pwd)

for artifact in "${release_platform_binaries[@]}"; do
    install -m 0755 "$artifact" "$dest/$(basename "$artifact")"
done

if command -v sha256sum >/dev/null; then
    sha256=(sha256sum)
else
    sha256=(shasum -a 256)
fi
(cd "$dest" && "${sha256[@]}" grpcview_* >SHA256SUMS)

render() {
    src=$1
    name=$2
    sed -e "s|@BASE_URL@|$base_url|g" -e "s|@VERSION@|$version|g" "$src" |
        awk -v sums="$dest/SHA256SUMS" '
            $0 == "@SHA256SUMS@" { while ((getline line < sums) > 0) print line; next }
            { print }
        ' >"$dest/$name"
    chmod 0755 "$dest/$name"
    for placeholder in @BASE_URL@ @VERSION@ @SHA256SUMS@; do
        if grep -q "$placeholder" "$dest/$name"; then
            echo "error: $name still has an unsubstituted $placeholder" >&2
            exit 1
        fi
    done
}

render tools/install.sh.tmpl install.sh

ls -l "$dest"
for artifact in "$dest"/grpcview_*; do
    printf '%s %s\n' "$artifact" "$(od -An -t x1 -N4 "$artifact" | tr -d ' ')"
done

if [ "$dry_run" = true ]; then
    echo "dry run: staged $version without publishing it"
    exit 0
fi

notes=$(printf '## Install\n\n```sh\ncurl -fsSL https://github.com/%s/releases/latest/download/install.sh | sh\n```\n' "$repo")
"$gh" release create "$version" \
    --repo "$repo" \
    --target "$sha" \
    --title "$version" \
    --latest \
    --generate-notes \
    --notes "$notes" \
    "$dest"/grpcview_* "$dest/SHA256SUMS" "$dest/install.sh"
