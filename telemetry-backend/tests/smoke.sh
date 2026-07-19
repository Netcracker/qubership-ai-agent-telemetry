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

compose ps --status running grafana | grep -q grafana || fail 'Grafana was not started by the fixture stack'

[ "$(status "$TEST_BASE_URL/grafana/")" = 401 ] || fail 'Grafana must require Basic Auth'
[ "$(status "$TEST_BASE_URL/select/vmui/")" = 401 ] || fail 'VMUI must require Basic Auth'
[ "$(status --request POST "$TEST_BASE_URL/v1/logs")" = 401 ] || fail 'ingest must require a bearer token'
[ "$(status --header "Authorization: Bearer $TEST_INGEST_TOKEN" "$TEST_BASE_URL/unknown")" = 404 ] ||
  fail 'unknown routes must return 404'

attempt=0
while :; do
  grafana_status=$(status --user "$TEST_DASHBOARD_USER:$TEST_DASHBOARD_PASSWORD" "$TEST_BASE_URL/grafana/" || true)
  case $grafana_status in
    200 | 302) break ;;
  esac
  attempt=$((attempt + 1))
  [ "$attempt" -lt 120 ] || fail "Grafana did not become ready (HTTP $grafana_status)"
  sleep 1
done

[ "$(status --user "$TEST_DASHBOARD_USER:$TEST_DASHBOARD_PASSWORD" "$TEST_BASE_URL/select/vmui/")" = 200 ] ||
  fail 'authenticated VMUI request failed'

datasource=$(curl --fail --silent --show-error --cacert "$TEST_CA_CERT" \
  --user "$TEST_DASHBOARD_USER:$TEST_DASHBOARD_PASSWORD" \
  "$TEST_BASE_URL/grafana/api/datasources/uid/victorialogs")
[ "$(printf '%s' "$datasource" | jq -r '.type')" = victoriametrics-logs-datasource ] ||
  fail 'VictoriaLogs datasource was not provisioned'

for uid in ai-agent-executive ai-agent-skills ai-agent-mcp ai-agent-commands ai-agent-health; do
  curl --fail --silent --show-error --cacert "$TEST_CA_CERT" \
    --user "$TEST_DASHBOARD_USER:$TEST_DASHBOARD_PASSWORD" \
    "$TEST_BASE_URL/grafana/api/dashboards/uid/$uid" >/dev/null || fail "dashboard $uid was not provisioned"
done

cookie_jar=$(mktemp)
trap 'rm -f "$cookie_jar"' EXIT HUP INT TERM
login_status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
  --cacert "$TEST_CA_CERT" --user "$TEST_DASHBOARD_USER:$TEST_DASHBOARD_PASSWORD" \
  --cookie-jar "$cookie_jar" --header 'Content-Type: application/json' \
  --data '{"user":"admin","password":"fixture-admin-password"}' "$TEST_BASE_URL/grafana/login")
[ "$login_status" = 200 ] || fail "Grafana administrator login failed (HTTP $login_status)"

sh "$script_dir/query-contract.sh"
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
