#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM
bin_dir=$tmp_dir/bin
curl_log=$tmp_dir/curl.log
state_file=$tmp_dir/state
mkdir -p "$bin_dir"
: >"$curl_log"
: >"$state_file"

cat >"$bin_dir/curl" <<'EOF'
#!/bin/sh
set -eu

printf '%s\n' "$*" >>"$TEST_CURL_LOG"
count=$(wc -l <"$TEST_CURL_STATE")
printf 'x\n' >>"$TEST_CURL_STATE"
case "$TEST_CURL_SCENARIO" in
  transient)
    [ "$count" -gt 0 ] || exit 28
    printf '%s\n' '{"status":"success","data":{"result":[{"value":[0,"1"]}]}}'
    ;;
  malformed)
    printf '%s\n' '{"status":"success","data":{}}'
    ;;
  slow-success)
    sleep 1
    printf '%s\n' '{"status":"success","data":{"result":[{"value":[0,"1"]}]}}'
    ;;
  *) exit 2 ;;
esac
EOF
chmod 700 "$bin_dir/curl"

run_visibility() {
  scenario=$1 deadline=$2 max_time=${3:-1}
  : >"$curl_log"
  : >"$state_file"
  PATH="$bin_dir:$PATH" TEST_CURL_LOG="$curl_log" TEST_CURL_STATE="$state_file" \
    TEST_CURL_SCENARIO="$scenario" TEST_METRICS_VISIBILITY_ONLY=1 \
    TEST_VM_METRICS_DEADLINE_SECONDS="$deadline" TEST_VM_METRICS_CURL_MAX_TIME="$max_time" \
    TEST_BASE_URL=https://fixture.invalid TEST_CA_CERT="$tmp_dir/ca.pem" \
    TEST_DASHBOARD_USER=viewer TEST_DASHBOARD_PASSWORD=secret \
    sh "$script_dir/metrics-query-contract.sh"
}

if zero_padded_zero_output=$(run_visibility transient 3 00 2>&1); then
  printf '%s\n' 'FAIL: zero-padded zero timeout was accepted' >&2
  exit 1
fi
case "$zero_padded_zero_output" in
  *'deadlines must be positive integer seconds'*) ;;
  *) printf 'FAIL: zero-padded zero timeout diagnostic is unclear: %s\n' "$zero_padded_zero_output" >&2; exit 1 ;;
esac

if zero_padded_positive_output=$(run_visibility transient 0008 1 2>&1); then
  printf '%s\n' 'FAIL: zero-padded positive timeout was accepted' >&2
  exit 1
fi
case "$zero_padded_positive_output" in
  *'deadlines must be positive integer seconds'*) ;;
  *) printf 'FAIL: zero-padded positive timeout diagnostic is unclear: %s\n' \
    "$zero_padded_positive_output" >&2; exit 1 ;;
esac

run_visibility transient 3
grep -F -- '--connect-timeout' "$curl_log" >/dev/null || {
  printf '%s\n' 'FAIL: VictoriaMetrics queries lack a connection timeout' >&2
  exit 1
}
grep -F -- '--max-time 1' "$curl_log" >/dev/null || {
  printf '%s\n' 'FAIL: VictoriaMetrics queries lack the requested total timeout' >&2
  exit 1
}

if malformed_output=$(run_visibility malformed 3 2>&1); then
  printf '%s\n' 'FAIL: malformed successful response was accepted' >&2
  exit 1
fi
case "$malformed_output" in
  *'malformed successful response'*) ;;
  *) printf 'FAIL: malformed response diagnostic is unclear: %s\n' "$malformed_output" >&2; exit 1 ;;
esac

start=$(date +%s)
if slow_output=$(run_visibility slow-success 2 2>&1); then
  printf '%s\n' 'FAIL: per-metric retries exceeded the shared visibility deadline' >&2
  exit 1
fi
elapsed=$(($(date +%s) - start))
[ "$elapsed" -le 4 ] || {
  printf 'FAIL: shared visibility deadline took %s seconds\n' "$elapsed" >&2
  exit 1
}
[ "$(wc -l <"$curl_log")" -lt 4 ] || {
  printf '%s\n' 'FAIL: all metrics received independent retry windows' >&2
  exit 1
}
case "$slow_output" in
  *'shared 2-second deadline'*) ;;
  *) printf 'FAIL: shared deadline diagnostic is unclear: %s\n' "$slow_output" >&2; exit 1 ;;
esac

printf '%s\n' 'PASS: VictoriaMetrics query retry contract'
