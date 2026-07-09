#!/bin/sh
set -eu

event_name=${EVENT_NAME:-}
base_sha=${BASE_SHA:-}
before_sha=${BEFORE_SHA:-}
after_sha=${AFTER_SHA:-}

if [ "$event_name" = "pull_request" ]; then
  changed_files=$(git diff --name-only "$base_sha" HEAD)
elif [ "$event_name" = "push" ]; then
  if [ "$before_sha" = "0000000000000000000000000000000000000000" ]; then
    changed_files=$(git diff-tree --no-commit-id --name-only -r "$after_sha")
  else
    changed_files=$(git diff --name-only "$before_sha" "$after_sha")
  fi
else
  changed_files="force-run"
fi

run_build=false
while IFS= read -r file; do
  [ -n "$file" ] || continue
  case "$file" in
    .github/*|docs/*|CODE-OF-CONDUCT.md|CONTRIBUTING.md|LICENSE|README.md|SECURITY.md)
      ;;
    *)
      run_build=true
      break
      ;;
  esac
done <<EOF
$changed_files
EOF

if [ -n "${GITHUB_OUTPUT:-}" ]; then
  echo "run-build=$run_build" >> "$GITHUB_OUTPUT"
fi

printf '%s\n' "$run_build"
