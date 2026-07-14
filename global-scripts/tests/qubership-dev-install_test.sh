#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
INSTALLER="$SCRIPT_DIR/../qubership-dev-install.sh"

fail() {
  printf 'FAIL: %s\n' "$1" >&2
  exit 1
}

assert_contains() {
  _haystack=$1
  _needle=$2
  case $_haystack in
    *"$_needle"*) ;;
    *) fail "expected output to contain: $_needle" ;;
  esac
}

assert_not_contains() {
  _haystack=$1
  _needle=$2
  case $_haystack in
    *"$_needle"*) fail "expected output not to contain: $_needle" ;;
    *) ;;
  esac
}

assert_log_contains() {
  _needle=$1
  grep -F "$_needle" "$QDI_TEST_LOG" >/dev/null || {
    printf 'command log:\n' >&2
    sed 's/^/  /' "$QDI_TEST_LOG" >&2
    fail "expected command log to contain: $_needle"
  }
}

setup_component_fixture() {
  FIXTURE_ROOT=$(mktemp -d)
  export HOME="$FIXTURE_ROOT/home"
  export XDG_DATA_HOME="$FIXTURE_ROOT/data"
  export XDG_CONFIG_HOME="$FIXTURE_ROOT/config"
  export QDI_TEST_LOG="$FIXTURE_ROOT/commands.log"
  export QDI_GIT_CONFIG="$FIXTURE_ROOT/git-hooks-path"
  export QDI_APM_STATE="$FIXTURE_ROOT/apm-installed"
  export QDI_MARKETPLACE_STATE="$FIXTURE_ROOT/marketplace-added"
  export QDI_TELEMETRY_INSTALLER="$FIXTURE_ROOT/telemetry-installer.sh"
  export QDI_TELEMETRY_CLI="$FIXTURE_ROOT/ai-agent-telemetry"
  export QDI_GIT_ORIGIN_FILE="$FIXTURE_ROOT/git-origin"
  export QDI_APM_INSTALLER="$FIXTURE_ROOT/apm-installer.sh"
  export QDI_APM_CLI="$FIXTURE_ROOT/apm"
  export QUBERSHIP_DEV_APM_INSTALL_URL=https://example.test/apm-unix
  export QUBERSHIP_DEV_TELEMETRY_INSTALL_URL=https://example.test/install.sh
  export QUBERSHIP_DEV_GIT_HOOKS_REPOSITORY=https://example.test/pre-commit-global.git
  export QUBERSHIP_DEV_GIT_HOOKS_DIR="$XDG_DATA_HOME/qubership/pre-commit-global"
  export PATH="$FIXTURE_ROOT/bin:/usr/bin:/bin"
  unset CYBER_FERRET_PASSWORD QDI_FAIL_APM_COMMAND QDI_GIT_STATUS QDI_GIT_PULL_FAIL
  mkdir -p "$HOME" "$FIXTURE_ROOT/bin" "$XDG_CONFIG_HOME/ai-agent-telemetry"
  printf 'AI_AGENT_TELEMETRY_ENDPOINT=https://telemetry.example.test\n' \
    > "$XDG_CONFIG_HOME/ai-agent-telemetry/env"
  : > "$QDI_TEST_LOG"

  cat > "$FIXTURE_ROOT/bin/java" <<'EOF'
#!/bin/sh
printf 'java %s\n' "$*" >> "$QDI_TEST_LOG"
EOF

  cat > "$FIXTURE_ROOT/bin/apm" <<'EOF'
#!/bin/sh
printf 'apm %s\n' "$*" >> "$QDI_TEST_LOG"
if [ "${QDI_FAIL_APM_COMMAND:-}" = "${1:-}" ]; then
  exit 9
fi
case "$*" in
  'marketplace list')
    [ ! -f "$QDI_MARKETPLACE_STATE" ] || printf 'qubership-ai-packages Netcracker/qubership-ai-packages\n'
    ;;
  'marketplace add Netcracker/qubership-ai-packages')
    : > "$QDI_MARKETPLACE_STATE"
    ;;
  view*)
    [ -f "$QDI_APM_STATE" ]
    ;;
  install*)
    : > "$QDI_APM_STATE"
    ;;
esac
EOF
  cp "$FIXTURE_ROOT/bin/apm" "$QDI_APM_CLI"

  cat > "$QDI_APM_INSTALLER" <<'EOF'
