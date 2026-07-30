#!/bin/sh
set -eu

dashboard_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/../grafana/dashboards" 2>/dev/null && pwd) || {
  printf 'FAIL: Grafana dashboard directory is missing\n' >&2
  exit 1
}

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

# Universal invariants every provisioned dashboard shares.
check_common() {
  file=$1
  uid=$2
  datasource_uid=$3
  path=$dashboard_dir/$file
  [ -f "$path" ] || fail "$file is missing"
  jq empty "$path" || fail "$file is not valid JSON"
  [ "$(jq -r '.uid' "$path")" = "$uid" ] || fail "$file has an unexpected UID"
  [ "$(jq -r '.editable' "$path")" = false ] || fail "$file must not be editable"
  jq -e --arg uid "$datasource_uid" '
    [.panels[] | select(.type != "text" and .type != "row") | .targets[]?]
    | length > 0 and all(.datasource.uid == $uid)
  ' "$path" >/dev/null || fail "$file must use datasource $datasource_uid"
}

check_logs_selector() {
  file=$1
  path=$dashboard_dir/$file
  jq -e '[.panels[] | select(.type != "text" and .type != "row") | .targets[]?]
    | all(.expr | contains("service.name=\"ai-agent-telemetry\""))' "$path" >/dev/null ||
    fail "$file contains a query without the service selector"
}

