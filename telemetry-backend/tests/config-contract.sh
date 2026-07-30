#!/bin/sh
set -eu

backend_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
compose_file=$backend_dir/docker-compose.yml
env_file=$backend_dir/.env.example
caddyfile=$backend_dir/Caddyfile
readme=$backend_dir/README.md
datasource_file=$backend_dir/grafana/provisioning/datasources/victorialogs.yaml
health_dashboard=$backend_dir/grafana/dashboards/telemetry-health.json
collector_config=$backend_dir/otel-collector-config.yaml
fixture_stack=$backend_dir/tests/with-fixture-stack.sh

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

for name in DASHBOARD_AUTH_USER DASHBOARD_AUTH_PASSWORD_HASH GRAFANA_ADMIN_PASSWORD VM_RETENTION; do
  grep -q "^$name=" "$env_file" || fail "$name is missing from .env.example"
done
if grep -q '^GRAFANA_ADMIN_USER=' "$env_file"; then
  fail 'Grafana administrator username must not be configurable'
fi

metrics_pipeline=$(sed -n '/^    metrics:$/,/^    [a-z]/p' "$collector_config")
printf '%s\n' "$metrics_pipeline" | grep -Fq 'processors: [deltatocumulative, batch]' ||
  fail 'metrics pipeline must convert delta metrics and batch them'
if grep -Fq 'transform/metrics_identity' "$collector_config"; then
  fail 'metrics pipeline must not classify harness identity at ingestion'
fi
if grep -Fq 'sed -i' "$fixture_stack"; then
  fail 'fixture stack must render timestamps without sed -i'
fi
caddy_readiness=$(sed -n '/^  status=$(curl/,/sleep 1/p' "$fixture_stack")
if printf '%s\n' "$caddy_readiness" | grep -Fq 'show-error'; then
  fail 'expected fixture-stack readiness retries must not emit curl errors'
fi

grep -q '@ingest path /v1/logs' "$caddyfile" || fail 'ingest path matcher is missing'
grep -q '@grafana path /grafana/\\*' "$caddyfile" || fail 'Grafana path matcher is missing'
grep -q '@grafana_login {' "$caddyfile" || fail 'Grafana login matcher is missing'
grep -q 'path /grafana/login' "$caddyfile" || fail 'Grafana login path is missing'
grep -q '@grafana_native_login {' "$caddyfile" || fail 'Grafana native login matcher is missing'
if ! sed -n '/@grafana_native_login {/,/}/p' "$caddyfile" | grep -q 'method POST'; then
  fail 'Grafana native login POST matcher is missing'
fi
grep -q '@dashboard_entry path / /grafana' "$caddyfile" || fail 'dashboard entry redirect matcher is missing'
grep -q '@vmui path /select/\\*' "$caddyfile" || fail 'VMUI path matcher is missing'
grep -q 'header_up X-WEBAUTH-USER viewer-{http.auth.user.id}' "$caddyfile" ||
  fail 'Grafana auth proxy must isolate viewer identities from native administrators'

rendered=$(mktemp)
trap 'rm -f "$rendered"' EXIT HUP INT TERM
docker compose --env-file "$env_file" -f "$compose_file" config --format json >"$rendered"
jq -e '.services.caddy.ports | length == 2' "$rendered" >/dev/null || fail 'Caddy must publish two ports'
jq -e '[.services.collector.ports, .services.victorialogs.ports] | all(. == null)' "$rendered" >/dev/null ||
  fail 'Collector and VictoriaLogs must not publish ports'
jq -e '.services.victoriametrics != null and .services.victoriametrics.ports == null' "$rendered" >/dev/null ||
  fail 'VictoriaMetrics must exist without published ports'
jq -e 'any(.services.victoriametrics.volumes[]?; .target == "/vmetrics")' "$rendered" >/dev/null ||
  fail 'VictoriaMetrics must persist metrics under /vmetrics'
jq -e '.services.victoriametrics.command | index("-retentionPeriod=30d") != null' "$rendered" >/dev/null ||
  fail 'VictoriaMetrics must use VM_RETENTION'
jq -e '(.services.grafana.build.context // "") | endswith("/telemetry-backend/grafana")' "$rendered" >/dev/null ||
  fail 'Grafana build context is missing'
