#!/bin/sh
set -eu

backend_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
compose_file=$backend_dir/docker-compose.yml
env_file=$backend_dir/.env.example
caddyfile=$backend_dir/Caddyfile

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

for name in DASHBOARD_AUTH_USER DASHBOARD_AUTH_PASSWORD_HASH GRAFANA_ADMIN_USER GRAFANA_ADMIN_PASSWORD; do
  grep -q "^$name=" "$env_file" || fail "$name is missing from .env.example"
done

grep -q '@ingest path /v1/logs' "$caddyfile" || fail 'ingest path matcher is missing'
grep -q '@grafana path /grafana/\\*' "$caddyfile" || fail 'Grafana path matcher is missing'
grep -q '@vmui path /select/\\*' "$caddyfile" || fail 'VMUI path matcher is missing'

rendered=$(mktemp)
trap 'rm -f "$rendered"' EXIT HUP INT TERM
docker compose --env-file "$env_file" -f "$compose_file" config --format json >"$rendered"
jq -e '.services.caddy.ports | length == 2' "$rendered" >/dev/null || fail 'Caddy must publish two ports'
jq -e '[.services.collector.ports, .services.victorialogs.ports] | all(. == null)' "$rendered" >/dev/null ||
  fail 'Collector and VictoriaLogs must not publish ports'

printf 'PASS: backend configuration contract\n'
