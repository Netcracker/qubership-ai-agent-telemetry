#!/bin/sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)

if [ "${FIXTURE_STACK_READY:-}" != 1 ]; then
  TEST_WITH_GRAFANA=1 exec sh "$script_dir/with-fixture-stack.sh" \
    env FIXTURE_STACK_READY=1 sh "$0"
fi

: "${TEST_COMPOSE_PROJECT:?set TEST_COMPOSE_PROJECT}"
: "${TEST_ENV_FILE:?set TEST_ENV_FILE}"
: "${TEST_COMPOSE_FILE:?set TEST_COMPOSE_FILE}"
: "${TEST_INGEST_TOKEN:?set TEST_INGEST_TOKEN}"
: "${TEST_RENDERED_FIXTURE:?set TEST_RENDERED_FIXTURE}"

compose() {
  docker compose -p "$TEST_COMPOSE_PROJECT" --env-file "$TEST_ENV_FILE" -f "$TEST_COMPOSE_FILE" "$@"
}

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

status() {
  curl --silent --show-error --output /dev/null --write-out '%{http_code}' --cacert "$TEST_CA_CERT" "$@"
}

location() {
  curl --silent --show-error --head --cacert "$TEST_CA_CERT" "$@" |
    awk 'BEGIN { IGNORECASE=1 } /^location:/ { sub(/\r$/, "", $2); print $2 }'
}

compose ps --status running grafana | grep -q grafana || fail 'Grafana was not started by the fixture stack'

attempt=0
while :; do
  grafana_status=$(status "$TEST_BASE_URL/grafana/" || true)
  [ "$grafana_status" = 302 ] && break
  attempt=$((attempt + 1))
  [ "$attempt" -lt 120 ] || fail "Grafana did not become ready (HTTP $grafana_status)"
  sleep 1
done

[ "$(status "$TEST_BASE_URL/")" = 308 ] || fail 'root path must redirect to Grafana'
[ "$(location "$TEST_BASE_URL/")" = /grafana/ ] || fail 'root path must redirect to /grafana/'
[ "$(status "$TEST_BASE_URL/grafana")" = 308 ] || fail '/grafana must include a trailing slash redirect'
[ "$(location "$TEST_BASE_URL/grafana")" = /grafana/ ] || fail '/grafana must redirect to /grafana/'
[ "$(status "$TEST_BASE_URL/grafana/")" = 302 ] || fail 'Grafana must redirect unauthenticated users to login'
[ "$(status "$TEST_BASE_URL/grafana/login")" = 401 ] || fail 'Grafana login must require Basic Auth'
[ "$(status --request POST --header 'Content-Type: application/json' --data '{}' \
  "$TEST_BASE_URL/grafana/login")" = 401 ] || fail 'Grafana native login POST must require Basic Auth'
[ "$(status "$TEST_BASE_URL/select/vmui/")" = 401 ] || fail 'VMUI must require Basic Auth'
[ "$(status --request POST "$TEST_BASE_URL/v1/logs")" = 401 ] || fail 'ingest must require a bearer token'
[ "$(status --header "Authorization: Bearer $TEST_INGEST_TOKEN" "$TEST_BASE_URL/unknown")" = 404 ] ||
  fail 'unknown routes must return 404'

challenge_headers=$(mktemp)
frontend_status=$(curl --silent --show-error --dump-header "$challenge_headers" --output /dev/null \
  --write-out '%{http_code}' --cacert "$TEST_CA_CERT" --request POST --header 'Content-Type: application/json' \
  --data '{}' "$TEST_BASE_URL/grafana/api/frontend-metrics")
[ "$frontend_status" = 401 ] || fail 'unauthenticated Grafana API request must be rejected'
if grep -qi '^www-authenticate: Basic' "$challenge_headers"; then
  fail 'Grafana subrequests must not trigger a Basic Auth challenge'
fi

viewer_cookie=$(mktemp)
attempt=0
while :; do
  grafana_status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
    --cacert "$TEST_CA_CERT" --user "$TEST_DASHBOARD_USER:$TEST_DASHBOARD_PASSWORD" \
    --cookie-jar "$viewer_cookie" "$TEST_BASE_URL/grafana/login" || true)
  [ "$grafana_status" = 302 ] && break
  attempt=$((attempt + 1))
  [ "$attempt" -lt 120 ] || fail "Grafana auth proxy did not become ready (HTTP $grafana_status)"
  sleep 1
done

[ "$(status --cookie "$viewer_cookie" "$TEST_BASE_URL/grafana/")" = 200 ] ||
  fail 'Grafana viewer cookie did not authenticate the dashboard'
