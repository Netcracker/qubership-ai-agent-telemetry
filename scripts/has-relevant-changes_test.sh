#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
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

assert_path_triggers_build() {
  repo=$1
  path=$2
  previous_sha=$(cd "$repo" && git rev-parse HEAD)
  (
    cd "$repo"
    mkdir -p "$(dirname "$path")"
    printf 'changed\n' > "$path"
    git add "$path"
    git commit -qm "change $path"
  )
  changed_sha=$(cd "$repo" && git rev-parse HEAD)
  assert_output true "$repo" \
    EVENT_NAME=pull_request \
    BASE_SHA="$previous_sha" \
    BEFORE_SHA= \
    AFTER_SHA="$changed_sha"
}

repo="$test_dir/repo"
make_repo "$repo"
base_sha=$(cd "$repo" && git rev-parse HEAD)

(
  cd "$repo"
  printf 'updated\n' > docs/index.md
  git add docs/index.md
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

for lifecycle_path in \
  lifecycle.go \
  update.go \
  managed_cli.go \
  scripts/install.sh \
  global-scripts/qubership-dev-install.sh \
  .github/workflows/installer-tests.yaml \
  .github/workflows/qubership-dev-installer-tests.yaml \
  .github/workflows/release.yaml \
  README.md \
  agent-packages/ai-agent-telemetry/README.md \
  agent-packages/ai-agent-telemetry-configure/README.md \
  docs/agent-integration.md \
  docs/adr/0002-bare-binary-on-path.md \
  docs/adr/0005-cli-managed-global-hooks.md \
  docs/cli.md \
  docs/release.md \
  docs/superpowers/decisions/2026-06-23-codex-sandbox-execpolicy-rule.md \
  docs/superpowers/decisions/2026-06-23-on-path-binary-lifecycle.md \
  global-scripts/README.md
do
  assert_path_triggers_build "$repo" "$lifecycle_path"
done

printf 'has-relevant-changes tests passed\n'
