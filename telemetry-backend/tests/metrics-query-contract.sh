#!/bin/sh
set -eu

: "${TEST_BASE_URL:?set TEST_BASE_URL}"
: "${TEST_CA_CERT:?set TEST_CA_CERT}"
: "${TEST_DASHBOARD_USER:?set TEST_DASHBOARD_USER}"
: "${TEST_DASHBOARD_PASSWORD:?set TEST_DASHBOARD_PASSWORD}"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

query_metric() {
  query=$1
  curl --fail --silent --show-error --cacert "$TEST_CA_CERT" \
    --user "$TEST_DASHBOARD_USER:$TEST_DASHBOARD_PASSWORD" \
    --get --data-urlencode "query=$query" \
    "$TEST_BASE_URL/prometheus/api/v1/query"
}

assert_metric_value() {
  name=$1
  expected=$2
  query=$3
  actual=$(query_metric "$query" | jq -r '.data.result[0].value[1] // empty')
  [ "$actual" = "$expected" ] || fail "$name=$actual, want $expected"
}

assert_metric_value codex_sessions 2 \
  'codex_thread_started_total{service_name="codex_cli_rs",session_source="cli",model="fixture-codex"}'
assert_metric_value codex_tools 3 \
  'codex_tool_call_total{service_name="codex_cli_rs",tool="exec_command",success="true"}'
assert_metric_value codex_tokens 1200 \
  'codex_turn_token_usage_sum{service_name="codex_cli_rs",token_type="total",model="fixture-codex"}'
assert_metric_value claude_sessions 2 \
  'claude_code_session_count_total{service_name="claude-code",start_type="fresh"}'
assert_metric_value claude_tokens 900 \
  'claude_code_token_usage_tokens_total{service_name="claude-code",type="input",model="fixture-claude"}'
assert_metric_value cline_fixture 1 \
  'cline_fixture_task_count_total{service_name="cline-fixture",fixture="true"}'

query_metric '{service_name="cline-fixture",agent_harness=~".+"}' |
  jq -e '.data.result | length == 0' >/dev/null ||
  fail 'the Cline fixture must not gain a synthetic harness label'

printf 'PASS: native metrics query contract\n'