#!/bin/sh
printf 'apm-installer %s\n' "$*" >> "$QDI_TEST_LOG"
mkdir -p "$HOME/.local/bin"
cp "$QDI_APM_CLI" "$HOME/.local/bin/apm"
chmod +x "$HOME/.local/bin/apm"
EOF

  cat > "$FIXTURE_ROOT/bin/git" <<'EOF'
#!/bin/sh
printf 'git %s\n' "$*" >> "$QDI_TEST_LOG"
if [ "${1:-}" = config ] && [ "${2:-}" = --global ] && [ "${3:-}" = --get ]; then
  [ -f "$QDI_GIT_CONFIG" ] || exit 1
  cat "$QDI_GIT_CONFIG"
  exit 0
fi
if [ "${1:-}" = config ] && [ "${2:-}" = --global ] && [ "${3:-}" = core.hooksPath ]; then
  printf '%s\n' "$4" > "$QDI_GIT_CONFIG"
  exit 0
fi
if [ "${1:-}" = clone ]; then
  mkdir -p "$3/.git" "$3/hooks-global"
  printf '%s\n' "$2" > "$QDI_GIT_ORIGIN_FILE"
  exit 0
fi
if [ "${1:-}" = -C ] && [ "${3:-}" = rev-parse ]; then
  [ -d "$2/.git" ] || exit 1
  printf 'true\n'
  exit 0
fi
if [ "${1:-}" = -C ] && [ "${3:-}" = remote ] && [ "${4:-}" = get-url ]; then
  [ -f "$QDI_GIT_ORIGIN_FILE" ] || exit 1
  cat "$QDI_GIT_ORIGIN_FILE"
  exit 0
fi
if [ "${1:-}" = -C ] && [ "${3:-}" = status ]; then
  [ -z "${QDI_GIT_STATUS:-}" ] || printf '%s\n' "$QDI_GIT_STATUS"
  exit 0
fi
if [ "${1:-}" = -C ] && [ "${3:-}" = pull ]; then
  [ -z "${QDI_GIT_PULL_FAIL:-}" ] || exit 1
  exit 0
fi
exit 0
EOF

  cat > "$FIXTURE_ROOT/bin/curl" <<'EOF'
#!/bin/sh
printf 'curl %s\n' "$*" >> "$QDI_TEST_LOG"
out=
url=
while [ "$#" -gt 0 ]; do
  case $1 in
    -o)
      shift
      out=$1
      ;;
    http*) url=$1 ;;
  esac
  shift
done
[ -n "$out" ] || exit 2
case $url in
  "$QUBERSHIP_DEV_APM_INSTALL_URL") cp "$QDI_APM_INSTALLER" "$out" ;;
  "$QUBERSHIP_DEV_TELEMETRY_INSTALL_URL") cp "$QDI_TELEMETRY_INSTALLER" "$out" ;;
  *) exit 3 ;;
esac
EOF

  cat > "$QDI_TELEMETRY_INSTALLER" <<'EOF'
#!/bin/sh
printf 'telemetry-installer %s\n' "$*" >> "$QDI_TEST_LOG"
mkdir -p "$HOME/.local/bin"
cp "$QDI_TELEMETRY_CLI" "$HOME/.local/bin/ai-agent-telemetry"
chmod +x "$HOME/.local/bin/ai-agent-telemetry"
EOF

cat > "$QDI_TELEMETRY_CLI" <<'EOF'
#!/bin/sh
printf 'ai-agent-telemetry %s\n' "$*" >> "$QDI_TEST_LOG"
EOF

  chmod +x "$FIXTURE_ROOT/bin/java" "$FIXTURE_ROOT/bin/apm" "$FIXTURE_ROOT/bin/git" \
    "$FIXTURE_ROOT/bin/curl" "$QDI_APM_INSTALLER" "$QDI_APM_CLI" \
    "$QDI_TELEMETRY_INSTALLER" "$QDI_TELEMETRY_CLI"
}

teardown_component_fixture() {
  rm -rf "$FIXTURE_ROOT"
  unset FIXTURE_ROOT HOME XDG_DATA_HOME XDG_CONFIG_HOME QDI_TEST_LOG QDI_GIT_CONFIG QDI_APM_STATE
  unset QDI_MARKETPLACE_STATE QDI_APM_INSTALLER QDI_APM_CLI QDI_TELEMETRY_INSTALLER QDI_TELEMETRY_CLI
  unset QDI_GIT_ORIGIN_FILE QDI_GIT_STATUS QDI_GIT_PULL_FAIL
  unset QUBERSHIP_DEV_APM_INSTALL_URL
  unset QUBERSHIP_DEV_TELEMETRY_INSTALL_URL QUBERSHIP_DEV_GIT_HOOKS_REPOSITORY
  unset QUBERSHIP_DEV_GIT_HOOKS_DIR QDI_FAIL_APM_COMMAND CYBER_FERRET_PASSWORD
  PATH=/usr/bin:/bin
  export PATH
}

