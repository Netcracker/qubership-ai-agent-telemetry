#!/bin/sh
set -eu

backend_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
compose_file=$backend_dir/docker-compose.yml
env_file=$backend_dir/.env.example
caddyfile=$backend_dir/Caddyfile
readme=$backend_dir/README.md

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

for name in DASHBOARD_AUTH_USER DASHBOARD_AUTH_PASSWORD_HASH GRAFANA_ADMIN_USER GRAFANA_ADMIN_PASSWORD; do
  grep -q "^$name=" "$env_file" || fail "$name is missing from .env.example"
done

grep -q '@ingest path /v1/logs' "$caddyfile" || fail 'ingest path matcher is missing'
grep -q '@grafana path /grafana/\\*' "$caddyfile" || fail 'Grafana path matcher is missing'
grep -q '@dashboard_entry path / /grafana' "$caddyfile" || fail 'dashboard entry redirect matcher is missing'
grep -q '@vmui path /select/\\*' "$caddyfile" || fail 'VMUI path matcher is missing'

rendered=$(mktemp)
trap 'rm -f "$rendered"' EXIT HUP INT TERM
docker compose --env-file "$env_file" -f "$compose_file" config --format json >"$rendered"
jq -e '.services.caddy.ports | length == 2' "$rendered" >/dev/null || fail 'Caddy must publish two ports'
jq -e '[.services.collector.ports, .services.victorialogs.ports] | all(. == null)' "$rendered" >/dev/null ||
  fail 'Collector and VictoriaLogs must not publish ports'
jq -e '(.services.grafana.build.context // "") | endswith("/telemetry-backend/grafana")' "$rendered" >/dev/null ||
  fail 'Grafana build context is missing'
jq -e '.services.grafana.ports == null' "$rendered" >/dev/null || fail 'Grafana must not publish ports'
jq -e '.services.grafana.environment.GF_AUTH_ANONYMOUS_ORG_ROLE == "Viewer"' "$rendered" >/dev/null ||
  fail 'Grafana anonymous Viewer role is missing'
jq -e '.services.grafana.environment.GF_USERS_ALLOW_SIGN_UP == "false"' "$rendered" >/dev/null ||
  fail 'Grafana user sign-up must be disabled'
jq -e '.services.grafana.environment.GF_PLUGINS_PREINSTALL_DISABLED == "true"' "$rendered" >/dev/null ||
  fail 'Grafana default plugin preinstallation must be disabled'
jq -e '.services.grafana.environment.GF_ANALYTICS_REPORTING_ENABLED == "false"' "$rendered" >/dev/null ||
  fail 'Grafana anonymous usage reporting must be disabled'
[ -d "$backend_dir/grafana/provisioning/plugins" ] || fail 'Grafana plugin provisioning directory is missing'
[ -d "$backend_dir/grafana/provisioning/alerting" ] || fail 'Grafana alerting provisioning directory is missing'

for text in /grafana/ DASHBOARD_AUTH_USER DASHBOARD_AUTH_PASSWORD_HASH GRAFANA_ADMIN_USER \
  GRAFANA_ADMIN_PASSWORD 'grafana cli admin reset-admin-password' 'Executive overview' 'Skill adoption' \
  'MCP usage and reliability' 'Command adoption' 'Telemetry health'; do
  grep -Fq "$text" "$readme" || fail "backend README is missing: $text"
done

printf 'PASS: backend configuration contract\n'
