#!/usr/bin/env bash
set -euo pipefail

release_tags() {
    git tag -l 'v*' --sort=creatordate | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' || true
}

version=$(git tag --points-at HEAD -l 'v*' | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | tail -n1 || true)

if [[ -z "$version" ]]; then
    commit_time=$(TZ=UTC0 git show -s --format=%cd --date=format-local:%Y%m%d HEAD)
    short_sha=$(git rev-parse --short=12 HEAD)
    latest_tag=$(release_tags | tail -n1)

    if [[ -n "$latest_tag" ]]; then
        IFS=. read -r major minor patch <<<"${latest_tag#v}"
        version="v${major}.${minor}.$((patch + 1))-0.${commit_time}-${short_sha}"
    else
        version="v0.0.0-${commit_time}-${short_sha}"
    fi
fi

git update-index -q --refresh || true
if ! git diff-index --quiet HEAD --; then
    version="${version}+dirty"
fi

echo "$version"
