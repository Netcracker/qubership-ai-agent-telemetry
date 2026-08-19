#!/bin/sh

set -eu

repo_root=$(
  CDPATH=''
  cd -- "$(dirname -- "$0")/.."
  pwd
)
test_root=$(mktemp -d "${TMPDIR:-/tmp}/ai-agent-telemetry-apm-compile.XXXXXX")
trap 'rm -rf "$test_root"' EXIT HUP INT TERM
project_root="$test_root/project"
mkdir -p "$project_root"

git -C "$repo_root" ls-files --cached --others --exclude-standard -z |
  tar -C "$repo_root" --null -T - -cf - |
  tar -x -C "$project_root"
cp "$project_root/AGENTS.md" "$test_root/canonical-AGENTS.md"

cat >"$project_root/apm.yml" <<'YAML'
name: ai-agent-telemetry-apm-compile-test
version: 1.0.0
dependencies:
  apm:
    - git: ./agent-packages/ai-agent-telemetry-configure
      targets: [codex]
YAML

cd "$project_root"
"${APM_BIN:-apm}" install --target codex
"${APM_BIN:-apm}" compile --target codex --single-agents --output "$test_root/compiled-AGENTS.md"

if ! cmp -s AGENTS.md "$test_root/canonical-AGENTS.md"; then
  echo "APM compilation changed the canonical AGENTS.md" >&2
  diff -u "$test_root/canonical-AGENTS.md" AGENTS.md >&2 || true
  exit 1
fi

# The backticks are literal Markdown delimiters in the generated instruction.
# shellcheck disable=SC2016
if ! grep -Fq 'invoke the `ai-agent-telemetry-configure` skill' "$test_root/compiled-AGENTS.md"; then
  echo "Compiled Codex context does not contain the telemetry configuration trigger" >&2
  exit 1
fi