[ "$(status --cookie "$viewer_cookie" \
  "$TEST_BASE_URL/grafana/public/build/img/icons/unicons/external-link-alt.svg")" = 200 ] ||
  fail 'Grafana static asset did not accept the viewer cookie'
[ "$(status --request POST --cookie "$viewer_cookie" --header 'Content-Type: application/json' --data '{}' \
  "$TEST_BASE_URL/grafana/api/frontend-metrics")" = 200 ] ||
  fail 'Grafana frontend metrics did not accept the viewer cookie'
viewer_user=$(curl --fail --silent --show-error --cacert "$TEST_CA_CERT" --cookie "$viewer_cookie" \
  "$TEST_BASE_URL/grafana/api/user")
[ "$(printf '%s' "$viewer_user" | jq -r '.isGrafanaAdmin')" = false ] ||
  fail 'Grafana auth proxy user must not have administrator access'
viewer_orgs=$(curl --fail --silent --show-error --cacert "$TEST_CA_CERT" --cookie "$viewer_cookie" \
  "$TEST_BASE_URL/grafana/api/user/orgs")
printf '%s' "$viewer_orgs" | jq -e 'any(.[]; .role == "Viewer")' >/dev/null ||
  fail 'Grafana auth proxy user does not have the Viewer organization role'

[ "$(status --user "$TEST_DASHBOARD_USER:$TEST_DASHBOARD_PASSWORD" "$TEST_BASE_URL/select/vmui/")" = 200 ] ||
  fail 'authenticated VMUI request failed'
[ "$(status --request POST "$TEST_BASE_URL/v1/metrics")" = 401 ] ||
  fail 'metrics ingest must require a bearer token'
[ "$(status "$TEST_BASE_URL/prometheus/api/v1/query?query=up")" = 401 ] ||
  fail 'VictoriaMetrics queries must require Basic Auth'
[ "$(status --user "$TEST_DASHBOARD_USER:$TEST_DASHBOARD_PASSWORD" --request POST \
  "$TEST_BASE_URL/prometheus/api/v1/write")" = 404 ] ||
  fail 'dashboard credentials must not authorize VictoriaMetrics writes'

datasource=$(curl --fail --silent --show-error --cacert "$TEST_CA_CERT" \
  --cookie "$viewer_cookie" \
  "$TEST_BASE_URL/grafana/api/datasources/uid/victorialogs")
[ "$(printf '%s' "$datasource" | jq -r '.type')" = victoriametrics-logs-datasource ] ||
  fail 'VictoriaLogs datasource was not provisioned'
datasource_health=$(curl --fail --silent --show-error --cacert "$TEST_CA_CERT" \
  --cookie "$viewer_cookie" \
  "$TEST_BASE_URL/grafana/api/datasources/uid/victorialogs/health")
[ "$(printf '%s' "$datasource_health" | jq -r '.status')" = OK ] ||
  fail 'VictoriaLogs datasource health check failed'

[ "$(status --cookie "$viewer_cookie" \
  "$TEST_BASE_URL/grafana/api/datasources/uid/victoriametrics")" = 200 ] ||
  fail 'VictoriaMetrics datasource was not provisioned'
metrics_datasource=$(curl --fail --silent --show-error --cacert "$TEST_CA_CERT" \
  --cookie "$viewer_cookie" \
  "$TEST_BASE_URL/grafana/api/datasources/uid/victoriametrics")
[ "$(printf '%s' "$metrics_datasource" | jq -r '.type')" = prometheus ] ||
  fail 'VictoriaMetrics datasource must use the Prometheus type'
metrics_datasource_health=$(curl --fail --silent --show-error --cacert "$TEST_CA_CERT" \
  --cookie "$viewer_cookie" \
  "$TEST_BASE_URL/grafana/api/datasources/uid/victoriametrics/health")
[ "$(printf '%s' "$metrics_datasource_health" | jq -r '.status')" = OK ] ||
  fail 'VictoriaMetrics datasource health check failed'

for uid in \
  ai-agent-health \
  ai-agent-telemetry-adoption \
  native-agent-metrics-overview \
  codex-native-metrics; do
  curl --fail --silent --show-error --cacert "$TEST_CA_CERT" \
    --cookie "$viewer_cookie" \
    "$TEST_BASE_URL/grafana/api/dashboards/uid/$uid" >/dev/null || fail "dashboard $uid was not provisioned"
done

cookie_jar=$(mktemp)
grafana_query=$(mktemp)
grafana_response=$(mktemp)
trap 'rm -f "$challenge_headers" "$viewer_cookie" "$cookie_jar" "$grafana_query" "$grafana_response"' \
  EXIT HUP INT TERM
