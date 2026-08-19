#!/bin/sh
set -eu

[ "$#" -gt 0 ] || {
  printf 'usage: %s command [args...]\n' "$0" >&2
  exit 2
}

backend_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
compose_file=$backend_dir/docker-compose.yml
fixture=$backend_dir/tests/fixtures/otel-events.json
metrics_fixture=$backend_dir/tests/fixtures/otel-metrics.json
tmp_dir=$(mktemp -d)
project=telemetry-dashboard-contract-$$
env_file=$tmp_dir/backend.env
ca_cert=$tmp_dir/caddy-root.crt
rendered_fixture=$tmp_dir/otel-events.json
rendered_metrics_fixture=$tmp_dir/otel-metrics.json
event_sed_script=$tmp_dir/otel-events.sed
dashboard_user='viewer'
dashboard_password='fixture-viewer-password'
grafana_admin_password='fixture-admin-password'
ingest_token='fixture-ingest-token'
http_port=${TEST_HTTP_PORT:-18080}
https_port=${TEST_HTTPS_PORT:-18443}
base_url=https://localhost:$https_port

compose() {
  docker compose -p "$project" --env-file "$env_file" -f "$compose_file" "$@"
}

cleanup() {
  compose down -v --remove-orphans >/dev/null 2>&1 || true
  rm -rf "$tmp_dir"
}
trap cleanup EXIT HUP INT TERM

password_hash=$(docker run --rm caddy:2 caddy hash-password --plaintext "$dashboard_password")
admin_password_hash=$(docker run --rm caddy:2 caddy hash-password --plaintext "$grafana_admin_password")
{
  printf '%s\n' 'SITE_ADDRESS=localhost'
  printf '%s\n' 'CADDY_TLS=internal'
  printf 'INGEST_TOKEN=%s\n' "$ingest_token"
  printf 'DASHBOARD_AUTH_USER=%s\n' "$dashboard_user"
  printf "DASHBOARD_AUTH_PASSWORD_HASH='%s'\n" "$password_hash"
  printf '%s\n' "GRAFANA_ADMIN_PASSWORD=$grafana_admin_password"
  printf "GRAFANA_ADMIN_PASSWORD_HASH='%s'\n" "$admin_password_hash"
  printf '%s\n' 'VL_RETENTION=30d'
  printf '%s\n' 'VM_RETENTION=30d'
  printf '%s\n' 'VM_SELF_SCRAPE_INTERVAL=5s'
  printf 'HTTP_PORT=%s\n' "$http_port"
  printf 'HTTPS_PORT=%s\n' "$https_port"
} >"$env_file"

if [ "${TEST_WITH_GRAFANA:-}" = 1 ]; then
  compose up -d
else
  compose up -d victorialogs collector caddy
fi

attempt=0
while ! compose cp caddy:/data/caddy/pki/authorities/local/root.crt "$ca_cert" >/dev/null 2>&1; do
  attempt=$((attempt + 1))
  [ "$attempt" -lt 60 ] || {
    printf 'FAIL: Caddy root certificate was not created\n' >&2
    exit 1
  }
  sleep 1
done

attempt=0
while :; do
  status=$(curl --silent --output /dev/null --write-out '%{http_code}' \
    --cacert "$ca_cert" "$base_url/unknown" || true)
  [ "$status" = 404 ] && break
  attempt=$((attempt + 1))
  [ "$attempt" -lt 60 ] || {
    printf 'FAIL: Caddy did not become ready\n' >&2
    exit 1
  }
  sleep 1
done

now=$(date -u +%s)
hour=$((now - now % 3600))
index=1
while [ "$index" -le 8 ]; do
  timestamp=$((hour * 1000000000 + index * 100000000))
  printf 's/__TS_%s__/%s/g\n' "$index" "$timestamp" >>"$event_sed_script"
  index=$((index + 1))