# Aggregate queries must return numeric frames (stats/statsRange), not log rows. Panels named in
# $2.. are exempt — they intentionally issue a logs query (e.g. cumulative first-seen onboarding,
# which statsRange cannot express) and shape the series with a Grafana transform.
check_numeric_stats() {
  file=$1
  shift
  path=$dashboard_dir/$file
  jq -e --argjson exempt "$(printf '%s\n' "$@" | jq -R . | jq -s .)" '
    [.panels[]
      | select(.type != "text" and .type != "row")
      | select((.title // "") as $t | ($exempt | index($t)) | not)
      | .targets[]? | select(.expr | contains("| stats"))]
    | all(.queryType == "stats" or .queryType == "statsRange")' "$path" >/dev/null ||
    fail "$file must use the numeric stats query type for aggregate queries"
}

check_titles() {
  file=$1
  shift
  path=$dashboard_dir/$file
  for title in "$@"; do
    jq -e --arg title "$title" 'any(.panels[]; .title == $title)' "$path" >/dev/null ||
      fail "$file is missing panel: $title"
  done
}

check_selftest_excluded() {
  file=$1
  title=$2
  path=$dashboard_dir/$file
  jq -e --arg title "$title" '
    .panels[] | select(.title == $title) |
    all(.targets[]; .expr | contains("agent:!=\"selftest\""))
  ' "$path" >/dev/null || fail "$file must exclude selftest from $title"
}

check_neutral_stat() {
  file=$1
  title=$2
  path=$dashboard_dir/$file
  jq -e --arg title "$title" '
    .panels[] | select(.title == $title) |
    .type == "stat" and .options.colorMode == "none" and .options.graphMode == "none"
  ' "$path" >/dev/null || fail "$file must render $title as a neutral stat"
}

check_legend_format() {
  file=$1
  title=$2
  legend_format=$3
  path=$dashboard_dir/$file
  jq -e --arg title "$title" --arg legend_format "$legend_format" '
    .panels[] | select(.title == $title) |
    .targets[0].legendFormat == $legend_format
  ' "$path" >/dev/null || fail "$file must render readable series names in $title"
}

check_metric_mappings() {
  file=$1
  title=$2
  path=$dashboard_dir/$file
  jq -e --arg title "$title" '
    .panels[] | select(.title == $title) |
    ((.fieldConfig.defaults.mappings // []) | length) > 0 or
    any(.fieldConfig.overrides[]?;
      .matcher.id == "byName" and .matcher.options == "Metric" and
      any(.properties[]?; .id == "mappings" and (.value | length) > 0))
  ' "$path" >/dev/null || fail "$file must humanize metric names in $title"
}

# --- Telemetry health -------------------------------------------------------
health=telemetry-health.json
check_common "$health" ai-agent-health victorialogs
check_logs_selector "$health"
check_numeric_stats "$health"
check_titles "$health" \
  'Event ID coverage' 'Machine ID coverage' 'Duplicate delivery rate' 'MCP duration coverage' \
  'Version adoption' 'Harness and OS coverage' 'MCP outcomes' 'Data quality by version'

# Neither identifier-named title nor any display name may leak a raw identifier value.
jq -e '[.panels[].title, .panels[].fieldConfig.defaults.displayName?]
  | map(select(. != null))
  | all(test("machine\\.id|session\\.id|event\\.id") | not)' \
  "$dashboard_dir/$health" >/dev/null || fail "$health displays a raw identifier"

check_selftest_excluded "$health" 'Harness and OS coverage'
check_selftest_excluded "$health" 'Version adoption'
jq -e '.panels[] | select(.title == "MCP outcomes") | .type == "bargauge"' \
  "$dashboard_dir/$health" >/dev/null ||
  fail 'telemetry health must use a readable MCP outcome bar gauge'
check_legend_format "$health" 'Version adoption' '{{service.version}}'
check_legend_format "$health" 'MCP outcomes' '{{agent}} · {{mcp.outcome}}'
check_metric_mappings "$health" 'Data quality by version'

# --- Adoption overview (replaces the four per-domain dashboards) ------------
adoption=ai-agent-telemetry-adoption.json
check_common "$adoption" ai-agent-telemetry-adoption victorialogs
check_logs_selector "$adoption"
check_numeric_stats "$adoption" 'Onboarding over time'
check_titles "$adoption" \
  'Installs' 'Used in repositories' 'Sessions caught' 'Telemetry activity' 'Onboarding over time' \
  'Activity per machine' 'Top skills executed' 'Top MCPs' 'Machines by harness' 'Machines by OS' \
  'Skills per repository (matrix)' 'Skills per repository (stacked)' 'Skills per repository (table)' \
  'MCPs per repository (matrix)' 'MCPs per repository (stacked)' 'MCPs per repository (table)'

# Flat per-repository tables must parse the grouped series into readable rows.
for title in 'Skills per repository (table)' 'MCPs per repository (table)'; do
  jq -e --arg title "$title" '
    .panels[] | select(.title == $title) |
    any(.transformations[]?; .id == "seriesToRows") and
    any(.transformations[]?;
      .id == "extractFields" and .options.source == "Metric" and .options.format == "regexp") and
    any(.transformations[]?; .id == "organize")' \
    "$dashboard_dir/$adoption" >/dev/null ||
    fail "$adoption panel '$title' must render grouped series as parsed rows"
done

# Session and event identifiers are never exposed. A raw machine.id is allowed only in the
# intentional per-machine ranking, where the machine is the unit of analysis.
jq -e '[.panels[].title, .panels[].fieldConfig.defaults.displayName?]
  | map(select(. != null))
  | all(test("session\\.id|event\\.id") | not)' "$dashboard_dir/$adoption" >/dev/null ||
  fail "$adoption displays a raw session or event identifier"
jq -e '[.panels[] | select(.title != "Activity per machine")
    | .title, .fieldConfig.defaults.displayName?]
  | map(select(. != null))
  | all(test("machine\\.id") | not)' "$dashboard_dir/$adoption" >/dev/null ||
  fail "$adoption exposes a machine identifier outside the per-machine panel"

for title in 'Installs' 'Used in repositories' 'Sessions caught'; do
  check_neutral_stat "$adoption" "$title"
done
for title in 'Installs' 'Used in repositories' 'Sessions caught' 'Machines by harness' 'Machines by OS'; do
  check_selftest_excluded "$adoption" "$title"
done

# --- Native agent metrics overview -----------------------------------------
overview=native-agent-metrics-overview.json
check_common "$overview" native-agent-metrics-overview victoriametrics
check_titles "$overview" \
  'Signal availability' 'Metrics freshness' 'Top-level sessions started' 'Token usage over time' \
  'Tokens by model and type' 'Observed client versions'

overview_path=$dashboard_dir/$overview
jq -e '
  any(.panels[].targets[]?; .expr | contains("service_name=\"codex_cli_rs\"")) and
  any(.panels[].targets[]?; .expr | contains("service_name=~\"claude-code|claude-code-desktop\"")) and
  all(.panels[].targets[]?; (.expr | contains("agent_harness")) | not)
' "$overview_path" >/dev/null || fail "$overview must classify Codex and Claude without agent_harness"

jq -e '
  .panels[] | select(.title == "Top-level sessions started") |
  any(.targets[]?; .expr | contains("session_source=\"cli\"")) and
  any(.targets[]?; .expr | contains("start_type!=\"agents_view\""))
' "$overview_path" >/dev/null || fail "$overview sessions panel must select top-level sessions"

for title in 'Token usage over time' 'Tokens by model and type'; do
  jq -e --arg title "$title" '
    .panels[] | select(.title == $title) |
    .fieldConfig.defaults.unit as $unit | $unit == "tokens" or $unit == "tps"
  ' "$overview_path" >/dev/null || fail "$overview token panel '$title' must use tokens or tps"
done

printf 'PASS: Grafana dashboard contract\n'
