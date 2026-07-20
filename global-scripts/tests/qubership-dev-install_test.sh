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

assert_log_not_contains() {
  _needle=$1
  if grep -F "$_needle" "$QDI_TEST_LOG" >/dev/null; then
    printf 'command log:\n' >&2
    sed 's/^/  /' "$QDI_TEST_LOG" >&2
    fail "expected command log not to contain: $_needle"
  fi
}

setup_component_fixture() {
  FIXTURE_ROOT=$(mktemp -d)
  export HOME="$FIXTURE_ROOT/home"
  export XDG_DATA_HOME="$FIXTURE_ROOT/data"
  export XDG_CONFIG_HOME="$FIXTURE_ROOT/config"
  export XDG_STATE_HOME="$FIXTURE_ROOT/state"
  export XDG_CACHE_HOME="$FIXTURE_ROOT/cache"
  export QDI_TEST_LOG="$FIXTURE_ROOT/commands.log"
  export QDI_GIT_CONFIG="$FIXTURE_ROOT/git-hooks-path"
  export QDI_MARKETPLACE_STATE="$FIXTURE_ROOT/marketplace-added"
  export QDI_TELEMETRY_INSTALLER="$FIXTURE_ROOT/telemetry-installer.sh"
  export QDI_TELEMETRY_CLI="$FIXTURE_ROOT/ai-agent-telemetry"
  export QDI_GIT_ORIGIN_FILE="$FIXTURE_ROOT/git-origin"
  export QDI_APM_INSTALLER="$FIXTURE_ROOT/apm-installer.sh"
  export QDI_APM_CLI="$FIXTURE_ROOT/apm"
  export QDI_TELEMETRY_RECEIPT="$XDG_STATE_HOME/ai-agent-telemetry/hooks-uninstalled"
  export QDI_TELEMETRY_CONFIG_DIR="$XDG_CONFIG_HOME/ai-agent-telemetry"
  export QDI_TELEMETRY_CACHE_DIR="$XDG_CACHE_HOME/ai-agent-telemetry"
  export QDI_TELEMETRY_HOOK="$HOME/.codex/hooks.json"
  export QDI_MANAGED_TELEMETRY_BIN="$HOME/.local/bin/ai-agent-telemetry"
  export QUBERSHIP_DEV_APM_INSTALL_URL=https://example.test/apm-unix
  export QUBERSHIP_DEV_TELEMETRY_INSTALL_URL=https://example.test/install.sh
  export QUBERSHIP_DEV_GIT_HOOKS_REPOSITORY=https://example.test/pre-commit-global.git
  export QUBERSHIP_DEV_GIT_HOOKS_DIR="$XDG_DATA_HOME/qubership/pre-commit-global"
  export PATH="$FIXTURE_ROOT/bin:/usr/bin:/bin"
  unset CYBER_FERRET_PASSWORD QDI_FAIL_APM_COMMAND QDI_FAIL_TELEMETRY_HOOKS
  unset QDI_GIT_STATUS QDI_GIT_PULL_FAIL QDI_FAIL_GIT_ORIGIN QDI_FAIL_GIT_STATUS
  unset QDI_TEST_JAVA_EXIT_CODE QDI_TEST_JAVA_SPEC_VERSION
  mkdir -p "$HOME" "$FIXTURE_ROOT/bin" "$XDG_CONFIG_HOME/ai-agent-telemetry"
  printf 'AI_AGENT_TELEMETRY_ENDPOINT=https://telemetry.example.test\n' \
    > "$XDG_CONFIG_HOME/ai-agent-telemetry/env"
  : > "$QDI_TEST_LOG"

  cat > "$FIXTURE_ROOT/bin/java" <<'EOF'
#!/bin/sh
printf 'java %s\n' "$*" >> "$QDI_TEST_LOG"
if [ "${QDI_TEST_JAVA_EXIT_CODE:-0}" -ne 0 ]; then
  exit "$QDI_TEST_JAVA_EXIT_CODE"
fi
printf '    java.specification.version = %s\n' "${QDI_TEST_JAVA_SPEC_VERSION:-21}" >&2
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
    exit 1
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
if [ "${1:-}" = config ] && [ "${2:-}" = --global ] && [ "${3:-}" = --unset-all ]; then
  rm -f "$QDI_GIT_CONFIG"
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
  [ "${QDI_FAIL_GIT_ORIGIN:-0}" -eq 0 ] || exit 8
  [ -f "$QDI_GIT_ORIGIN_FILE" ] || exit 1
  cat "$QDI_GIT_ORIGIN_FILE"
  exit 0
fi
if [ "${1:-}" = -C ] && [ "${3:-}" = status ]; then
  [ "${QDI_FAIL_GIT_STATUS:-0}" -eq 0 ] || exit 8
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
if [ "${QDI_FAIL_TELEMETRY_HOOKS:-0}" -eq 1 ] &&
  [ "${1:-}" = hooks ] && [ "${2:-}" = uninstall ]; then
  exit 9
fi
EOF

  chmod +x "$FIXTURE_ROOT/bin/java" "$FIXTURE_ROOT/bin/apm" "$FIXTURE_ROOT/bin/git" \
    "$FIXTURE_ROOT/bin/curl" "$QDI_APM_INSTALLER" "$QDI_APM_CLI" \
    "$QDI_TELEMETRY_INSTALLER" "$QDI_TELEMETRY_CLI"
}

