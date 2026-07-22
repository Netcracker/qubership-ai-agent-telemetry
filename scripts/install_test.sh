#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
INSTALLER="$SCRIPT_DIR/install.sh"

fail() {
  printf 'FAIL: %s\n' "$1" >&2
  exit 1
}

assert_contains() {
  case $1 in
    *"$2"*) ;;
    *) fail "expected output to contain: $2" ;;
  esac
}

assert_not_contains() {
  case $1 in
    *"$2"*) fail "expected output not to contain: $2" ;;
    *) ;;
  esac
}

setup_fixture() {
  FIXTURE_ROOT=$(TMPDIR=/tmp mktemp -d)
  export FIXTURE_ROOT
  export HOME="$FIXTURE_ROOT/home"
  export TMPDIR="$FIXTURE_ROOT/tmp"
  export QDI_TEST_LOG="$FIXTURE_ROOT/curl.log"
  export QDI_EXEC_LOG="$FIXTURE_ROOT/exec.log"
  export QDI_INPUT_LOG="$FIXTURE_ROOT/input.log"
  export QDI_BINARY_EXIT=0
  export QDI_READ_INPUT=0
  export QDI_SLEEP_SECONDS=0
  export QDI_OS=Linux
  export QDI_ARCH=x86_64
  export QDI_DOWNLOAD_FAIL=0
  export QDI_RESPONSE_BODY='private-response-body'
  export AI_AGENT_TELEMETRY_INSTALL_BASE_URL=https://release.example.test/releases
  unset AI_AGENT_TELEMETRY_INSTALL_VERSION AI_AGENT_TELEMETRY_TOKEN
  mkdir -p "$HOME" "$TMPDIR" "$FIXTURE_ROOT/bin"
  : > "$QDI_TEST_LOG"

  cat > "$FIXTURE_ROOT/asset" <<'EOF'
#!/bin/sh
printf '%s\n' "$@" > "$QDI_EXEC_LOG"
if [ "${QDI_READ_INPUT:-0}" = 1 ]; then
  printf 'Collector endpoint: ' >&2
  if ! IFS= read -r answer; then
    exit 64
  fi
  printf '%s\n' "$answer" > "$QDI_INPUT_LOG"
fi
if [ "${QDI_SLEEP_SECONDS:-0}" -gt 0 ]; then
  : > "$FIXTURE_ROOT/child-ready"
  sleep "$QDI_SLEEP_SECONDS"
fi
exit "${QDI_BINARY_EXIT:-0}"
EOF
  chmod +x "$FIXTURE_ROOT/asset"

  cat > "$FIXTURE_ROOT/bin/uname" <<'EOF'
#!/bin/sh
case ${1:-} in
  -s) printf '%s\n' "$QDI_OS" ;;
  -m) printf '%s\n' "$QDI_ARCH" ;;
  *) exit 2 ;;
esac
EOF

  cat > "$FIXTURE_ROOT/bin/curl" <<'EOF'
#!/bin/sh
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
if [ -z "$out" ]; then
  cat "$QDI_BOOTSTRAP_SOURCE"
  exit 0
fi
printf '%s\n' "$url" >> "$QDI_TEST_LOG"
if [ "${QDI_DOWNLOAD_FAIL:-0}" = 1 ]; then
  printf '%s\n' "$QDI_RESPONSE_BODY"
  exit 22
fi
case $url in
  */SHA256SUMS) cp "$FIXTURE_ROOT/SHA256SUMS" "$out" ;;
  *) cp "$FIXTURE_ROOT/asset" "$out" ;;
esac
EOF
  chmod +x "$FIXTURE_ROOT/bin/uname" "$FIXTURE_ROOT/bin/curl"
  export PATH="$FIXTURE_ROOT/bin:${QDI_SYSTEM_PATH:-/usr/bin:/bin}"
  write_sums ai-agent-telemetry-linux-amd64
}

fixture_sha256_of() {
  if command -v sha256sum >/dev/null 2>&1 && output=$(sha256sum "$1" 2>/dev/null); then
    printf '%s\n' "$output" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1 && output=$(shasum -a 256 "$1" 2>/dev/null); then
    printf '%s\n' "$output" | awk '{print $1}'
  else
    fail 'fixture SHA-256 generation requires sha256sum or shasum'
  fi
}

