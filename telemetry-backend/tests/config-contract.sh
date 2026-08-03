#!/bin/sh
# Literal Markdown and shell snippets are matched as data, not evaluated by this script.
# shellcheck disable=SC2016

set -eu

backend_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
compose_file=$backend_dir/docker-compose.yml
env_file=$backend_dir/.env.example
caddyfile=$backend_dir/Caddyfile
readme=$backend_dir/README.md
native_onboarding=$backend_dir/native-otlp-onboarding.md
datasource_file=$backend_dir/grafana/provisioning/datasources/victorialogs.yaml
health_dashboard=$backend_dir/grafana/dashboards/telemetry-health.json
adoption_dashboard=$backend_dir/grafana/dashboards/ai-agent-telemetry-adoption.json
overview_dashboard=$backend_dir/grafana/dashboards/native-agent-metrics-overview.json
codex_dashboard=$backend_dir/grafana/dashboards/codex-native-metrics.json
collector_config=$backend_dir/otel-collector-config.yaml
fixture_stack=$backend_dir/tests/with-fixture-stack.sh

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

for name in DASHBOARD_AUTH_USER DASHBOARD_AUTH_PASSWORD_HASH GRAFANA_ADMIN_PASSWORD VL_RETENTION VM_RETENTION \
  VM_MAX_HOURLY_SERIES VM_MAX_DAILY_SERIES VM_MIN_FREE_DISK_SPACE_BYTES; do
  grep -q "^$name=" "$env_file" || fail "$name is missing from .env.example"
done
if grep -q '^GRAFANA_ADMIN_USER=' "$env_file"; then
  fail 'Grafana administrator username must not be configurable'
fi

metrics_pipeline=$(sed -n '/^    metrics:$/,/^    [a-z]/p' "$collector_config")
printf '%s\n' "$metrics_pipeline" |
  grep -Fq 'processors: [memory_limiter, resource/privacy, attributes/privacy, deltatocumulative, batch]' ||
  fail 'metrics pipeline must limit memory, remove sensitive attributes, convert delta metrics, and batch them'
logs_pipeline=$(sed -n '/^    logs:$/,/^    [a-z]/p' "$collector_config")
printf '%s\n' "$logs_pipeline" |
  grep -Fq 'processors: [memory_limiter, batch]' ||
  fail 'logs pipeline must limit memory before batching events'
grep -Fq 'max_streams: 100000' "$collector_config" ||
  fail 'delta-to-cumulative state must have a bounded stream count'
grep -Fq 'limit_mib: 512' "$collector_config" ||
  fail 'Collector memory usage must have an explicit hard limit'