teardown_component_fixture() {
  rm -rf "$FIXTURE_ROOT"
  unset FIXTURE_ROOT HOME XDG_DATA_HOME XDG_CONFIG_HOME XDG_STATE_HOME XDG_CACHE_HOME
  unset QDI_TEST_LOG QDI_GIT_CONFIG
  unset QDI_MARKETPLACE_STATE QDI_APM_INSTALLER QDI_APM_CLI QDI_TELEMETRY_INSTALLER QDI_TELEMETRY_CLI
  unset QDI_GIT_ORIGIN_FILE QDI_GIT_STATUS QDI_GIT_PULL_FAIL
  unset QDI_FAIL_GIT_ORIGIN QDI_FAIL_GIT_STATUS
  unset QDI_TELEMETRY_RECEIPT QDI_TELEMETRY_CONFIG_DIR QDI_TELEMETRY_CACHE_DIR
  unset QDI_TELEMETRY_HOOK QDI_MANAGED_TELEMETRY_BIN QDI_FAIL_TELEMETRY_HOOKS
  unset QUBERSHIP_DEV_APM_INSTALL_URL
  unset QUBERSHIP_DEV_TELEMETRY_INSTALL_URL QUBERSHIP_DEV_GIT_HOOKS_REPOSITORY
  unset QUBERSHIP_DEV_GIT_HOOKS_DIR QDI_FAIL_APM_COMMAND CYBER_FERRET_PASSWORD
  unset QDI_TEST_JAVA_EXIT_CODE QDI_TEST_JAVA_SPEC_VERSION
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
  assert_contains "$output" "--uninstall"
  assert_contains "$output" "--purge"
  assert_contains "$output" "Uninstall the selected Qubership developer tools."
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

test_uninstall_option_combinations_are_rejected_before_changes() {
  assert_exit_with 2 '--purge requires --uninstall' --purge
  assert_exit_with 2 '--harnesses is not valid with --uninstall' --uninstall --harnesses claude
  assert_exit_with 2 '--force-update is not valid with --uninstall' --uninstall --force-update
  assert_exit_with 2 '--force-git-hooks is not valid with --uninstall' --uninstall --force-git-hooks
  assert_exit_with 2 '--non-interactive is not valid with --uninstall' --uninstall --non-interactive
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
  assert_contains "$output" "Java 21 or newer is required"
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

test_java_20_is_rejected() {
  setup_component_fixture
  QDI_TEST_JAVA_SPEC_VERSION=20
  export QDI_TEST_JAVA_SPEC_VERSION
  run_fixture_installer --components git-hooks --non-interactive
  [ "$RUN_CODE" -eq 1 ] || fail "expected Java 20 rejection: $RUN_OUTPUT"
  assert_contains "$RUN_OUTPUT" "Detected Java 20"
  assert_contains "$RUN_OUTPUT" "Java 21 or newer is required"
  assert_log_not_contains "git clone"
  teardown_component_fixture
}

test_java_21_and_newer_are_accepted() {
  for version in 21 26; do
    setup_component_fixture
    QDI_TEST_JAVA_SPEC_VERSION=$version
    export QDI_TEST_JAVA_SPEC_VERSION
    run_fixture_installer --components git-hooks --non-interactive
    [ "$RUN_CODE" -eq 0 ] || fail "expected Java $version acceptance: $RUN_OUTPUT"
    teardown_component_fixture
  done
}

test_unrecognized_or_failing_java_is_rejected() {
  setup_component_fixture
  QDI_TEST_JAVA_SPEC_VERSION=unknown
  export QDI_TEST_JAVA_SPEC_VERSION
  run_fixture_installer --components git-hooks --non-interactive
  [ "$RUN_CODE" -eq 1 ] || fail "expected malformed Java rejection: $RUN_OUTPUT"
  assert_contains "$RUN_OUTPUT" "Could not determine the Java version"
  teardown_component_fixture

  setup_component_fixture
  QDI_TEST_JAVA_EXIT_CODE=1
  export QDI_TEST_JAVA_EXIT_CODE
  run_fixture_installer --components git-hooks --non-interactive
  [ "$RUN_CODE" -eq 1 ] || fail "expected failing Java rejection: $RUN_OUTPUT"
  assert_contains "$RUN_OUTPUT" "Could not determine the Java version"
  teardown_component_fixture
}

test_default_install_runs_every_component() {
  setup_component_fixture
  run_fixture_installer --non-interactive
  [ "$RUN_CODE" -eq 0 ] || fail "default install failed: $RUN_OUTPUT"
  assert_log_contains "apm self-update"
  assert_log_contains "apm install qubership-global-essentials@qubership-ai-packages -g --target claude,codex,cursor"
  assert_log_contains "apm compile -g"
  assert_log_contains "apm deps list -g"
  assert_log_contains "telemetry-installer --skip-config"
  assert_log_not_contains "telemetry-installer --skip-config --force"
  assert_log_contains "ai-agent-telemetry hooks install --target=claude,codex,cursor"
  assert_log_contains "ai-agent-telemetry status"
  assert_log_contains "ai-agent-telemetry selftest"
  assert_log_contains "git clone https://example.test/pre-commit-global.git $QUBERSHIP_DEV_GIT_HOOKS_DIR"
  assert_contains "$RUN_OUTPUT" "apm              OK"
  assert_contains "$RUN_OUTPUT" "telemetry        OK"
  assert_contains "$RUN_OUTPUT" "git-hooks        OK"
  assert_contains "$RUN_OUTPUT" "export CYBER_FERRET_PASSWORD='<password>'"
  assert_contains "$RUN_OUTPUT" "global-scripts/README.md#cyberferret-password"
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
  assert_log_not_contains "apm self-update"
  assert_log_contains "apm install qubership-global-essentials@qubership-ai-packages -g --target cursor"
  teardown_component_fixture
}

test_existing_clis_are_updated_by_default() {
  setup_component_fixture
  cat > "$FIXTURE_ROOT/bin/ai-agent-telemetry" <<'EOF'
#!/bin/sh
printf 'old-ai-agent-telemetry %s\n' "$*" >> "$QDI_TEST_LOG"
EOF
  chmod +x "$FIXTURE_ROOT/bin/ai-agent-telemetry"
  run_fixture_installer --components apm,telemetry --non-interactive
  [ "$RUN_CODE" -eq 0 ] || fail "existing CLI update failed: $RUN_OUTPUT"
  assert_log_contains "apm self-update"
  assert_log_contains "telemetry-installer --skip-config --force"
  assert_log_not_contains "old-ai-agent-telemetry hooks install"
  assert_log_contains "ai-agent-telemetry hooks install --target=claude,codex,cursor"
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
  : > "$QDI_MARKETPLACE_STATE"
  mkdir -p "$QUBERSHIP_DEV_GIT_HOOKS_DIR/.git" "$QUBERSHIP_DEV_GIT_HOOKS_DIR/hooks-global"
  printf '%s\n' "$QUBERSHIP_DEV_GIT_HOOKS_REPOSITORY" > "$QDI_GIT_ORIGIN_FILE"
  run_fixture_installer --force-update --force-git-hooks --harnesses claude --non-interactive
  [ "$RUN_CODE" -eq 0 ] || fail "force update failed: $RUN_OUTPUT"
  assert_log_contains "apm self-update"
  assert_log_contains "apm marketplace update qubership-ai-packages"
  assert_log_contains \
    "apm install --update qubership-global-essentials@qubership-ai-packages -g --target claude"
  assert_log_contains "telemetry-installer --skip-config --force"
  assert_log_contains "ai-agent-telemetry hooks install --target=claude"
  assert_log_contains "git -C $QUBERSHIP_DEV_GIT_HOOKS_DIR pull --ff-only"
  teardown_component_fixture
}

test_force_update_refreshes_git_hooks_through_symlink() {
  setup_component_fixture
  _physical_data="$FIXTURE_ROOT/physical-data"
  _linked_data="$FIXTURE_ROOT/linked-data"
  QUBERSHIP_DEV_GIT_HOOKS_DIR="$_linked_data/qubership/pre-commit-global"
  export QUBERSHIP_DEV_GIT_HOOKS_DIR
  mkdir -p \
    "$_physical_data/qubership/pre-commit-global/.git" \
    "$_physical_data/qubership/pre-commit-global/hooks-global"
  ln -s "$_physical_data" "$_linked_data"
  printf '%s\n' "$QUBERSHIP_DEV_GIT_HOOKS_REPOSITORY" > "$QDI_GIT_ORIGIN_FILE"
  (CDPATH='' cd -- "$QUBERSHIP_DEV_GIT_HOOKS_DIR/hooks-global" && pwd -P) > "$QDI_GIT_CONFIG"

  run_fixture_installer --components git-hooks --force-update --non-interactive

  [ "$RUN_CODE" -eq 0 ] || fail "symlinked Git hooks update failed: $RUN_OUTPUT"
  assert_log_contains "git -C $QUBERSHIP_DEV_GIT_HOOKS_DIR pull --ff-only"
  assert_contains "$RUN_OUTPUT" "git-hooks        OK"
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

test_apm_uninstall_skips_missing_manifest() {
  setup_component_fixture
  run_fixture_installer --uninstall --components apm
  [ "$RUN_CODE" -eq 0 ] || fail "missing-manifest uninstall failed: $RUN_OUTPUT"
  assert_contains "$RUN_OUTPUT" "apm              SKIPPED"
  assert_log_not_contains "apm uninstall"
  teardown_component_fixture
}

test_apm_uninstall_invokes_global_package_removal() {
  setup_component_fixture
  mkdir -p "$HOME/.apm"
  : > "$HOME/.apm/apm.yml"
  : > "$QDI_MARKETPLACE_STATE"
  run_fixture_installer --uninstall --components apm
  [ "$RUN_CODE" -eq 0 ] || fail "APM uninstall failed: $RUN_OUTPUT"
  assert_log_contains "apm uninstall -g qubership-global-essentials@qubership-ai-packages"
  assert_contains "$RUN_OUTPUT" "apm              OK"
  [ -x "$FIXTURE_ROOT/bin/apm" ] || fail "removed APM CLI"
  [ -f "$QDI_MARKETPLACE_STATE" ] || fail "removed marketplace marker"
  teardown_component_fixture
}

test_apm_uninstall_failure_does_not_stop_telemetry() {
  setup_component_fixture
  mkdir -p "$HOME/.apm" "$(dirname "$QDI_MANAGED_TELEMETRY_BIN")"
  : > "$HOME/.apm/apm.yml"
  cp "$QDI_TELEMETRY_CLI" "$QDI_MANAGED_TELEMETRY_BIN"
  chmod +x "$QDI_MANAGED_TELEMETRY_BIN"
  export QDI_FAIL_APM_COMMAND=uninstall
  run_fixture_installer --uninstall --components apm,telemetry
  [ "$RUN_CODE" -eq 1 ] || fail "expected aggregated uninstall failure: $RUN_OUTPUT"
  assert_contains "$RUN_OUTPUT" "apm              FAILED"
  assert_contains "$RUN_OUTPUT" "telemetry        OK"
  assert_log_contains "ai-agent-telemetry hooks uninstall"
  teardown_component_fixture
}

test_telemetry_uninstall_removes_hooks_before_managed_binary() {
  setup_component_fixture
  mkdir -p "$(dirname "$QDI_MANAGED_TELEMETRY_BIN")" "$QDI_TELEMETRY_CACHE_DIR"
  : > "$QDI_TELEMETRY_CACHE_DIR/cache.db"
  cp "$QDI_TELEMETRY_CLI" "$QDI_MANAGED_TELEMETRY_BIN"
  chmod +x "$QDI_MANAGED_TELEMETRY_BIN"
  run_fixture_installer --uninstall --components telemetry
  [ "$RUN_CODE" -eq 0 ] || fail "telemetry uninstall failed: $RUN_OUTPUT"
  assert_log_contains "ai-agent-telemetry hooks uninstall"
  [ ! -e "$QDI_MANAGED_TELEMETRY_BIN" ] || fail "managed telemetry binary was preserved"
  [ -d "$QDI_TELEMETRY_CONFIG_DIR" ] || fail "normal uninstall removed telemetry config"
  [ -d "$QDI_TELEMETRY_CACHE_DIR" ] || fail "normal uninstall removed telemetry cache"
  assert_contains "$RUN_OUTPUT" "Uninstall summary"
  teardown_component_fixture
}

test_telemetry_hook_failure_preserves_managed_binary() {
  setup_component_fixture
  mkdir -p "$(dirname "$QDI_MANAGED_TELEMETRY_BIN")"
  cp "$QDI_TELEMETRY_CLI" "$QDI_MANAGED_TELEMETRY_BIN"
  chmod +x "$QDI_MANAGED_TELEMETRY_BIN"
  export QDI_FAIL_TELEMETRY_HOOKS=1
  run_fixture_installer --uninstall --components telemetry
  [ "$RUN_CODE" -eq 1 ] || fail "expected telemetry hook failure: $RUN_OUTPUT"
  [ -x "$QDI_MANAGED_TELEMETRY_BIN" ] || fail "removed managed binary after hook failure"
  teardown_component_fixture
}

test_telemetry_uninstall_preserves_external_path_command() {
  setup_component_fixture
  cp "$QDI_TELEMETRY_CLI" "$FIXTURE_ROOT/bin/ai-agent-telemetry"
  chmod +x "$FIXTURE_ROOT/bin/ai-agent-telemetry"
  run_fixture_installer --uninstall --components telemetry
  [ "$RUN_CODE" -eq 0 ] || fail "external telemetry uninstall failed: $RUN_OUTPUT"
  assert_log_contains "ai-agent-telemetry hooks uninstall"
  [ -x "$FIXTURE_ROOT/bin/ai-agent-telemetry" ] || fail "removed external telemetry command"
  teardown_component_fixture
}

test_telemetry_uninstall_accepts_valid_receipt_on_repeat() {
  setup_component_fixture
  mkdir -p "$(dirname "$QDI_TELEMETRY_RECEIPT")"
  printf 'version=1\nstate=uninstalled\n' > "$QDI_TELEMETRY_RECEIPT"
  run_fixture_installer --uninstall --components telemetry
  [ "$RUN_CODE" -eq 0 ] || fail "repeat telemetry uninstall failed: $RUN_OUTPUT"
  [ -f "$QDI_TELEMETRY_RECEIPT" ] || fail "removed telemetry receipt"
  teardown_component_fixture
}

test_telemetry_uninstall_fails_closed_without_cli_or_receipt() {
  setup_component_fixture
  mkdir -p "$(dirname "$QDI_TELEMETRY_HOOK")"
  : > "$QDI_TELEMETRY_HOOK"
  run_fixture_installer --uninstall --components telemetry
  [ "$RUN_CODE" -eq 1 ] || fail "expected unsafe telemetry uninstall failure: $RUN_OUTPUT"
  assert_contains "$RUN_OUTPUT" "native hook files exist"
  [ -e "$QDI_TELEMETRY_HOOK" ] || fail "removed hook without telemetry ownership proof"
  teardown_component_fixture
}

test_telemetry_uninstall_treats_dangling_hook_symlink_as_existing() {
  setup_component_fixture
  mkdir -p "$(dirname "$QDI_TELEMETRY_HOOK")"
  ln -s "$FIXTURE_ROOT/missing-hook-target" "$QDI_TELEMETRY_HOOK"
  run_fixture_installer --uninstall --components telemetry
  [ "$RUN_CODE" -eq 1 ] || fail "expected dangling-hook telemetry uninstall failure: $RUN_OUTPUT"
  assert_contains "$RUN_OUTPUT" "native hook files exist"
  [ -L "$QDI_TELEMETRY_HOOK" ] || fail "removed dangling hook symlink"
  [ ! -e "$QDI_TELEMETRY_RECEIPT" ] || fail "wrote receipt with dangling native hook symlink"
  teardown_component_fixture
}

test_telemetry_uninstall_writes_receipt_when_no_hooks_exist() {
  setup_component_fixture
  run_fixture_installer --uninstall --components telemetry
  [ "$RUN_CODE" -eq 0 ] || fail "receipt-only telemetry uninstall failed: $RUN_OUTPUT"
  [ "$(cat "$QDI_TELEMETRY_RECEIPT")" = "$(printf 'version=1\nstate=uninstalled')" ] ||
    fail "telemetry receipt has unexpected content"
  teardown_component_fixture
}

test_telemetry_purge_removes_only_package_config_and_cache() {
  setup_component_fixture
  mkdir -p "$QDI_TELEMETRY_CONFIG_DIR" "$QDI_TELEMETRY_CACHE_DIR"
  mkdir -p "$(dirname "$QDI_TELEMETRY_RECEIPT")"
  : > "$QDI_TELEMETRY_CONFIG_DIR/config.yaml"
  : > "$QDI_TELEMETRY_CACHE_DIR/cache.db"
  : > "$QDI_MARKETPLACE_STATE"
  printf 'version=1\nstate=uninstalled\n' > "$QDI_TELEMETRY_RECEIPT"
  run_fixture_installer --uninstall --purge --components telemetry
  [ "$RUN_CODE" -eq 0 ] || fail "telemetry purge failed: $RUN_OUTPUT"
  [ ! -e "$QDI_TELEMETRY_CONFIG_DIR" ] || fail "telemetry config directory remains"
  [ ! -e "$QDI_TELEMETRY_CACHE_DIR" ] || fail "telemetry cache directory remains"
  [ -f "$QDI_TELEMETRY_RECEIPT" ] || fail "purge removed telemetry receipt"
  [ -f "$QDI_MARKETPLACE_STATE" ] || fail "purge removed marketplace marker"
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

test_git_hooks_uninstall_deactivates_exact_managed_path() {
  setup_component_fixture
  printf '%s/hooks-global\n' "$QUBERSHIP_DEV_GIT_HOOKS_DIR" > "$QDI_GIT_CONFIG"
  run_fixture_installer --uninstall --components git-hooks
  [ "$RUN_CODE" -eq 0 ] || fail "Git hooks deactivation failed: $RUN_OUTPUT"
  assert_log_contains "git config --global --unset-all core.hooksPath"
  [ ! -e "$QDI_GIT_CONFIG" ] || fail "managed core.hooksPath remains configured"
  assert_log_not_contains "java "
  teardown_component_fixture
}

test_git_hooks_uninstall_fails_without_git_and_continues() {
  setup_component_fixture
  isolated_path="$FIXTURE_ROOT/no-git-bin"
  mkdir -p "$isolated_path"
  for tool in awk cat dirname mkdir mv rm sh; do
    ln -s "$(command -v "$tool")" "$isolated_path/$tool"
  done
  PATH=$isolated_path
  export PATH
  run_fixture_installer --uninstall --components git-hooks,telemetry
  [ "$RUN_CODE" -eq 1 ] || fail "expected missing-Git uninstall failure: $RUN_OUTPUT"
  assert_contains "$RUN_OUTPUT" "git-hooks: cannot uninstall because Git is not on PATH"
  assert_contains "$RUN_OUTPUT" "git-hooks        FAILED"
  assert_contains "$RUN_OUTPUT" "telemetry        OK"
  [ -f "$QDI_TELEMETRY_RECEIPT" ] || fail "later telemetry component did not run"
  teardown_component_fixture
}

test_git_hooks_uninstall_preserves_relative_configured_path() {
  setup_component_fixture
  managed_parent=$(dirname "$QUBERSHIP_DEV_GIT_HOOKS_DIR")
  mkdir -p "$QUBERSHIP_DEV_GIT_HOOKS_DIR/.git" "$QUBERSHIP_DEV_GIT_HOOKS_DIR/hooks-global"
  printf '%s\n' "$QUBERSHIP_DEV_GIT_HOOKS_REPOSITORY" > "$QDI_GIT_ORIGIN_FILE"
  printf 'pre-commit-global/hooks-global\n' > "$QDI_GIT_CONFIG"
  set +e
  RUN_OUTPUT=$(CDPATH='' cd -- "$managed_parent" && sh "$INSTALLER" --uninstall --components git-hooks 2>&1)
  RUN_CODE=$?
  set -e
  [ "$RUN_CODE" -eq 0 ] || fail "relative-path Git hooks uninstall failed: $RUN_OUTPUT"
  [ "$(cat "$QDI_GIT_CONFIG")" = pre-commit-global/hooks-global ] ||
    fail "unset relative core.hooksPath after cwd-based canonicalization"
  assert_log_not_contains "git config --global --unset-all core.hooksPath"
  teardown_component_fixture
}

test_git_hooks_uninstall_reports_origin_inspection_failure() {
  setup_component_fixture
  mkdir -p "$QUBERSHIP_DEV_GIT_HOOKS_DIR/.git" "$QUBERSHIP_DEV_GIT_HOOKS_DIR/hooks-global"
  export QDI_FAIL_GIT_ORIGIN=1
  run_fixture_installer --uninstall --components git-hooks
  [ "$RUN_CODE" -eq 1 ] || fail "expected origin inspection failure: $RUN_OUTPUT"
  assert_contains "$RUN_OUTPUT" "git-hooks: cannot read origin for $QUBERSHIP_DEV_GIT_HOOKS_DIR"
  [ -d "$QUBERSHIP_DEV_GIT_HOOKS_DIR" ] || fail "removed clone after origin inspection failure"
  teardown_component_fixture
}

test_git_hooks_uninstall_reports_status_inspection_failure() {
  setup_component_fixture
  mkdir -p "$QUBERSHIP_DEV_GIT_HOOKS_DIR/.git" "$QUBERSHIP_DEV_GIT_HOOKS_DIR/hooks-global"
  printf '%s\n' "$QUBERSHIP_DEV_GIT_HOOKS_REPOSITORY" > "$QDI_GIT_ORIGIN_FILE"
  export QDI_FAIL_GIT_STATUS=1
  run_fixture_installer --uninstall --components git-hooks
  [ "$RUN_CODE" -eq 1 ] || fail "expected status inspection failure: $RUN_OUTPUT"
  assert_contains "$RUN_OUTPUT" "git-hooks: cannot inspect worktree status for $QUBERSHIP_DEV_GIT_HOOKS_DIR"
  [ -d "$QUBERSHIP_DEV_GIT_HOOKS_DIR" ] || fail "removed clone after status inspection failure"
  teardown_component_fixture
}

test_git_hooks_uninstall_preserves_unrelated_path() {
  setup_component_fixture
  printf '/other/hooks\n' > "$QDI_GIT_CONFIG"
  run_fixture_installer --uninstall --components git-hooks
  [ "$RUN_CODE" -eq 0 ] || fail "unrelated Git hooks uninstall failed: $RUN_OUTPUT"
  [ "$(cat "$QDI_GIT_CONFIG")" = /other/hooks ] || fail "changed unrelated core.hooksPath"
  assert_log_not_contains "git config --global --unset-all core.hooksPath"
  teardown_component_fixture
}

test_git_hooks_uninstall_accepts_missing_clone() {
  setup_component_fixture
  run_fixture_installer --uninstall --components git-hooks
  [ "$RUN_CODE" -eq 0 ] || fail "missing Git hooks clone uninstall failed: $RUN_OUTPUT"
  assert_contains "$RUN_OUTPUT" "git-hooks        OK"
  teardown_component_fixture
}

test_git_hooks_uninstall_removes_clean_expected_clone() {
  setup_component_fixture
  mkdir -p "$QUBERSHIP_DEV_GIT_HOOKS_DIR/.git" "$QUBERSHIP_DEV_GIT_HOOKS_DIR/hooks-global"
  printf '%s\n' "$QUBERSHIP_DEV_GIT_HOOKS_REPOSITORY" > "$QDI_GIT_ORIGIN_FILE"
  run_fixture_installer --uninstall --components git-hooks
  [ "$RUN_CODE" -eq 0 ] || fail "clean Git hooks clone uninstall failed: $RUN_OUTPUT"
  [ ! -e "$QUBERSHIP_DEV_GIT_HOOKS_DIR" ] || fail "clean managed Git hooks clone remains"
  teardown_component_fixture
}

test_git_hooks_uninstall_preserves_wrong_origin_clone() {
  setup_component_fixture
  mkdir -p "$QUBERSHIP_DEV_GIT_HOOKS_DIR/.git" "$QUBERSHIP_DEV_GIT_HOOKS_DIR/hooks-global"
  printf 'https://example.test/unrelated.git\n' > "$QDI_GIT_ORIGIN_FILE"
  run_fixture_installer --uninstall --components git-hooks
  [ "$RUN_CODE" -eq 1 ] || fail "expected wrong-origin uninstall failure: $RUN_OUTPUT"
  assert_contains "$RUN_OUTPUT" "because its origin is https://example.test/unrelated.git"
  [ -d "$QUBERSHIP_DEV_GIT_HOOKS_DIR" ] || fail "removed wrong-origin Git hooks clone"
  teardown_component_fixture
}

test_git_hooks_uninstall_preserves_dirty_clone() {
  setup_component_fixture
  mkdir -p "$QUBERSHIP_DEV_GIT_HOOKS_DIR/.git" "$QUBERSHIP_DEV_GIT_HOOKS_DIR/hooks-global"
  printf '%s\n' "$QUBERSHIP_DEV_GIT_HOOKS_REPOSITORY" > "$QDI_GIT_ORIGIN_FILE"
  export QDI_GIT_STATUS=' M hooks-global/pre-commit'
  run_fixture_installer --uninstall --components git-hooks
  [ "$RUN_CODE" -eq 1 ] || fail "expected dirty-clone uninstall failure: $RUN_OUTPUT"
  assert_contains "$RUN_OUTPUT" "preserving modified worktree"
  [ -d "$QUBERSHIP_DEV_GIT_HOOKS_DIR" ] || fail "removed modified Git hooks clone"
  teardown_component_fixture
}

test_git_hooks_uninstall_deactivates_before_clone_validation_failure() {
  setup_component_fixture
  mkdir -p "$QUBERSHIP_DEV_GIT_HOOKS_DIR/hooks-global"
  (CDPATH='' cd -- "$QUBERSHIP_DEV_GIT_HOOKS_DIR/hooks-global" && pwd -P) > "$QDI_GIT_CONFIG"
  run_fixture_installer --uninstall --components git-hooks
  [ "$RUN_CODE" -eq 1 ] || fail "expected non-worktree uninstall failure: $RUN_OUTPUT"
  assert_contains "$RUN_OUTPUT" "because it is not a Git worktree"
  [ ! -e "$QDI_GIT_CONFIG" ] || fail "core.hooksPath remains after clone validation failure"
  [ -d "$QUBERSHIP_DEV_GIT_HOOKS_DIR" ] || fail "removed non-worktree Git hooks directory"
  teardown_component_fixture
}

test_help_describes_public_options
test_invalid_component_fails_before_installation
test_invalid_harness_fails_before_installation
test_empty_selection_fails_before_installation
test_uninstall_option_combinations_are_rejected_before_changes
test_git_hook_prerequisites_fail_non_interactively
test_git_hook_prerequisites_are_not_checked_when_skipped
test_declined_prerequisite_installation_stops_bootstrap
test_java_20_is_rejected
test_java_21_and_newer_are_accepted
test_unrecognized_or_failing_java_is_rejected
test_default_install_runs_every_component
test_missing_apm_uses_official_bootstrap_contract
test_existing_clis_are_updated_by_default
test_selection_and_harnesses_are_forwarded
test_force_update_refreshes_selected_components
test_force_update_refreshes_git_hooks_through_symlink
test_existing_unrelated_git_hooks_are_skipped
test_component_failure_does_not_stop_independent_components
test_apm_uninstall_skips_missing_manifest
test_apm_uninstall_invokes_global_package_removal
test_apm_uninstall_failure_does_not_stop_telemetry
test_telemetry_uninstall_removes_hooks_before_managed_binary
test_telemetry_hook_failure_preserves_managed_binary
test_telemetry_uninstall_preserves_external_path_command
test_telemetry_uninstall_accepts_valid_receipt_on_repeat
test_telemetry_uninstall_fails_closed_without_cli_or_receipt
test_telemetry_uninstall_treats_dangling_hook_symlink_as_existing
test_telemetry_uninstall_writes_receipt_when_no_hooks_exist
test_telemetry_purge_removes_only_package_config_and_cache
test_unconfigured_telemetry_fails_non_interactively
test_unconfigured_telemetry_configures_interactively
test_git_hooks_reject_wrong_origin_and_dirty_clone
test_git_hooks_reject_non_repository_and_divergence
test_git_hooks_uninstall_deactivates_exact_managed_path
test_git_hooks_uninstall_fails_without_git_and_continues
test_git_hooks_uninstall_preserves_relative_configured_path
test_git_hooks_uninstall_reports_origin_inspection_failure
test_git_hooks_uninstall_reports_status_inspection_failure
test_git_hooks_uninstall_preserves_unrelated_path
test_git_hooks_uninstall_accepts_missing_clone
test_git_hooks_uninstall_removes_clean_expected_clone
test_git_hooks_uninstall_preserves_wrong_origin_clone
test_git_hooks_uninstall_preserves_dirty_clone
test_git_hooks_uninstall_deactivates_before_clone_validation_failure
printf 'PASS: POSIX developer installer tests\n'
