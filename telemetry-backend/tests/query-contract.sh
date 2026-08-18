#!/bin/sh
set -eu

: "${TEST_BASE_URL:?set TEST_BASE_URL}"
: "${TEST_CA_CERT:?set TEST_CA_CERT}"
: "${TEST_DASHBOARD_USER:?set TEST_DASHBOARD_USER}"
: "${TEST_DASHBOARD_PASSWORD:?set TEST_DASHBOARD_PASSWORD}"
: "${TEST_TIME_FROM:?set TEST_TIME_FROM}"
: "${TEST_TIME_TO:?set TEST_TIME_TO}"

selector='{service.name="ai-agent-telemetry"}'

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

query_value() {
  query=$1
  field=$2
  curl --fail --silent --show-error --cacert "$TEST_CA_CERT" \
    --user "$TEST_DASHBOARD_USER:$TEST_DASHBOARD_PASSWORD" \
    --data-urlencode "query=$query" \
    --data-urlencode "start=$TEST_TIME_FROM" \
    --data-urlencode "end=$TEST_TIME_TO" \
    "$TEST_BASE_URL/select/logsql/query" |
    jq -sr --arg field "$field" 'if length == 1 then .[0][$field] // empty else empty end'
}

assert_query() {
  name=$1
  expected=$2
  query=$3
  actual=$(query_value "$query" "$name")
  [ "$actual" = "$expected" ] || fail "$name=$actual, want $expected"
}

assert_query active_installs 4 "$selector | stats count_uniq(machine.id) active_installs"
assert_query active_repositories 4 "$selector | stats count_uniq(repo.remote) active_repositories"
assert_query distinct_events 8 "$selector | stats count_uniq(event.id) distinct_events"
assert_query raw_id_events 9 "$selector | stats count(event.id) raw_id_events"
assert_query skill_events 3 "$selector _msg:=\"skill_executed\" | stats count_uniq(event.id) skill_events"
assert_query mcp_events 4 "$selector _msg:=\"mcp_tool_executed\" | stats count_uniq(event.id) mcp_events"
assert_query command_events 1 "$selector _msg:=\"command_invoked\" | stats count_uniq(event.id) command_events"
assert_query mcp_succeeded 2 "$selector _msg:=\"mcp_tool_executed\" | stats count_uniq(event.id) if (mcp.outcome:=\"succeeded\") mcp_succeeded"
assert_query mcp_failed 1 "$selector _msg:=\"mcp_tool_executed\" | stats count_uniq(event.id) if (mcp.outcome:=\"failed\") mcp_failed"
assert_query mcp_unknown 1 "$selector _msg:=\"mcp_tool_executed\" | stats count_uniq(event.id) if (mcp.outcome:=\"unknown\") mcp_unknown"
assert_query mcp_failure_rate 0.3333333333333333 "$selector _msg:=\"mcp_tool_executed\" | stats count_uniq(event.id) if (mcp.outcome:=\"failed\") failed, count_uniq(event.id) if (mcp.outcome:=\"succeeded\") succeeded | math failed / (failed + succeeded) as mcp_failure_rate"
assert_query mcp_duration_records 3 "$selector _msg:=\"mcp_tool_executed\" | stats count(mcp.duration_ms) mcp_duration_records"

dashboard_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/../grafana/dashboards" && pwd)
queries=$(mktemp)
response=$(mktemp)
trap 'rm -f "$queries" "$response"' EXIT HUP INT TERM
for dashboard in "$dashboard_dir"/*.json; do
  jq -c '[.panels[].targets[]?
    | select(.datasource.uid == "victorialogs")
    | .expr] | unique[]' "$dashboard" >>"$queries"
done
sort -u "$queries" -o "$queries"
while IFS= read -r encoded_query; do
  query=$(printf '%s\n' "$encoded_query" | jq -r .)
  query=$(printf '%s\n' "$query" | sed 's/\${[^}]*:regex}/.*/g')
  status=$(curl --silent --show-error --cacert "$TEST_CA_CERT" \
    --user "$TEST_DASHBOARD_USER:$TEST_DASHBOARD_PASSWORD" \
    --output "$response" --write-out '%{http_code}' \
    --data-urlencode "query=$query" \
    --data-urlencode "start=$TEST_TIME_FROM" \
    --data-urlencode "end=$TEST_TIME_TO" \
    "$TEST_BASE_URL/select/logsql/query")
  [ "$status" = 200 ] || fail "dashboard query returned HTTP $status: $(cat "$response")"
done <"$queries"

printf 'PASS: LogsQL query contract\n'
