#!/usr/bin/env bash
# Builds the multi-arch grpcview binaries and publishes them to Google Cloud Storage.
#
#   tools/release.sh --bucket gs://my-bucket
#
# Layout written to the bucket:
#
#   <bucket>/grpcview/<version>/grpcview_<goos>_<goarch>
#   <bucket>/grpcview/<version>/SHA256SUMS
#   <bucket>/grpcview/latest                 (a text file holding <version>)
#
# <version> comes from tools/version.sh. Version directories are immutable: the
# script refuses to overwrite one unless --force is passed.
set -euo pipefail

BUCKET=${GRPCVIEW_RELEASE_BUCKET:-}
PREFIX=${GRPCVIEW_RELEASE_PREFIX:-grpcview}
assume_yes=false
dry_run=false
allow_dirty=false
force=false

usage() {
    cat >&2 <<'EOF'
usage: tools/release.sh [--bucket gs://BUCKET] [options]

  --bucket gs://BUCKET  destination bucket (or set GRPCVIEW_RELEASE_BUCKET)
  --prefix PATH         object prefix inside the bucket (default: grpcview)
  --dry-run             build and stage, print the upload plan, upload nothing
  --force               overwrite an already-published version
  --allow-dirty         release from a modified worktree (stamps +dirty)
  --yes                 skip the confirmation prompt
EOF
    exit 2
}

while [[ $# -gt 0 ]]; do
    case "$1" in
    --bucket)
        BUCKET=${2:-}
        shift 2
        ;;
    --prefix)
        PREFIX=${2:-}
        shift 2
        ;;
    --dry-run)
        dry_run=true
        shift
        ;;
    --force)
        force=true
        shift
        ;;
    --allow-dirty)
        allow_dirty=true
        shift
        ;;
    --yes | -y)
        assume_yes=true
        shift
        ;;
    -h | --help) usage ;;
    *)
        echo "unknown argument: $1" >&2
        usage
        ;;
    esac
done

cd "$(git rev-parse --show-toplevel)"

if [[ -z "$BUCKET" ]]; then
    echo "error: no destination bucket; pass --bucket gs://... or set GRPCVIEW_RELEASE_BUCKET" >&2
    exit 2
fi
BUCKET=${BUCKET%/}
[[ "$BUCKET" == gs://* ]] || BUCKET="gs://$BUCKET"

if ! git diff-index --quiet HEAD -- && [[ "$allow_dirty" != true ]]; then
    echo "error: worktree is modified; commit first or pass --allow-dirty" >&2
    exit 1
fi

version=$(tools/version.sh)
dest="$BUCKET/$PREFIX/$version"

if [[ "$force" != true ]] && gcloud storage ls "$dest" >/dev/null 2>&1; then
    echo "error: $dest already exists; pass --force to overwrite" >&2
    exit 1
fi

echo "==> building $version"
bazel build --stamp -c opt //service/cmd:release

staging=$(mktemp -d)
trap 'rm -rf "$staging"' EXIT

for artifact in $(bazel cquery --stamp -c opt --output=files //service/cmd:release 2>/dev/null); do
    install -m 0755 "$artifact" "$staging/$(basename "$artifact")"
done

# `sha256sum` on Linux, `shasum` on macOS; both write the same format.
if command -v sha256sum >/dev/null; then
    sha256=(sha256sum)
else
    sha256=(shasum -a 256)
fi
(cd "$staging" && "${sha256[@]}" grpcview_* >SHA256SUMS)
printf '%s\n' "$version" >"$staging/latest"

echo
echo "version:     $version"
echo "destination: $dest/"
(cd "$staging" && ls -l grpcview_* | awk '{printf "  %-28s %10d bytes\n", $9, $5}')
echo "  SHA256SUMS"
echo "and $BUCKET/$PREFIX/latest -> $version"
echo

if [[ "$dry_run" == true ]]; then
    echo "dry run: nothing uploaded"
    exit 0
fi

if [[ "$assume_yes" != true ]]; then
    read -r -p "upload to $dest/ ? [y/N] " reply
    [[ "$reply" == [yY]* ]] || {
        echo "aborted"
        exit 1
    }
fi

# Version directories never change, so they can be cached forever; `latest` is
# the pointer clients re-read, so it must not be.
gcloud storage cp \
    --cache-control="public, max-age=31536000, immutable" \
    "$staging"/grpcview_* "$staging/SHA256SUMS" "$dest/"

gcloud storage cp \
    --cache-control="no-cache" \
    "$staging/latest" "$BUCKET/$PREFIX/latest"

echo "published $version to $dest/"
