#!/usr/bin/env bash
exec bazel run --tool_tag=gopackagesdriver -- @rules_go//go/tools/gopackagesdriver "${@}"
