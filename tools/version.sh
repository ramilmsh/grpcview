#!/usr/bin/env bash
# Prints the version string stamped into release binaries as STABLE_VERSION_TAG.
#
# An exact `vX.Y.Z` tag pointing at HEAD wins. Otherwise this prints a Go
# pseudo-version (https://go.dev/ref/mod#pseudo-versions) — the canonical way to
# name an untagged commit by date:
#
#   v0.0.0-20260806152233-1a2b3c4d5e6f      (no vX.Y.Z tag exists yet)
#   v0.1.1-0.20260806152233-1a2b3c4d5e6f    (latest tag is v0.1.0)
#
# The timestamp is the commit time in UTC, so the strings sort chronologically
# and compare as semver prereleases below the tag they build on. A dirty
# worktree gets a `+dirty` build-metadata suffix; the Release action in
# buildbuddy.yaml refuses to publish one.
set -euo pipefail

release_tags() {
    git tag -l 'v*' --sort=creatordate | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' || true
}

version=$(git tag --points-at HEAD -l 'v*' | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | tail -n1 || true)

if [[ -z "$version" ]]; then
    commit_time=$(TZ=UTC0 git show -s --format=%cd --date=format-local:%Y%m%d%H%M%S HEAD)
    short_sha=$(git rev-parse --short=12 HEAD)
    latest_tag=$(release_tags | tail -n1)

    if [[ -n "$latest_tag" ]]; then
        # Go bumps the patch of the tag being built on and sorts the result
        # below it with a `-0.` prerelease segment.
        IFS=. read -r major minor patch <<<"${latest_tag#v}"
        version="v${major}.${minor}.$((patch + 1))-0.${commit_time}-${short_sha}"
    else
        version="v0.0.0-${commit_time}-${short_sha}"
    fi
fi

# diff-index compares the index against HEAD and trusts the index's stat data, so
# a file whose mtime or inode changed without its content changing reads as a
# modification. A fresh CI checkout hits this routinely — the tree it stats was
# written by a different process than the one asking — and calls a clean worktree
# dirty. Refreshing re-stats the tree first, which is what `git status` does.
git update-index -q --refresh || true
if ! git diff-index --quiet HEAD --; then
    version="${version}+dirty"
fi

echo "$version"