[ "$(status --user "$TEST_DASHBOARD_USER:$TEST_DASHBOARD_PASSWORD" \
  "$TEST_BASE_URL/grafana/login?disableAutoLogin=true")" = 200 ] ||
  fail 'Grafana administrator login page is unavailable'
login_status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
  --cacert "$TEST_CA_CERT" --user "$TEST_DASHBOARD_USER:$TEST_DASHBOARD_PASSWORD" \
  --cookie-jar "$cookie_jar" --header 'Content-Type: application/json' \
  --data '{"user":"admin","password":"fixture-admin-password"}' "$TEST_BASE_URL/grafana/login")
[ "$login_status" = 200 ] || fail "Grafana administrator login failed (HTTP $login_status)"
admin_user=$(curl --fail --silent --show-error --cacert "$TEST_CA_CERT" --cookie "$cookie_jar" \
  "$TEST_BASE_URL/grafana/api/user")
[ "$(printf '%s' "$admin_user" | jq -r '.isGrafanaAdmin')" = true ] ||
  fail 'Grafana administrator session does not have administrator access'

time_from_ms=$((TEST_TIME_FROM * 1000))
# Instant Prometheus queries evaluate at `to`. Keep it close to ingestion so fixture samples are in the lookback window.
time_to_ms=$(($(date -u +%s) * 1000))
jq -n --arg from "$time_from_ms" --arg to "$time_to_ms" '{
  from: $from,
  to: $to,
  queries: [
    {
      datasource: {type: "victoriametrics-logs-datasource", uid: "victorialogs"},
      expr: "{service.name=\"ai-agent-telemetry\"} | stats count_uniq(machine.id) active_installs",
      queryType: "stats", refId: "S", maxDataPoints: 1000, intervalMs: 60000
    },
    {
      datasource: {type: "victoriametrics-logs-datasource", uid: "victorialogs"},
      expr: "{service.name=\"ai-agent-telemetry\"} _msg:=\"skill_executed\" | stats by (skill.name) count_uniq(event.id) events, count_uniq(machine.id) installs, count_uniq(repo.remote) repositories | sort by (events) desc | limit 20",
      queryType: "stats", refId: "T", maxDataPoints: 1000, intervalMs: 60000
    },
    {
      datasource: {type: "victoriametrics-logs-datasource", uid: "victorialogs"},
      expr: "{service.name=\"ai-agent-telemetry\"} _msg:=\"skill_executed\" | stats by (skill.name) count_uniq(event.id) events",
      queryType: "stats", refId: "P", maxDataPoints: 1000, intervalMs: 60000
    },
    {
      datasource: {type: "victoriametrics-logs-datasource", uid: "victorialogs"},
      expr: "{service.name=\"ai-agent-telemetry\"} _msg:=\"skill_executed\" | stats by (_time:1h) count_uniq(event.id) events",
      queryType: "statsRange", refId: "R", maxDataPoints: 1000, intervalMs: 60000
    },
    {
      datasource: {type: "victoriametrics-logs-datasource", uid: "victorialogs"},
      expr: "{service.name=\"ai-agent-telemetry\"} | stats count() total, count(event.id) with_event_id | math 100 * with_event_id / total as coverage_percent | keep coverage_percent",
      queryType: "stats", refId: "H", maxDataPoints: 1000, intervalMs: 60000
    },
    {
      datasource: {type: "victoriametrics-logs-datasource", uid: "victorialogs"},
      expr: "{service.name=\"ai-agent-telemetry\"} agent:!=\"selftest\" | stats by (agent, os.type) count_uniq(machine.id) active_installs",
      queryType: "stats", refId: "O", maxDataPoints: 1000, intervalMs: 60000
    },
    {
      datasource: {type: "victoriametrics-logs-datasource", uid: "victorialogs"},
      expr: "{service.name=\"ai-agent-telemetry\"} _msg:=\"mcp_tool_executed\" | format if (!mcp.server.name:*) \"Unknown\" as mcp.server.name | stats by (mcp.server.name, mcp.tool.name) count_uniq(event.id) calls",
      queryType: "stats", refId: "M", maxDataPoints: 1000, intervalMs: 60000
    },
    {
      datasource: {type: "prometheus", uid: "victoriametrics"},
      expr: "sum(codex_tool_call_total{service_name=\"codex_cli_rs\"})",
      format: "time_series", instant: true, refId: "CM", maxDataPoints: 1000, intervalMs: 60000
    },
    {
      datasource: {type: "prometheus", uid: "victoriametrics"},
      expr: "sum(claude_code_token_usage_tokens_total{service_name=\"claude-code\",type=\"input\"})",
      format: "time_series", instant: true, refId: "HM", maxDataPoints: 1000, intervalMs: 60000
    }
  ]
}' >"$grafana_query"
curl --fail --silent --show-error --cacert "$TEST_CA_CERT" \
  --cookie "$viewer_cookie" \
  --header 'Content-Type: application/json' --data-binary "@$grafana_query" \
  "$TEST_BASE_URL/grafana/api/ds/query" >"$grafana_response"
