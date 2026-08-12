#!/usr/bin/env bash

set -euo pipefail

repo_root=$(cd "$(dirname "$0")/.." && pwd)

assert_gate() {
  local workflow=$1
  local gate=$2
  local expected_needs=$3

  yq -e --arg gate "$gate" '.jobs[$gate].if == "${{ always() }}"' "$repo_root/$workflow" >/dev/null
  yq -e --arg gate "$gate" '.jobs[$gate]["runs-on"] == "ubuntu-latest"' "$repo_root/$workflow" >/dev/null
  yq -e --arg gate "$gate" --argjson expected "$expected_needs" '.jobs[$gate].needs == $expected' "$repo_root/$workflow" >/dev/null
  yq -e --arg gate "$gate" '.jobs[$gate].steps | any(.[]; .name == "Fail when a required job did not succeed")' "$repo_root/$workflow" >/dev/null
  yq -e --arg gate "$gate" '.jobs[$gate].steps | any(.[]; .run | contains("Expected two"))' "$repo_root/$workflow" >/dev/null
}

assert_gate ".github/workflows/go-build.yaml" "ci-gate" '["changes", "build", "hook-platform-tests"]'
assert_gate ".github/workflows/installer-tests.yaml" "ci-gate" '["changes", "posix-lifecycle", "windows-lifecycle"]'