write_sums() (
  asset=$1
  digest=$(fixture_sha256_of "$FIXTURE_ROOT/asset")
  printf '%s  %s\n' "$digest" "$asset" > "$FIXTURE_ROOT/SHA256SUMS"
)

teardown_fixture() {
  rm -rf "$FIXTURE_ROOT"
}

run_installer() {
  set +e
  RUN_OUTPUT=$(sh "$INSTALLER" "$@" 2>&1)
  RUN_CODE=$?
  set -e
}

run_installer_path() {
  installer=$1
  shift
  set +e
  RUN_OUTPUT=$(sh "$installer" "$@" 2>&1)
  RUN_CODE=$?
  set -e
}

run_without_terminal() {
  python3 - "$@" <<'PY'
import subprocess
import sys

result = subprocess.run(
    ["sh", *sys.argv[1:]],
    stdin=subprocess.DEVNULL,
    stdout=subprocess.PIPE,
    stderr=subprocess.STDOUT,
    start_new_session=True,
    check=False,
)
sys.stdout.buffer.write(result.stdout)
sys.exit(result.returncode)
PY
}

assert_temp_clean() {
  if find "$TMPDIR" -mindepth 1 -print -quit | grep . >/dev/null 2>&1; then
    find "$TMPDIR" -mindepth 1 -maxdepth 2 -print >&2
    fail "private temporary directory was not removed"
  fi
}

test_asset_selection() {
  cases='Linux x86_64 ai-agent-telemetry-linux-amd64
Linux aarch64 ai-agent-telemetry-linux-arm64
Darwin x86_64 ai-agent-telemetry-darwin-amd64
Darwin arm64 ai-agent-telemetry-darwin-arm64'
  printf '%s\n' "$cases" | while IFS=' ' read -r os arch asset; do
    setup_fixture
    QDI_OS=$os QDI_ARCH=$arch
    export QDI_OS QDI_ARCH
    write_sums "$asset"
    run_installer update --components telemetry
    [ "$RUN_CODE" -eq 0 ] || fail "$os/$arch failed: $RUN_OUTPUT"
    grep -Fx "https://release.example.test/releases/latest/download/$asset" "$QDI_TEST_LOG" >/dev/null ||
      fail "wrong asset URL for $os/$arch"
    grep -Fx 'https://release.example.test/releases/latest/download/SHA256SUMS' "$QDI_TEST_LOG" >/dev/null ||
      fail "missing checksum URL for $os/$arch"
    teardown_fixture
  done
}

test_versioned_and_latest_urls() {
  setup_fixture
  run_installer
  [ "$RUN_CODE" -eq 0 ] || fail "latest run failed: $RUN_OUTPUT"
  grep -Fx 'https://release.example.test/releases/latest/download/ai-agent-telemetry-linux-amd64' "$QDI_TEST_LOG" >/dev/null ||
    fail "latest asset URL missing"
  : > "$QDI_TEST_LOG"
  AI_AGENT_TELEMETRY_INSTALL_VERSION=v1.2.3
  export AI_AGENT_TELEMETRY_INSTALL_VERSION
  run_installer install
  [ "$RUN_CODE" -eq 0 ] || fail "versioned run failed: $RUN_OUTPUT"
  grep -Fx 'https://release.example.test/releases/download/v1.2.3/ai-agent-telemetry-linux-amd64' "$QDI_TEST_LOG" >/dev/null ||
    fail "versioned asset URL missing"
  grep -Fx 'https://release.example.test/releases/download/v1.2.3/SHA256SUMS' "$QDI_TEST_LOG" >/dev/null ||
    fail "versioned checksum URL missing"
  teardown_fixture
}

