#!/usr/bin/env bash
# gopls needs generated .go files and package JSON on disk; .bazelrc's linux config downloads minimal.
export GOPACKAGESDRIVER_BAZEL_BUILD_FLAGS="${GOPACKAGESDRIVER_BAZEL_BUILD_FLAGS:-} --remote_download_toplevel"
exec bazel run --tool_tag=gopackagesdriver -- @rules_go//go/tools/gopackagesdriver "${@}"