for processor in resource/privacy attributes/privacy; do
  section=$(awk -v heading="  $processor:" '
    $0 == heading { in_section = 1; next }
    in_section && /^  [a-z]/ { exit }
    in_section { print }
  ' "$collector_config")
  for key in session.id user.email user.account_uuid organization.id; do
    printf '%s\n' "$section" | grep -Fq "key: $key" ||
      fail "$processor must remove $key"
  done
done
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

upgrade_section=$(sed -n '/^## Upgrade an existing stack$/,/^## Dashboards$/p' "$readme")
printf '%s\n' "$upgrade_section" | grep -Fqx 'VM_RETENTION=365d' ||
  fail 'backend README upgrade snippet must use the default 365-day metrics retention'
upgrade_text=$(printf '%s\n' "$upgrade_section" | tr '\n' ' ' | tr -s ' ')
printf '%s\n' "$upgrade_text" |
  grep -Fq 'Do not run `docker compose down -v`: it deletes both the VictoriaLogs and VictoriaMetrics data volumes.' ||
  fail 'backend README must warn that down -v deletes both telemetry data volumes'

dashboards_section=$(sed -n '/^## Dashboards$/,/^## Native agent metrics$/p' "$readme")
dashboards_text=$(printf '%s\n' "$dashboards_section" | tr '\n' ' ' | tr -s ' ')
for text in 'Adoption overview' 'Telemetry health' 'Native agent metrics overview' 'Codex native metrics'; do
  printf '%s\n' "$dashboards_text" | grep -Fq "$text" ||
    fail "backend README dashboard summary is missing: $text"
done
printf '%s\n' "$dashboards_text" | grep -Fq 'per-installation' ||
  fail 'backend README dashboard summary must use per-installation terminology'
if printf '%s\n' "$dashboards_text" | grep -Fq 'per-machine'; then
  fail 'backend README dashboard summary must not use per-machine terminology'
fi
if printf '%s\n' "$dashboards_text" | grep -Fq 'event.id'; then
  fail 'backend README dashboard summary must not describe obsolete event.id health semantics'
fi

[ "$(jq -r '.time.from' "$adoption_dashboard")" = now-30d/d ] &&
  [ "$(jq -r '.time.to' "$adoption_dashboard")" = now/d ] &&
  [ "$(jq -r '.timezone' "$adoption_dashboard")" = utc ] ||
  fail 'Adoption overview must default to complete UTC days over the previous 30 days'
for dashboard in "$health_dashboard" "$overview_dashboard" "$codex_dashboard"; do
  [ "$(jq -r '.time.from' "$dashboard")" = now-7d ] ||
    fail "$(basename "$dashboard") must default to seven days"
done

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
legacy_env=$(mktemp)
trap 'rm -f "$rendered" "$legacy_env"' EXIT HUP INT TERM
docker compose --env-file "$env_file" -f "$compose_file" config --format json >"$rendered"
jq -e '.services.caddy.ports | length == 2' "$rendered" >/dev/null || fail 'Caddy must publish two ports'
jq -e '[.services.collector.ports, .services.victorialogs.ports] | all(. == null)' "$rendered" >/dev/null ||
  fail 'Collector and VictoriaLogs must not publish ports'
jq -e '.services.victoriametrics != null and .services.victoriametrics.ports == null' "$rendered" >/dev/null ||
  fail 'VictoriaMetrics must exist without published ports'
jq -e 'any(.services.victoriametrics.volumes[]?; .target == "/vmetrics")' "$rendered" >/dev/null ||
  fail 'VictoriaMetrics must persist metrics under /vmetrics'
jq -e '.services.victorialogs.command | index("-retentionPeriod=365d") != null' "$rendered" >/dev/null ||
  fail 'VictoriaLogs must use VL_RETENTION'
jq -e '.services.victoriametrics.command | index("-retentionPeriod=365d") != null' "$rendered" >/dev/null ||
  fail 'VictoriaMetrics must use VM_RETENTION'
jq -e '.services.victoriametrics.command | index("-storage.maxHourlySeries=50000") != null' "$rendered" >/dev/null ||
  fail 'VictoriaMetrics must bound hourly series cardinality'
jq -e '.services.victoriametrics.command | index("-storage.maxDailySeries=200000") != null' "$rendered" >/dev/null ||
  fail 'VictoriaMetrics must bound daily series cardinality'
jq -e '.services.victoriametrics.command | index("-storage.minFreeDiskSpaceBytes=1073741824") != null' "$rendered" >/dev/null ||
  fail 'VictoriaMetrics must reserve free disk space'
jq -e '.services.victoriametrics.command | index("-selfScrapeInterval=30s") != null' "$rendered" >/dev/null ||
  fail 'VictoriaMetrics must self-scrape operational metrics every 30 seconds'
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

for text in /grafana/ DASHBOARD_AUTH_USER DASHBOARD_AUTH_PASSWORD_HASH GRAFANA_ADMIN_PASSWORD \
  "administrator username is \`admin\`" 'Upgrade an existing stack' 'docker compose config' \
  'com.docker.compose.volume=grafana-data' "Do not run \`docker compose down -v\`" \
  'grafana cli admin reset-admin-password' 'Adoption overview' 'Telemetry health' \
  'Native agent metrics overview' 'Codex native metrics' 'Native metrics and hook telemetry' \
  'Native OTLP onboarding' 'does not configure harness' 'Backend fixture'; do
  grep -Fq "$text" "$readme" || fail "backend README is missing: $text"
done

for text in \
  'metrics_exporter = { otlp-http = {' 'OTEL_EXPORTER_OTLP_METRICS_ENDPOINT' \
  'OTEL_METRICS_INCLUDE_SESSION_ID=false' 'Cline is not an accepted `--harnesses` target' \
  'per-session high-cardinality labels' '## Verify ingestion' '## Remove the configuration'; do
  grep -Fq "$text" "$native_onboarding" || fail "native OTLP onboarding guide is missing: $text"
done

for text in 'metrics_exporter = { otlp-http = {' 'OTEL_EXPORTER_OTLP_METRICS_ENDPOINT'; do
  if grep -Fq "$text" "$readme"; then
    fail "backend README must link to, not duplicate, native exporter configuration: $text"
  fi
done

support_section=$(sed -n '/^## Native agent metrics$/,/^### Native metrics and hook telemetry$/p' "$readme")
for harness in Codex 'Claude Code' Cursor Cline; do
  printf '%s\n' "$support_section" | grep -Eq "^\\| $harness \\|([^|]*\\|){4}$" ||
    fail "backend README support matrix is missing a five-column row for $harness"
done
for term in 'Backend fixture' 'Live pilot'; do
  printf '%s\n' "$support_section" | grep -Fq "\`$term\`" ||
    fail "backend README must define $term"
done

for line in \
  'exporter = "none"' \
  'trace_exporter = "none"' \
  'log_user_prompt = false' \
  'export OTEL_LOGS_EXPORTER=none' \
  'export OTEL_TRACES_EXPORTER=none' \
  'export OTEL_METRICS_INCLUDE_SESSION_ID=false'; do
  grep -Fqx "$line" "$native_onboarding" ||
    fail "native OTLP onboarding guide is missing exact safety contract: $line"
done
sed '/^VL_RETENTION=/d; /^VM_RETENTION=/d' "$env_file" >"$legacy_env"
docker compose --env-file "$legacy_env" -f "$compose_file" config --format json >"$rendered"
jq -e '.services.victorialogs.command | index("-retentionPeriod=365d") != null' "$rendered" >/dev/null ||
  fail 'VictoriaLogs must default to 365d when a legacy environment omits VL_RETENTION'
jq -e '.services.victoriametrics.command | index("-retentionPeriod=365d") != null' "$rendered" >/dev/null ||
  fail 'VictoriaMetrics must default to 365d when a legacy environment omits VM_RETENTION'
printf '%s\n' \
  'VL_RETENTION=14d' \
  'VM_RETENTION=7d' \
  'VM_MAX_HOURLY_SERIES=12345' \
  'VM_MAX_DAILY_SERIES=54321' \
  'VM_MIN_FREE_DISK_SPACE_BYTES=268435456' >>"$legacy_env"
docker compose --env-file "$legacy_env" -f "$compose_file" config --format json >"$rendered"
jq -e '.services.victorialogs.command | index("-retentionPeriod=14d") != null' "$rendered" >/dev/null ||
  fail 'VictoriaLogs must honor an explicit VL_RETENTION override'
jq -e '.services.victoriametrics.command | index("-retentionPeriod=7d") != null' "$rendered" >/dev/null ||
  fail 'VictoriaMetrics must honor an explicit VM_RETENTION override'
jq -e '.services.victoriametrics.command | index("-storage.maxHourlySeries=12345") != null' "$rendered" >/dev/null ||
  fail 'VictoriaMetrics must honor an explicit hourly series override'
jq -e '.services.victoriametrics.command | index("-storage.maxDailySeries=54321") != null' "$rendered" >/dev/null ||
  fail 'VictoriaMetrics must honor an explicit daily series override'
jq -e '.services.victoriametrics.command | index("-storage.minFreeDiskSpaceBytes=268435456") != null' "$rendered" >/dev/null ||
  fail 'VictoriaMetrics must honor an explicit free-disk override'
printf 'PASS: backend configuration contract\n'