run_fixture_installer() {
  set +e
  RUN_OUTPUT=$(sh "$INSTALLER" "$@" 2>&1)
  RUN_CODE=$?
  set -e
}

assert_exit_with() {
  _expected_code=$1
  _expected_text=$2
  shift 2
  set +e
  output=$(sh "$INSTALLER" "$@" 2>&1)
  code=$?
  set -e
  [ "$code" -eq "$_expected_code" ] || fail "expected exit $_expected_code, got $code: $output"
  assert_contains "$output" "$_expected_text"
}

test_help_describes_public_options() {
  output=$(sh "$INSTALLER" --help 2>&1) || fail "--help returned nonzero"
  assert_contains "$output" "--components"
  assert_contains "$output" "--skip"
  assert_contains "$output" "--harnesses"
  assert_contains "$output" "--force-git-hooks"
  assert_contains "$output" "--force-update"
  assert_contains "$output" "--non-interactive"
}

test_invalid_component_fails_before_installation() {
  assert_exit_with 2 'unknown component "unknown"' --components unknown
}

test_invalid_harness_fails_before_installation() {
  assert_exit_with 2 'unknown harness "unknown"' --harnesses unknown
}

test_empty_selection_fails_before_installation() {
  assert_exit_with 2 'no components selected' --skip all
  assert_exit_with 2 'no components selected' --components telemetry --skip telemetry
  assert_exit_with 2 'component list contains an empty value' --components=apm,,telemetry
  assert_exit_with 2 'harness list contains an empty value' --harnesses=claude,
}

test_git_hook_prerequisites_fail_non_interactively() {
  empty_path=$(mktemp -d)
  set +e
  output=$(PATH="$empty_path" /bin/sh "$INSTALLER" --components git-hooks --non-interactive 2>&1)
  code=$?
  set -e
  rm -rf "$empty_path"
  [ "$code" -eq 1 ] || fail "expected prerequisite exit 1, got $code: $output"
  assert_contains "$output" "Git is required"
  assert_contains "$output" "Java is required"
}

test_git_hook_prerequisites_are_not_checked_when_skipped() {
  empty_path=$(mktemp -d)
  set +e
  output=$(PATH="$empty_path" /bin/sh "$INSTALLER" --components telemetry --non-interactive 2>&1)
  code=$?
  set -e
  rm -rf "$empty_path"
  [ "$code" -eq 1 ] || fail "expected incomplete handler exit 1, got $code: $output"
  case $output in
    *"Git is required"*|*"Java is required"*) fail "checked Git or Java for telemetry-only install" ;;
  esac
}

test_declined_prerequisite_installation_stops_bootstrap() {
  empty_path=$(mktemp -d)
  set +e
  output=$(printf 'no\n' | PATH="$empty_path" /bin/sh "$INSTALLER" --components git-hooks 2>&1)
  code=$?
  set -e
  rm -rf "$empty_path"
  [ "$code" -eq 1 ] || fail "expected declined prerequisite exit 1, got $code: $output"
  case $output in
    *"Installation stopped"*|*"prerequisite confirmation"*) ;;
    *) fail "expected declined or unavailable-terminal message: $output" ;;
  esac
}

test_default_install_runs_every_component() {
  setup_component_fixture
  run_fixture_installer --non-interactive
  [ "$RUN_CODE" -eq 0 ] || fail "default install failed: $RUN_OUTPUT"
  assert_log_contains "apm install qubership-global-essentials@qubership-ai-packages -g --target claude,codex,cursor"
  assert_log_contains "apm compile -g"
  assert_log_contains "telemetry-installer --skip-config"
  assert_log_contains "ai-agent-telemetry hooks install --target=claude,codex,cursor"
  assert_log_contains "ai-agent-telemetry status"
  assert_log_contains "ai-agent-telemetry selftest"
  assert_log_contains "git clone https://example.test/pre-commit-global.git $QUBERSHIP_DEV_GIT_HOOKS_DIR"
  assert_contains "$RUN_OUTPUT" "apm              OK"
  assert_contains "$RUN_OUTPUT" "telemetry        OK"
  assert_contains "$RUN_OUTPUT" "git-hooks        OK"
  teardown_component_fixture
}

