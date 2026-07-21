#!/bin/sh
set -eu

[ "$#" -gt 0 ] || {
  printf 'usage: %s command [args...]\n' "$0" >&2
  exit 2
}

backend_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
compose_file=$backend_dir/docker-compose.yml
fixture=$backend_dir/tests/fixtures/otel-events.json
tmp_dir=$(mktemp -d)
project=telemetry-dashboard-contract-$$
env_file=$tmp_dir/backend.env
ca_cert=$tmp_dir/caddy-root.crt
rendered_fixture=$tmp_dir/otel-events.json
dashboard_user='admin'
dashboard_password='fixture-viewer-password'
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
{
  printf '%s\n' 'SITE_ADDRESS=localhost'
  printf '%s\n' 'CADDY_TLS=internal'
  printf 'INGEST_TOKEN=%s\n' "$ingest_token"
  printf 'DASHBOARD_AUTH_USER=%s\n' "$dashboard_user"
  printf "DASHBOARD_AUTH_PASSWORD_HASH='%s'\n" "$password_hash"
  printf '%s\n' 'GRAFANA_ADMIN_PASSWORD=fixture-admin-password'
  printf '%s\n' 'VL_RETENTION=30d'
  printf 'HTTP_PORT=%s\n' "$http_port"
  printf 'HTTPS_PORT=%s\n' "$https_port"
} >"$env_file"

if [ "${TEST_WITH_GRAFANA:-}" = 1 ]; then
  compose up -d --build
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
  status=$(curl --silent --show-error --output /dev/null --write-out '%{http_code}' \
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
cp "$fixture" "$rendered_fixture"
while [ "$index" -le 8 ]; do
  timestamp=$((hour * 1000000000 + index * 100000000))
  sed -i "s/__TS_${index}__/$timestamp/g" "$rendered_fixture"
  index=$((index + 1))
done

curl --fail --silent --show-error --cacert "$ca_cert" \
  --header "Authorization: Bearer $ingest_token" \
  --header 'Content-Type: application/json' \
  --data-binary "@$rendered_fixture" \
  "$base_url/v1/logs" >/dev/null

attempt=0
while :; do
  total=$(curl --fail --silent --show-error --cacert "$ca_cert" \
    --user "$dashboard_user:$dashboard_password" \
    --data-urlencode 'query={service.name="ai-agent-telemetry"} | stats count() total' \
    --data-urlencode "start=$hour" \
    --data-urlencode "end=$((hour + 3600))" \
    "$base_url/select/logsql/query" |
    jq -sr 'if length == 1 then .[0].total // empty else empty end')
  [ "$total" = 8 ] && break
  attempt=$((attempt + 1))
  [ "$attempt" -lt 30 ] || {
    printf 'FAIL: fixture events did not reach VictoriaLogs (found %s, want 8)\n' "$total" >&2
    exit 1
  }
  sleep 1
done

export TEST_BASE_URL="$base_url"
export TEST_CA_CERT="$ca_cert"
export TEST_DASHBOARD_USER="$dashboard_user"
export TEST_DASHBOARD_PASSWORD="$dashboard_password"
export TEST_TIME_FROM="$hour"
export TEST_TIME_TO=$((hour + 3600))
export TEST_COMPOSE_PROJECT="$project"
export TEST_ENV_FILE="$env_file"
export TEST_COMPOSE_FILE="$compose_file"
export TEST_INGEST_TOKEN="$ingest_token"
export TEST_RENDERED_FIXTURE="$rendered_fixture"

"$@"