jq -e '.results | [.S, .T, .P, .R, .H, .O, .M] | all(.status == 200 and (.frames | length > 0))' \
  "$grafana_response" >/dev/null || fail 'Grafana datasource queries did not return frames'
jq -e '[.results[].frames[].schema.fields[].name] | index("Line") == null' \
  "$grafana_response" >/dev/null || fail 'an aggregate Grafana query returned a raw log frame'
jq -e '.results.S.frames[0]
  | (.schema.fields | any(.name == "Value" and .type == "number"))
    and (.data.values[1][0] == 3)' "$grafana_response" >/dev/null ||
  fail 'the Grafana stat query did not return active_installs=3 as numeric data'
for ref_id in T P; do
  jq -e --arg ref_id "$ref_id" '
    (.results[$ref_id].frames | [.[].schema.fields[]] | any(.type == "number"))
      and (.results[$ref_id].frames | [.[].schema.fields[].labels?]
        | any(.["skill.name"] == "testing"))' "$grafana_response" >/dev/null ||
    fail "the Grafana $ref_id query did not return grouped numeric skill data"
done
jq -e '[.results.T.frames[].schema.fields[].labels?.__name__]
  | contains(["events", "installs", "repositories"])' "$grafana_response" >/dev/null ||
  fail 'the Grafana table query did not return every requested metric'
jq -e '(.results.R.frames | [.[].schema.fields[]]
    | any(.name == "Time" and .type == "time"))
  and (.results.R.frames | [.[].schema.fields[]]
    | any(.name == "Value" and .type == "number"))' "$grafana_response" >/dev/null ||
  fail 'the Grafana range query did not return a numeric time series'
jq -e '.results.H.frames | length == 1 and
  (.[0].schema.fields | any(.name == "Value" and .type == "number" and .labels.__name__ == "coverage_percent"))' \
  "$grafana_response" >/dev/null ||
  fail 'the Grafana coverage query did not return exactly one percentage metric'
jq -e '[.results.O.frames[].schema.fields[].labels?.agent]
  | all(. != "selftest") and contains(["claude", "codex", "cursor"])' \
  "$grafana_response" >/dev/null ||
  fail 'the Grafana harness query did not exclude selftest or return every fixture harness'
jq -e '[.results.M.frames[].schema.fields[].labels?]
  | any(.["mcp.server.name"] == "Unknown" and .["mcp.tool.name"] == "search")' \
  "$grafana_response" >/dev/null ||
  fail 'the Grafana MCP query did not retain a tool event without a server name'
jq -e '.results | [.CM, .HM] | all(
  .status == 200 and
  (.frames | length > 0) and
  ([.frames[].schema.fields[]] | any(.type == "number"))
)' "$grafana_response" >/dev/null ||
  fail 'Grafana Prometheus queries did not return numeric frames'
jq -e '.results.CM.frames | any(
  ([.schema.fields[]] | any(.type == "number")) and
  any(.data.values[]?; any(.[]?; . == 3))
)' "$grafana_response" >/dev/null ||
  fail 'the Grafana Codex metrics query did not return 3 as numeric data'
jq -e '.results.HM.frames | any(
  ([.schema.fields[]] | any(.type == "number")) and
  any(.data.values[]?; any(.[]?; . == 900))
)' "$grafana_response" >/dev/null ||
  fail 'the Grafana Claude metrics query did not return 900 as numeric data'

sh "$script_dir/query-contract.sh"
sh "$script_dir/metrics-query-contract.sh"
sh "$script_dir/dashboard-contract.sh"

compose stop grafana >/dev/null
curl --fail --silent --show-error --cacert "$TEST_CA_CERT" \
  --header "Authorization: Bearer $TEST_INGEST_TOKEN" \
  --header 'Content-Type: application/json' --data-binary "@$TEST_RENDERED_FIXTURE" \
  "$TEST_BASE_URL/v1/logs" >/dev/null || fail 'ingest failed while Grafana was stopped'

published=$(compose ps --format json |
  jq -csr '[.[] | select(any(.Publishers[]?; (.PublishedPort // 0) > 0)) | .Service] | unique')
[ "$published" = '["caddy"]' ] || fail "only Caddy may publish host ports: $published"

printf 'PASS: telemetry backend smoke test\n'