test_missing_apm_uses_official_bootstrap_contract() {
  setup_component_fixture
  isolated_path="$FIXTURE_ROOT/isolated-bin"
  mkdir -p "$isolated_path"
  for tool in awk grep mktemp rm sh mkdir cp chmod cat sed; do
    /bin/ln -s "$(command -v "$tool")" "$isolated_path/$tool"
  done
  /bin/ln -s "$FIXTURE_ROOT/bin/curl" "$isolated_path/curl"
  PATH=$isolated_path
  export PATH
  run_fixture_installer --components apm --harnesses cursor --non-interactive
  [ "$RUN_CODE" -eq 0 ] || fail "APM bootstrap failed: $RUN_OUTPUT"
  assert_log_contains "curl -fsSL https://example.test/apm-unix -o"
  assert_log_contains "apm-installer"
  assert_log_contains "apm install qubership-global-essentials@qubership-ai-packages -g --target cursor"
  teardown_component_fixture
}

test_selection_and_harnesses_are_forwarded() {
  setup_component_fixture
  run_fixture_installer --components apm,telemetry --skip apm --harnesses codex --non-interactive
  [ "$RUN_CODE" -eq 0 ] || fail "selected install failed: $RUN_OUTPUT"
  assert_log_contains "telemetry-installer --skip-config"
  assert_log_contains "ai-agent-telemetry hooks install --target=codex"
  assert_not_contains "$(cat "$QDI_TEST_LOG")" "apm "
  assert_not_contains "$(cat "$QDI_TEST_LOG")" "git "
  teardown_component_fixture
}

test_force_update_refreshes_selected_components() {
  setup_component_fixture
  : > "$QDI_APM_STATE"
  : > "$QDI_MARKETPLACE_STATE"
  mkdir -p "$QUBERSHIP_DEV_GIT_HOOKS_DIR/.git" "$QUBERSHIP_DEV_GIT_HOOKS_DIR/hooks-global"
  printf '%s\n' "$QUBERSHIP_DEV_GIT_HOOKS_REPOSITORY" > "$QDI_GIT_ORIGIN_FILE"
  run_fixture_installer --force-update --force-git-hooks --harnesses claude --non-interactive
  [ "$RUN_CODE" -eq 0 ] || fail "force update failed: $RUN_OUTPUT"
  assert_log_contains "apm self-update"
  assert_log_contains "apm marketplace update qubership-ai-packages"
  assert_log_contains "apm update qubership-global-essentials -g --yes --target claude"
  assert_log_contains "telemetry-installer --skip-config --force"
  assert_log_contains "ai-agent-telemetry hooks install --target=claude"
  assert_log_contains "git -C $QUBERSHIP_DEV_GIT_HOOKS_DIR pull --ff-only"
  teardown_component_fixture
}

test_existing_unrelated_git_hooks_are_skipped() {
  setup_component_fixture
  printf '/other/hooks\n' > "$QDI_GIT_CONFIG"
  run_fixture_installer --components git-hooks --non-interactive
  [ "$RUN_CODE" -eq 0 ] || fail "git hook skip failed: $RUN_OUTPUT"
  assert_contains "$RUN_OUTPUT" "git-hooks        SKIPPED"
  assert_contains "$RUN_OUTPUT" "core.hooksPath is already set to /other/hooks"
  assert_not_contains "$(cat "$QDI_TEST_LOG")" "git clone"
  [ "$(cat "$QDI_GIT_CONFIG")" = /other/hooks ] || fail "overwrote existing Git hooks"
  teardown_component_fixture
}

test_component_failure_does_not_stop_independent_components() {
  setup_component_fixture
  export QDI_FAIL_APM_COMMAND=compile
  run_fixture_installer --components apm,telemetry --non-interactive
  [ "$RUN_CODE" -eq 1 ] || fail "expected aggregated failure exit 1, got $RUN_CODE: $RUN_OUTPUT"
  assert_contains "$RUN_OUTPUT" "apm              FAILED"
  assert_contains "$RUN_OUTPUT" "telemetry        OK"
  assert_log_contains "telemetry-installer --skip-config"
  assert_log_contains "ai-agent-telemetry hooks install --target=claude,codex,cursor"
  teardown_component_fixture
}