test_staged_version_override_precedence() {
  setup_fixture
  staged=$FIXTURE_ROOT/install.sh
  sed 's|^DEFAULT_BINARY_VERSION=.*|DEFAULT_BINARY_VERSION="v4.5.6"|' "$INSTALLER" > "$staged"
  grep -Fx 'DEFAULT_BINARY_VERSION="v4.5.6"' "$staged" >/dev/null ||
    fail 'staged installer did not contain the release default version'

  run_installer_path "$staged" install
  [ "$RUN_CODE" -eq 0 ] || fail "staged default run failed: $RUN_OUTPUT"
  grep -Fx 'https://release.example.test/releases/download/v4.5.6/ai-agent-telemetry-linux-amd64' "$QDI_TEST_LOG" >/dev/null ||
    fail 'staged default asset URL missing'

  : > "$QDI_TEST_LOG"
  AI_AGENT_TELEMETRY_INSTALL_VERSION=v7.8.9
  export AI_AGENT_TELEMETRY_INSTALL_VERSION
  run_installer_path "$staged" install
  [ "$RUN_CODE" -eq 0 ] || fail "staged override run failed: $RUN_OUTPUT"
  grep -Fx 'https://release.example.test/releases/download/v7.8.9/ai-agent-telemetry-linux-amd64' "$QDI_TEST_LOG" >/dev/null ||
    fail 'staged override asset URL missing'
  teardown_fixture
}

test_command_defaulting_and_exact_argv() {
  setup_fixture
  run_installer
  [ "$RUN_CODE" -eq 0 ] || fail "default run failed: $RUN_OUTPUT"
  [ "$(cat "$QDI_EXEC_LOG")" = install ] || fail "no arguments did not default to install"
  run_installer --components telemetry --non-interactive
  expected='install
--components
telemetry
--non-interactive'
  [ "$(cat "$QDI_EXEC_LOG")" = "$expected" ] || fail "option-first argv changed"
  run_installer update --components 'telemetry,apm' --non-interactive
  expected='update
--components
telemetry,apm
--non-interactive'
  [ "$(cat "$QDI_EXEC_LOG")" = "$expected" ] || fail "explicit update argv changed"
  run_installer uninstall --purge
  expected='uninstall
--purge'
  [ "$(cat "$QDI_EXEC_LOG")" = "$expected" ] || fail "explicit uninstall argv changed"
  teardown_fixture
}

test_checksum_failures_do_not_execute() {
  setup_fixture
  printf 'deadbeef  ai-agent-telemetry-linux-amd64\n' > "$FIXTURE_ROOT/SHA256SUMS"
  run_installer install
  [ "$RUN_CODE" -ne 0 ] || fail "checksum mismatch succeeded"
  assert_contains "$RUN_OUTPUT" 'checksum mismatch'
  [ ! -e "$QDI_EXEC_LOG" ] || fail "binary executed after checksum mismatch"
  assert_temp_clean
  printf 'deadbeef  another-asset\n' > "$FIXTURE_ROOT/SHA256SUMS"
  run_installer install
  [ "$RUN_CODE" -ne 0 ] || fail "missing checksum entry succeeded"
  assert_contains "$RUN_OUTPUT" 'no checksum entry'
  [ ! -e "$QDI_EXEC_LOG" ] || fail "binary executed without checksum entry"
  assert_temp_clean
  teardown_fixture
}

test_exit_status_and_cleanup() {
  setup_fixture
  QDI_BINARY_EXIT=37
  export QDI_BINARY_EXIT
  run_installer update
  [ "$RUN_CODE" -eq 37 ] || fail "binary exit 37 became $RUN_CODE: $RUN_OUTPUT"
  assert_temp_clean
  QDI_BINARY_EXIT=0
  QDI_DOWNLOAD_FAIL=1
  export QDI_BINARY_EXIT QDI_DOWNLOAD_FAIL
  run_installer update
  [ "$RUN_CODE" -ne 0 ] || fail "failed download succeeded"
  assert_not_contains "$RUN_OUTPUT" "$QDI_RESPONSE_BODY"
  assert_temp_clean
  teardown_fixture
}

test_no_secret_output() {
  setup_fixture
  AI_AGENT_TELEMETRY_TOKEN='transport-secret-value'
  export AI_AGENT_TELEMETRY_TOKEN
  run_installer --non-interactive
  [ "$RUN_CODE" -eq 0 ] || fail "secret-output fixture failed: $RUN_OUTPUT"
  assert_not_contains "$RUN_OUTPUT" "$AI_AGENT_TELEMETRY_TOKEN"
  teardown_fixture
}

