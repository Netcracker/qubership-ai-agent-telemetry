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
  request_timeout=${2:-5}
  connect_timeout=2
  [ "$request_timeout" -ge "$connect_timeout" ] || connect_timeout=$request_timeout
  curl --fail --silent --show-error --connect-timeout "$connect_timeout" --max-time "$request_timeout" \
    --cacert "$TEST_CA_CERT" \
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

is_positive_integer() {
  case "$1" in
    ''|*[!0-9]*|0*) return 1 ;;
    *) return 0 ;;
  esac
}

visibility_deadline_seconds=${TEST_VM_METRICS_DEADLINE_SECONDS:-30}
visibility_curl_max_time=${TEST_VM_METRICS_CURL_MAX_TIME:-5}
is_positive_integer "$visibility_deadline_seconds" &&
  is_positive_integer "$visibility_curl_max_time" ||
  fail 'VictoriaMetrics query deadlines must be positive integer seconds'
visibility_deadline=$(($(date +%s) + visibility_deadline_seconds))

assert_metric_visible() {
  metric=$1
  response=
  result_count=
  now=
  remaining=
  request_timeout=
  while :; do
    now=$(date +%s)
    remaining=$((visibility_deadline - now))
    [ "$remaining" -gt 0 ] ||
      fail "$metric did not become queryable within the shared ${visibility_deadline_seconds}-second deadline"
    request_timeout=$visibility_curl_max_time
    [ "$request_timeout" -le "$remaining" ] || request_timeout=$remaining
    if response=$(query_metric "$metric" "$request_timeout"); then
      if ! result_count=$(printf '%s' "$response" | jq -er '
        if .status == "success" and (.data | type) == "object" and (.data.result | type) == "array"
        then .data.result | length
        else error("invalid Prometheus response")
        end
      ' 2>/dev/null); then
        fail "$metric returned a malformed successful response"
      fi
      [ "$result_count" -eq 0 ] || return 0
    fi
    now=$(date +%s)
    [ "$now" -lt "$visibility_deadline" ] ||
      fail "$metric did not become queryable within the shared ${visibility_deadline_seconds}-second deadline"
    sleep 1
  done
}

for metric in \
  vm_hourly_series_limit_current_series \
  vm_daily_series_limit_current_series \
  vm_free_disk_space_bytes \
  vm_data_size_bytes; do
  assert_metric_visible "$metric"
done

[ "${TEST_METRICS_VISIBILITY_ONLY:-}" != 1 ] || exit 0

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

for query in \
  '{session_id=~".+"}' \
  '{user_email=~".+"}' \
  '{user_account_uuid=~".+"}' \
  '{organization_id=~".+"}'; do
  query_metric "$query" | jq -e '.data.result | length == 0' >/dev/null ||
    fail "sensitive native metric attribute reached storage: $query"
done

assert_metric_value codex_conversation_turns 4 \
  'codex_conversation_turn_count_total{service_name="codex_cli_rs"}'
assert_metric_value codex_mcp_calls 3 \
  'codex_mcp_call_total{service_name="codex_cli_rs",server="fixture-mcp",status="ok"}'
assert_metric_value codex_skill_injections 2 \
  'codex_skill_injected_total{service_name="codex_cli_rs",skill="fixture-skill",invoke_type="explicit",status="ok"}'

for metric in \
  codex_turn_e2e_duration_ms_milliseconds_bucket \
  codex_tool_call_duration_ms_milliseconds_bucket \
  codex_responses_api_engine_service_ttft_duration_ms_milliseconds_bucket \
  codex_responses_api_engine_service_tbt_duration_ms_milliseconds_bucket \
  codex_responses_api_inference_time_duration_ms_milliseconds_bucket; do
  query_metric "$metric{service_name=\"codex_cli_rs\",le=\"+Inf\"}" |
    jq -e '.data.result | length == 1' >/dev/null ||
    fail "$metric fixture must expose an explicit le bucket"
  query_metric "histogram_quantile(0.95, sum by (le) (rate($metric{service_name=\"codex_cli_rs\"}[1h])))" |
    jq -e '(.data.result | length == 1) and (.data.result[0].value[1] | tonumber) > 0' >/dev/null ||
    fail "$metric must support the dashboard histogram_quantile query"
done

assert_metric_value claude_version 1 \
  'count(group by (service_version) (claude_code_session_count_total{service_name="claude-code",service_version="fixture-claude"}))'

assert_metric_value claude_session_increase 2 \
  'sum(increase(claude_code_session_count_total{service_name="claude-code",start_type="fresh"}[1h]))'
assert_metric_value claude_token_increase 900 \
  'sum(increase(claude_code_token_usage_tokens_total{service_name="claude-code",type="input",model="fixture-claude"}[1h]))'

versions_query='label_set(
  group by (service_version) (
    max_over_time(codex_tool_call_total{service_name="codex_cli_rs"}[1h])
  ),
  "harness", "Codex"
)
or
label_set(
  group by (service_version) (
    max_over_time(claude_code_session_count_total{service_name=~"claude-code|claude-code-desktop"}[1h])
  ),
  "harness", "Claude"
)'
query_metric "$versions_query" | jq -e '
  [.data.result[].metric | {harness, service_version}] | sort_by(.harness) ==
  [
    {"harness": "Claude", "service_version": "fixture-claude"},
    {"harness": "Codex", "service_version": "fixture"}
  ]
' >/dev/null || fail 'client versions query must return Codex and Claude service versions'

query_metric 'codex_tool_call_total{service_name="codex_cli_rs",success="false"}' |
  jq -e '.data.result | length == 0' >/dev/null ||
  fail 'the Codex fixture must not include failed tool calls'

successful_only_ratio='
  (
    sum(increase(codex_tool_call_total{
      service_name="codex_cli_rs",
      success="false"
    }[1h]))
    or
    (0 * sum(increase(codex_tool_call_total{
      service_name="codex_cli_rs"
    }[1h])))
  )
  /
  sum(increase(codex_tool_call_total{
    service_name="codex_cli_rs"
  }[1h]))
'
no_calls_ratio='
  (
    sum(increase(codex_tool_call_total{
      service_name="codex-no-calls",
      success="false"
    }[1h]))
    or
    (0 * sum(increase(codex_tool_call_total{
      service_name="codex-no-calls"
    }[1h])))
  )
  /
  sum(increase(codex_tool_call_total{
    service_name="codex-no-calls"
  }[1h]))
'

zero_ratio=$(query_metric "$successful_only_ratio" |
  jq -r '.data.result[0].value[1] // empty')
[ "$zero_ratio" = 0 ] || fail "successful-only tool calls must produce a zero failure ratio"

query_metric "$no_calls_ratio" |
  jq -e '.data.result | length == 0' >/dev/null ||
  fail 'missing tool-call metrics must produce no failure-ratio series'

query_metric '{service_name="cline-fixture",agent_harness=~".+"}' |
  jq -e '.data.result | length == 0' >/dev/null ||
  fail 'the Cline fixture must not gain a synthetic harness label'

printf 'PASS: native metrics query contract\n'
