#!/usr/bin/env bash
# Deletes Bazel output bases whose workspace (e.g. a removed git worktree) no longer exists.
# Run after removing worktrees. --dry-run prints what would be deleted.
set -euo pipefail

dry_run=false
if [[ "${1:-}" == "--dry-run" ]]; then
  dry_run=true
fi

root="${XDG_CACHE_HOME:-$HOME/.cache}/bazel/_bazel_${USER}"

for base in "${root}"/*/; do
  base="${base%/}"
  name="${base##*/}"
  # Output bases are named by an md5 hash; skip cache/, install/ and the like.
  [[ "${name}" =~ ^[0-9a-f]{32}$ ]] || continue
  [[ -f "${base}/DO_NOT_BUILD_HERE" ]] || continue
  workspace="$(<"${base}/DO_NOT_BUILD_HERE")"
  [[ -z "${workspace}" || -e "${workspace}" ]] && continue
  echo "${workspace} -> ${base}"
  if [[ "${dry_run}" == false ]]; then
    chmod -R u+w "${base}"
    rm -rf "${base}"
  fi
done
