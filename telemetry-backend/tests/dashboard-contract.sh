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

check_dashboard() {
  file=$1
  uid=$2
  shift 2
  path=$dashboard_dir/$file
  [ -f "$path" ] || fail "$file is missing"
  jq empty "$path" || fail "$file is not valid JSON"
  [ "$(jq -r '.uid' "$path")" = "$uid" ] || fail "$file has an unexpected UID"
  [ "$(jq -r '.editable' "$path")" = false ] || fail "$file must not be editable"
  jq -e '[.panels[] | select(.type != "text") | .targets[]?]
    | length > 0 and all(.datasource.uid == "victorialogs")' "$path" >/dev/null ||
    fail "$file must use the provisioned VictoriaLogs datasource"
  jq -e '[.panels[] | select(.type != "text") | .targets[]?]
    | all(.expr | contains("service.name=\"ai-agent-telemetry\""))' "$path" >/dev/null ||
    fail "$file contains a query without the service selector"
  jq -e '[.panels[] | select(.type != "text") | .targets[]?
      | select(.expr | contains("| stats"))]
    | all(.queryType == "stats" or .queryType == "statsRange")' "$path" >/dev/null ||
    fail "$file must use the numeric stats query type for aggregate queries"
  jq -e '[.panels[] | select(.type == "table")]
    | all(any(.transformations[]?; .id == "seriesToRows") and
      any(.transformations[]?;
        .id == "extractFields" and .options.source == "Metric" and
        .options.format == "regexp") and
      any(.transformations[]?;
        .id == "organize" and .options.excludeByName.Time == true and
        .options.excludeByName.Metric == true))' \
    "$path" >/dev/null ||
    fail "$file must render grouped series as parsed rows without technical fields"
  jq -e '[.panels[].title, .panels[].fieldConfig.defaults.displayName?]
    | map(select(. != null))
    | all(test("machine\\.id|session\\.id|event\\.id") | not)' "$path" >/dev/null ||
    fail "$file displays a raw identifier"
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