jq -e '.services.grafana.ports == null' "$rendered" >/dev/null || fail 'Grafana must not publish ports'
jq -e '.services.grafana.environment.GF_AUTH_ANONYMOUS_ENABLED == "false"' "$rendered" >/dev/null ||
  fail 'Grafana anonymous access must be disabled'
jq -e '.services.grafana.environment.GF_SECURITY_ADMIN_USER == "admin"' "$rendered" >/dev/null ||
  fail 'Grafana administrator username must be fixed to admin'
jq -e '.services.grafana.environment.GF_AUTH_PROXY_ENABLED == "true"' "$rendered" >/dev/null ||
  fail 'Grafana auth proxy must be enabled'
jq -e '.services.grafana.environment.GF_AUTH_PROXY_ENABLE_LOGIN_TOKEN == "true"' "$rendered" >/dev/null ||
  fail 'Grafana auth proxy must issue a login token'
jq -e '.services.grafana.environment.GF_USERS_AUTO_ASSIGN_ORG_ROLE == "Viewer"' "$rendered" >/dev/null ||
  fail 'Grafana auth proxy users must receive the Viewer role'
jq -e '.services.grafana.environment.GF_USERS_ALLOW_SIGN_UP == "false"' "$rendered" >/dev/null ||
  fail 'Grafana user sign-up must be disabled'
jq -e '.services.grafana.environment.GF_PLUGINS_PREINSTALL_DISABLED == "true"' "$rendered" >/dev/null ||
  fail 'Grafana default plugin preinstallation must be disabled'
jq -e '.services.grafana.environment.GF_ANALYTICS_REPORTING_ENABLED == "false"' "$rendered" >/dev/null ||
  fail 'Grafana anonymous usage reporting must be disabled'
[ -d "$backend_dir/grafana/provisioning/plugins" ] || fail 'Grafana plugin provisioning directory is missing'
[ -d "$backend_dir/grafana/provisioning/alerting" ] || fail 'Grafana alerting provisioning directory is missing'
grep -q '^    uid: victorialogs$' "$datasource_file" || fail 'VictoriaLogs datasource UID changed'
grep -q '^    editable: true$' "$datasource_file" ||
  fail 'VictoriaLogs datasource must allow administrator edits'

for panel_spec in \
  'Event ID coverage:coverage_percent:80:95' \
  'Machine ID coverage:coverage_percent:80:95' \
  'Duplicate delivery rate:duplicate_percent:0.1:1' \
  'MCP duration coverage:coverage_percent:80:95'; do
  panel_title=${panel_spec%%:*}
  panel_values=${panel_spec#*:}
  percent_field=${panel_values%%:*}
  panel_values=${panel_values#*:}
  warning_threshold=${panel_values%%:*}
  critical_threshold=${panel_values#*:}
  jq -e --arg title "$panel_title" --arg field "$percent_field" \
    --argjson warning "$warning_threshold" --argjson critical "$critical_threshold" '
    .panels[] | select(.title == $title) |
    .fieldConfig.defaults.unit == "percent" and
    .fieldConfig.overrides == [] and
    .fieldConfig.defaults.thresholds.mode == "absolute" and
    [.fieldConfig.defaults.thresholds.steps[].value] == [null, $warning, $critical] and
    (.targets | length) == 1 and
    (.targets[0].expr | endswith("| keep " + $field))
  ' "$health_dashboard" >/dev/null ||
    fail "$panel_title must return and format only $percent_field with semantic thresholds"
done

for text in /grafana/ DASHBOARD_AUTH_USER DASHBOARD_AUTH_PASSWORD_HASH GRAFANA_ADMIN_PASSWORD \
  "administrator username is \`admin\`" 'Upgrade an existing stack' 'docker compose config' \
  'com.docker.compose.volume=grafana-data' "Do not run \`docker compose down -v\`" \
  'grafana cli admin reset-admin-password' 'Adoption overview' 'Telemetry health' \
  'Native agent metrics overview' 'Codex native metrics' 'Native metrics and hook telemetry' \
  'metrics_exporter = { otlp-http = {' 'OTEL_EXPORTER_OTLP_METRICS_ENDPOINT' \
  'OTEL_METRICS_INCLUDE_SESSION_ID=false' 'Cline is not an accepted `--harnesses` target' \
  'Backend fixture'; do
  grep -Fq "$text" "$readme" || fail "backend README is missing: $text"
done
printf 'PASS: backend configuration contract\n'