done
stale_timestamp=$(((hour - 2 * 86400) * 1000000000 + 900000000))
printf 's/__TS_9__/%s/g\n' "$stale_timestamp" >>"$event_sed_script"
cline_timestamp=$((hour * 1000000000 + 1000000000))
printf 's/__TS_10__/%s/g\n' "$cline_timestamp" >>"$event_sed_script"
cline_mcp_timestamp=$((hour * 1000000000 + 1100000000))
printf 's/__TS_11__/%s/g\n' "$cline_mcp_timestamp" >>"$event_sed_script"
sed -f "$event_sed_script" "$fixture" >"$rendered_fixture"

metric_fixture_time=$((now - 60))
metric_start_timestamp=$(((metric_fixture_time - 1) * 1000000000))
metric_timestamp=$((metric_fixture_time * 1000000000))
metric_visibility_cutoff=$(((now - 45) * 1000000000))
[ "$metric_timestamp" -le "$metric_visibility_cutoff" ] || {
  printf 'FAIL: fixture metrics must be timestamped at least 45 seconds in the past\n' >&2
  exit 1
}
sed -e "s/__METRIC_START_TS__/$metric_start_timestamp/g" \
  -e "s/__METRIC_TS__/$metric_timestamp/g" \
  "$metrics_fixture" >"$rendered_metrics_fixture"

curl --fail --silent --show-error --cacert "$ca_cert" \
  --header "Authorization: Bearer $ingest_token" \
  --header 'Content-Type: application/json' \
  --data-binary "@$rendered_fixture" \
  "$base_url/v1/logs" >/dev/null

attempt=0
while :; do
  total=$(curl --fail --silent --cacert "$ca_cert" \
    --user "$dashboard_user:$dashboard_password" \
    --data-urlencode 'query={service.name="ai-agent-telemetry"} | stats count() total' \
    --data-urlencode "start=$hour" \
    --data-urlencode "end=$((hour + 3600))" \
    "$base_url/select/logsql/query" |
    jq -sr 'if length == 1 then .[0].total // empty else empty end')
  [ "$total" = 10 ] && break
  attempt=$((attempt + 1))
  [ "$attempt" -lt 30 ] || {
    printf 'FAIL: fixture events did not reach VictoriaLogs (found %s, want 10)\n' "$total" >&2
    exit 1
  }
  sleep 1
done

metrics_status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
  --cacert "$ca_cert" \
  --request POST \
  --header "Authorization: Bearer $ingest_token" \
  --header 'Content-Type: application/json' \
  --data-binary "@$rendered_metrics_fixture" \
  "$base_url/v1/metrics")
[ "$metrics_status" = 200 ] || {
  printf 'FAIL: metrics ingest failed (HTTP %s)\n' "$metrics_status" >&2
  exit 1
}

attempt=0
while :; do
  metric_value=$(curl --fail --silent --cacert "$ca_cert" \
    --user "$dashboard_user:$dashboard_password" \
    --get \
    --data-urlencode 'query=codex_tool_call_total{tool="exec_command",success="true"}' \
    "$base_url/prometheus/api/v1/query" |
    jq -r '.data.result[0].value[1] // empty')
  [ "$metric_value" = 3 ] && break
  attempt=$((attempt + 1))
  [ "$attempt" -lt 30 ] || {
    printf 'FAIL: fixture metric did not reach VictoriaMetrics (found %s, want 3)\n' "$metric_value" >&2
    exit 1
  }
  sleep 1
done

export TEST_BASE_URL="$base_url"
export TEST_CA_CERT="$ca_cert"
export TEST_DASHBOARD_USER="$dashboard_user"
export TEST_DASHBOARD_PASSWORD="$dashboard_password"
export TEST_GRAFANA_ADMIN_PASSWORD="$grafana_admin_password"
export TEST_TIME_FROM="$hour"
export TEST_TIME_TO=$((hour + 3600))
export TEST_COMPOSE_PROJECT="$project"
export TEST_ENV_FILE="$env_file"
export TEST_COMPOSE_FILE="$compose_file"
export TEST_INGEST_TOKEN="$ingest_token"
export TEST_RENDERED_FIXTURE="$rendered_fixture"
export TEST_RENDERED_METRICS_FIXTURE="$rendered_metrics_fixture"

"$@"