check_display_name() {
  file=$1
  title=$2
  display_name=$3
  path=$dashboard_dir/$file
  jq -e --arg title "$title" --arg display_name "$display_name" '
    .panels[] | select(.title == $title) |
    .fieldConfig.defaults.displayName == $display_name
  ' "$path" >/dev/null || fail "$file must render readable series names in $title"
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

check_dashboard executive-overview.json ai-agent-executive \
  'Active installs' 'Active repositories' 'Active sessions' 'Installs using skills' 'Active install trend' \
  'Telemetry activity' \
  'Top skills' 'Top MCP tools' 'Repository adoption' 'Data semantics'
check_dashboard skill-adoption.json ai-agent-skills \
  'Active installs using skills' 'Active repositories using skills' 'Observed skills' 'Skill events' \
  'Active install trend' 'Skill event trend' 'Top skills by reach and frequency' 'Top skills by events' \
  'Repository by skill' 'Coverage note'
check_dashboard mcp-usage.json ai-agent-mcp \
  'MCP calls' 'Observed tools' 'Observed servers' 'Failure rate' 'p95 latency' 'Outcome trend' \
  'Top MCP tools' 'Tool reliability' 'Repository detail' 'Coverage note'
check_dashboard command-adoption.json ai-agent-commands \
  'Coverage notice' 'Observed commands' 'Active installs' 'Active repositories' 'Command invocations' \
  'Invocation trend' 'Top commands' 'Command sources' 'Repository detail'
check_dashboard telemetry-health.json ai-agent-health \
  'Event ID coverage' 'Machine ID coverage' 'Duplicate delivery rate' 'MCP duration coverage' \
  'Version adoption' 'Harness and OS coverage' 'MCP outcomes' 'Data quality by version'

check_selftest_excluded executive-overview.json 'Active installs'
check_selftest_excluded executive-overview.json 'Active repositories'
check_selftest_excluded executive-overview.json 'Active sessions'
check_selftest_excluded telemetry-health.json 'Harness and OS coverage'
check_selftest_excluded telemetry-health.json 'Version adoption'

jq -e '.templating.list[] | select(.name == "harness") |
  .query.query | contains("agent:!=\"selftest\"")' "$dashboard_dir/skill-adoption.json" >/dev/null ||
  fail 'skill adoption must hide selftest from the harness filter'
jq -e '[.panels[] | select(.type != "text") | .targets[]] |
  all(.expr | contains("agent:!=\"selftest\""))' "$dashboard_dir/skill-adoption.json" >/dev/null ||
  fail 'skill adoption panels must exclude selftest'
jq -e '[.templating.list[].query.query] |
  all(contains("agent:!=\"selftest\""))' "$dashboard_dir/skill-adoption.json" >/dev/null ||
  fail 'skill adoption filters must exclude selftest'

for panel_spec in \
  'executive-overview.json:Active installs' \
  'executive-overview.json:Active repositories' \
  'executive-overview.json:Active sessions' \
  'executive-overview.json:Installs using skills' \
  'skill-adoption.json:Active installs using skills' \
  'skill-adoption.json:Active repositories using skills' \
  'skill-adoption.json:Observed skills' \
  'skill-adoption.json:Skill events' \
  'mcp-usage.json:MCP calls' \
  'mcp-usage.json:Observed tools' \
  'mcp-usage.json:Observed servers' \
  'mcp-usage.json:p95 latency' \
  'command-adoption.json:Observed commands' \
  'command-adoption.json:Active installs' \
  'command-adoption.json:Active repositories' \
  'command-adoption.json:Command invocations'; do
  check_neutral_stat "${panel_spec%%:*}" "${panel_spec#*:}"
done

jq -e '.panels[] | select(.title == "Top skills by events") | .type == "bargauge"' \
  "$dashboard_dir/skill-adoption.json" >/dev/null ||
  fail 'skill adoption must use a bar gauge for the high-cardinality skill ranking'
jq -e '.panels[] | select(.title == "Outcome trend") |
  .fieldConfig.defaults.custom.stacking.mode == "normal"' "$dashboard_dir/mcp-usage.json" >/dev/null ||
  fail 'MCP outcomes must use a stacked time series'
jq -e '.panels[] | select(.title == "MCP outcomes") | .type == "bargauge"' \
  "$dashboard_dir/telemetry-health.json" >/dev/null ||
  fail 'telemetry health must use a readable MCP outcome bar gauge'
jq -e '.panels[] | select(.title == "Failure rate") |
  .fieldConfig.defaults.thresholds.mode == "absolute" and
  .fieldConfig.defaults.noValue == "N/A" and
  [.fieldConfig.defaults.thresholds.steps[].value] == [null, 0.01, 0.05] and
  (.targets[0].expr | endswith("| keep failure_rate"))' "$dashboard_dir/mcp-usage.json" >/dev/null ||
  fail 'MCP failure rate must be a single semantic percentage'
jq -e '[.panels[] | select(.type != "text" and .title != "Observed servers") | .targets[]] |
  all(.expr | contains("| format if (!mcp.server.name:*) \"Unknown\" as mcp.server.name") and
    contains("| filter mcp.server.name:~\"${server:regex}\""))' \
  "$dashboard_dir/mcp-usage.json" >/dev/null ||
  fail 'MCP panels must retain events without a server name and apply the server filter after normalization'
jq -e '.panels[] | select(.title == "Tool reliability") | .targets[0].expr |
  contains("100 * failed / (failed + succeeded) as failure_percent")' \
  "$dashboard_dir/mcp-usage.json" >/dev/null ||
  fail 'per-tool MCP failure rate must be rendered as an explicit percentage'
jq -e '.panels[] | select(.title == "Top MCP tools") | .targets[0].expr |
  contains("| format if (!mcp.server.name:*) \"Unknown\" as mcp.server.name")' \
  "$dashboard_dir/executive-overview.json" >/dev/null ||
  fail 'executive MCP ranking must retain events without a server name'

# Grafana field expressions are intentional literals, not shell expansions.
# shellcheck disable=SC2016
check_display_name skill-adoption.json 'Top skills by events' '${__field.labels["skill.name"]}'
# shellcheck disable=SC2016
check_display_name mcp-usage.json 'Outcome trend' '${__field.labels["mcp.outcome"]}'
# shellcheck disable=SC2016
check_display_name command-adoption.json 'Invocation trend' '${__field.labels["command.expansion_type"]}'
# shellcheck disable=SC2016
check_display_name command-adoption.json 'Command sources' '${__field.labels["command.source"]}'
check_legend_format telemetry-health.json 'Version adoption' '{{service.version}}'
check_legend_format telemetry-health.json 'MCP outcomes' '{{agent}} · {{mcp.outcome}}'

for panel_spec in \
  'executive-overview.json:Top skills' \
  'executive-overview.json:Top MCP tools' \
  'executive-overview.json:Repository adoption' \
  'skill-adoption.json:Top skills by reach and frequency' \
  'mcp-usage.json:Top MCP tools' \
  'mcp-usage.json:Tool reliability' \
  'mcp-usage.json:Repository detail' \
  'command-adoption.json:Top commands' \
  'telemetry-health.json:Data quality by version'; do
  check_metric_mappings "${panel_spec%%:*}" "${panel_spec#*:}"
done

printf 'PASS: Grafana dashboard contract\n'