test_signal_cleanup() {
  setup_fixture
  QDI_SLEEP_SECONDS=2
  export QDI_SLEEP_SECONDS
  sh "$INSTALLER" update > "$FIXTURE_ROOT/signal.out" 2>&1 &
  installer_pid=$!
  attempts=0
  while [ ! -e "$FIXTURE_ROOT/child-ready" ] && [ "$attempts" -lt 100 ]; do
    sleep 0.02
    attempts=$((attempts + 1))
  done
  [ -e "$FIXTURE_ROOT/child-ready" ] || fail "signal fixture did not start child"
  kill -TERM "$installer_pid"
  set +e
  wait "$installer_pid"
  code=$?
  set -e
  [ "$code" -ne 0 ] || fail "terminated bootstrap returned success"
  assert_temp_clean
  teardown_fixture
}

test_pty_prompt_reaches_controlling_terminal() {
  setup_fixture
  QDI_READ_INPUT=1
  QDI_BOOTSTRAP_SOURCE=$INSTALLER
  export QDI_READ_INPUT QDI_BOOTSTRAP_SOURCE
  command="env PATH='$PATH' HOME='$HOME' TMPDIR='$TMPDIR' FIXTURE_ROOT='$FIXTURE_ROOT' QDI_TEST_LOG='$QDI_TEST_LOG' QDI_EXEC_LOG='$QDI_EXEC_LOG' QDI_INPUT_LOG='$QDI_INPUT_LOG' QDI_READ_INPUT=1 QDI_BOOTSTRAP_SOURCE='$INSTALLER' AI_AGENT_TELEMETRY_INSTALL_BASE_URL='$AI_AGENT_TELEMETRY_INSTALL_BASE_URL' sh -c 'curl https://bootstrap.example.test/install.sh | sh -s -- install'"
  set +e
  pty_output=$(python3 - "$command" <<'PY'
import errno
import os
import pty
import select
import sys
import time

command = sys.argv[1]
prompt = b"Collector endpoint: "
answer = b"https://collector.example.test/v1/logs\n"
deadline = time.monotonic() + 15
child, terminal = pty.fork()
if child == 0:
    os.execvp("sh", ["sh", "-c", command])

output = bytearray()
answered = False
timed_out = False
while True:
    remaining = deadline - time.monotonic()
    if remaining <= 0:
        timed_out = True
        os.kill(child, 9)
        break
    readable, _, _ = select.select([terminal], [], [], remaining)
    if not readable:
        timed_out = True
        os.kill(child, 9)
        break
    try:
        chunk = os.read(terminal, 4096)
    except OSError as error:
        if error.errno == errno.EIO:
            break
        raise
    if not chunk:
        break
    output.extend(chunk)
    if not answered and prompt in output:
        os.write(terminal, answer)
        answered = True

sys.stdout.buffer.write(output)
_, status = os.waitpid(child, 0)
if timed_out or not answered:
    sys.exit(65)
if os.WIFEXITED(status):
    sys.exit(os.WEXITSTATUS(status))
sys.exit(128 + os.WTERMSIG(status))
PY
  )
  code=$?
  set -e
  [ "$code" -eq 0 ] || fail "PTY curl | sh failed ($code): $pty_output"
  [ "$(cat "$QDI_INPUT_LOG")" = 'https://collector.example.test/v1/logs' ] || fail "prompt did not read from /dev/tty"
  [ "$(cat "$QDI_EXEC_LOG")" = install ] || fail "PTY bootstrap did not execute the requested install"
  assert_temp_clean
  teardown_fixture
}

test_no_terminal_and_noninteractive_behavior() {
  setup_fixture
  QDI_READ_INPUT=1
  export QDI_READ_INPUT
  set +e
  output=$(run_without_terminal "$INSTALLER" install)
  code=$?
  set -e
  [ "$code" -eq 64 ] || fail "no-terminal required input returned $code: $output"
  assert_temp_clean
  QDI_READ_INPUT=0
  export QDI_READ_INPUT
  set +e
  output=$(run_without_terminal "$INSTALLER" --non-interactive)
  code=$?
  set -e
  [ "$code" -eq 0 ] || fail "no-terminal noninteractive run returned $code: $output"
  assert_temp_clean
  teardown_fixture
}

test_asset_selection
test_versioned_and_latest_urls
test_staged_version_override_precedence
test_command_defaulting_and_exact_argv
test_checksum_failures_do_not_execute
test_exit_status_and_cleanup
test_no_secret_output
test_signal_cleanup
test_pty_prompt_reaches_controlling_terminal
test_no_terminal_and_noninteractive_behavior
printf 'PASS: thin POSIX bootstrap transport tests\n'