test_unconfigured_telemetry_fails_non_interactively() {
  setup_component_fixture
  rm -f "$XDG_CONFIG_HOME/ai-agent-telemetry/env"
  run_fixture_installer --components telemetry --harnesses cursor --non-interactive
  [ "$RUN_CODE" -eq 1 ] || fail "expected unconfigured telemetry failure: $RUN_OUTPUT"
  assert_contains "$RUN_OUTPUT" "telemetry configuration is required"
  assert_not_contains "$(cat "$QDI_TEST_LOG")" "ai-agent-telemetry configure"
  teardown_component_fixture
}

test_unconfigured_telemetry_configures_interactively() {
  setup_component_fixture
  rm -f "$XDG_CONFIG_HOME/ai-agent-telemetry/env"
  run_fixture_installer --components telemetry --harnesses cursor
  [ "$RUN_CODE" -eq 0 ] || fail "interactive telemetry configuration failed: $RUN_OUTPUT"
  assert_log_contains "ai-agent-telemetry configure --hooks=cursor"
  teardown_component_fixture
}

test_git_hooks_reject_wrong_origin_and_dirty_clone() {
  setup_component_fixture
  mkdir -p "$QUBERSHIP_DEV_GIT_HOOKS_DIR/.git" "$QUBERSHIP_DEV_GIT_HOOKS_DIR/hooks-global"
  printf 'https://example.test/unrelated.git\n' > "$QDI_GIT_ORIGIN_FILE"
  run_fixture_installer --components git-hooks --force-git-hooks --non-interactive
  [ "$RUN_CODE" -eq 1 ] || fail "expected wrong-origin failure: $RUN_OUTPUT"
  assert_contains "$RUN_OUTPUT" "unexpected origin"
  assert_not_contains "$(cat "$QDI_TEST_LOG")" "git config --global core.hooksPath"

  printf '%s\n' "$QUBERSHIP_DEV_GIT_HOOKS_REPOSITORY" > "$QDI_GIT_ORIGIN_FILE"
  export QDI_GIT_STATUS=' M hooks-global/pre-commit'
  run_fixture_installer --components git-hooks --force-git-hooks --force-update --non-interactive
  [ "$RUN_CODE" -eq 1 ] || fail "expected dirty-clone failure: $RUN_OUTPUT"
  assert_contains "$RUN_OUTPUT" "local changes"
  assert_not_contains "$(cat "$QDI_TEST_LOG")" "git -C $QUBERSHIP_DEV_GIT_HOOKS_DIR pull --ff-only"
  teardown_component_fixture
}

test_git_hooks_reject_non_repository_and_divergence() {
  setup_component_fixture
  mkdir -p "$QUBERSHIP_DEV_GIT_HOOKS_DIR/hooks-global"
  run_fixture_installer --components git-hooks --force-git-hooks --non-interactive
  [ "$RUN_CODE" -eq 1 ] || fail "expected non-repository failure: $RUN_OUTPUT"
  assert_contains "$RUN_OUTPUT" "not the managed Git repository"

  mkdir -p "$QUBERSHIP_DEV_GIT_HOOKS_DIR/.git"
  printf '%s\n' "$QUBERSHIP_DEV_GIT_HOOKS_REPOSITORY" > "$QDI_GIT_ORIGIN_FILE"
  export QDI_GIT_PULL_FAIL=1
  run_fixture_installer --components git-hooks --force-git-hooks --force-update --non-interactive
  [ "$RUN_CODE" -eq 1 ] || fail "expected divergent update failure: $RUN_OUTPUT"
  assert_contains "$RUN_OUTPUT" "git-hooks        FAILED"
  teardown_component_fixture
}

test_help_describes_public_options
test_invalid_component_fails_before_installation
test_invalid_harness_fails_before_installation
test_empty_selection_fails_before_installation
test_git_hook_prerequisites_fail_non_interactively
test_git_hook_prerequisites_are_not_checked_when_skipped
test_declined_prerequisite_installation_stops_bootstrap
test_default_install_runs_every_component
test_missing_apm_uses_official_bootstrap_contract
test_selection_and_harnesses_are_forwarded
test_force_update_refreshes_selected_components
test_existing_unrelated_git_hooks_are_skipped
test_component_failure_does_not_stop_independent_components
test_unconfigured_telemetry_fails_non_interactively
test_unconfigured_telemetry_configures_interactively
test_git_hooks_reject_wrong_origin_and_dirty_clone
test_git_hooks_reject_non_repository_and_divergence
printf 'PASS: POSIX developer installer tests\n'
