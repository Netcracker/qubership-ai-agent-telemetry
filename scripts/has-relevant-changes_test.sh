#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
SCRIPT="$SCRIPT_DIR/has-relevant-changes.sh"

test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT

run_check() {
  (
    cd "$1"
    shift
    env "$@" "$SCRIPT"
  )
}

assert_output() {
  expected=$1
  shift
  output=$(run_check "$@")
  if [ "$output" != "$expected" ]; then
    printf 'Expected "%s", got "%s"\n' "$expected" "$output" >&2
    exit 1
  fi
}

make_repo() {
  repo=$1
  mkdir -p "$repo"
  (
    cd "$repo"
    git init -q
    git config user.email test@example.com
    git config user.name "Test User"
    printf 'module example.com/test\n' > go.mod
    printf '# Test\n' > README.md
    mkdir -p docs
    printf 'docs\n' > docs/index.md
    git add .
    git commit -qm initial
  )
}

repo="$test_dir/repo"
make_repo "$repo"
base_sha=$(cd "$repo" && git rev-parse HEAD)

(
  cd "$repo"
  printf 'updated\n' > README.md
  git add README.md
  git commit -qm docs-only
)
docs_sha=$(cd "$repo" && git rev-parse HEAD)

assert_output false "$repo" \
  EVENT_NAME=pull_request \
  BASE_SHA="$base_sha" \
  BEFORE_SHA= \
  AFTER_SHA="$docs_sha"

(
  cd "$repo"
  printf 'package main\n' > main.go
  git add main.go
  git commit -qm source-change
)
source_sha=$(cd "$repo" && git rev-parse HEAD)

assert_output true "$repo" \
  EVENT_NAME=pull_request \
  BASE_SHA="$base_sha" \
  BEFORE_SHA= \
  AFTER_SHA="$source_sha"

assert_output true "$repo" \
  EVENT_NAME=push \
  BASE_SHA= \
  BEFORE_SHA="$docs_sha" \
  AFTER_SHA="$source_sha"

assert_output true "$repo" \
  EVENT_NAME=push \
  BASE_SHA= \
  BEFORE_SHA=0000000000000000000000000000000000000000 \
  AFTER_SHA="$source_sha"

manual_output_file="$test_dir/github-output"
assert_output true "$repo" \
  EVENT_NAME=workflow_dispatch \
  BASE_SHA= \
  BEFORE_SHA= \
  AFTER_SHA="$source_sha" \
  GITHUB_OUTPUT="$manual_output_file"

if ! grep -qx 'run-build=true' "$manual_output_file"; then
  printf 'Expected GITHUB_OUTPUT to contain run-build=true\n' >&2
  exit 1
fi

printf 'has-relevant-changes tests passed\n'
