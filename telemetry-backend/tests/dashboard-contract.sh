#!/bin/sh
# PromQL templates such as $__range and $__rate_interval are matched literally.
# shellcheck disable=SC2016

set -eu

default_dashboard_dir=$(CDPATH='' cd -- "$(dirname -- "$0")/../grafana/dashboards" 2>/dev/null && pwd) || {
  printf 'FAIL: Grafana dashboard directory is missing\n' >&2
  exit 1
}
dashboard_dir=${DASHBOARD_DIR:-$default_dashboard_dir}
[ -d "$dashboard_dir" ] || {
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
  shift 2
  path=$dashboard_dir/$file
  allowed_uids=$(printf '%s\n' "$@" | jq -R . | jq -s .)

  [ -f "$path" ] || fail "$file is missing"
  jq empty "$path" || fail "$file is not valid JSON"
  [ "$(jq -r '.uid' "$path")" = "$uid" ] || fail "$file has an unexpected UID"
  [ "$(jq -r '.editable' "$path")" = false ] || fail "$file must not be editable"
  jq -e --argjson allowed_uids "$allowed_uids" '
    [.panels[] | select(.type != "text" and .type != "row") | .targets[]?]
    | length > 0 and all(.datasource.uid as $uid | $allowed_uids | index($uid) != null)
  ' "$path" >/dev/null || fail "$file uses an unexpected datasource"
}

check_logs_selector() {
  file=$1
  path=$dashboard_dir/$file
  jq -e '
    [.panels[] | select(.type != "text" and .type != "row") | .targets[]?
      | select(.datasource.uid == "victorialogs")]
    | length > 0 and all(.expr | contains("service.name=\"ai-agent-telemetry\""))
  ' "$path" >/dev/null ||
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
check_common "$health" ai-agent-health victorialogs victoriametrics
check_logs_selector "$health"
check_numeric_stats "$health"
check_titles "$health" \
  'OTLP coverage boundary' 'Machines not seen on target version' \
  'Inactive for more than 24 hours' 'Inactive for more than 48 hours' \
  'Native metric sample age' 'Stale installations' \
  'Active installations by version' 'Active installations by harness and OS'

health_path=$dashboard_dir/$health
for removed_title in \
  'Event ID coverage' 'Machine ID coverage' 'Duplicate delivery rate' \
  'MCP duration coverage' 'Data quality by version' 'Version adoption' \
  'Harness and OS coverage' 'MCP outcomes'; do
  jq -e --arg title "$removed_title" 'all(.panels[]; .title != $title)' "$health_path" >/dev/null ||
    fail "$health still contains legacy panel: $removed_title"
done

jq -e '
  .panels[] | select(.title == "OTLP coverage boundary") |
  .type == "text" and .options.mode == "markdown" and
  .options.content == "Native OTLP metrics do not share an installation identifier with hook telemetry.
This dashboard can show native-signal freshness, but it cannot count installations
that have or have not configured OTLP export."
' "$health_path" >/dev/null ||
  fail "$health must distinguish native freshness from installation-level OTLP coverage"

jq -e '
  any(.templating.list[];
    .name == "target_version" and .type == "textbox" and .hide == 0 and
    .label == "Target version" and
    .query == "v1.2.0" and
    .current.text == "v1.2.0" and .current.value == "v1.2.0")
' "$health_path" >/dev/null ||
  fail "$health must expose a labeled target_version with default v1.2.0"

jq -e '
  .panels[] | select(.title == "Native metric sample age") |
  .datasource.type == "prometheus" and .datasource.uid == "victoriametrics" and
  (.description | contains("client-provided sample timestamp")) and
  (.description | contains("previous 30 days")) and
  (.description | contains("clock skew or replay")) and
  .fieldConfig.defaults.unit == "s" and
  .options.colorMode == "none" and
  (.targets | length == 2) and
  all(.targets[];
    .datasource.type == "prometheus" and .datasource.uid == "victoriametrics") and
  any(.targets[];
    .legendFormat == "Codex" and
    .expr == "time() - max(tlast_over_time({__name__=~\"codex_.*\",service_name=\"codex_cli_rs\"}[30d]))") and
  any(.targets[];
    .legendFormat == "Claude" and
    .expr == "time() - max(tlast_over_time({__name__=~\"claude_code_.*\",service_name=~\"claude-code|claude-code-desktop\"}[30d]))")
' "$health_path" >/dev/null ||
  fail "$health native sample age must use the approved 30-day namespace queries"

jq -e '
  [
    "Machines not seen on target version",
    "Inactive for more than 24 hours",
    "Inactive for more than 48 hours",
    "Stale installations",
    "Active installations by version",
    "Active installations by harness and OS"
  ] as $titles
  | [.panels[] | select(.title as $title | $titles | index($title)) | .targets[]] as $targets
  | ($targets | all(.expr | contains("agent:!=\"selftest\"")))
' "$health_path" >/dev/null ||
  fail "$health hook populations must exclude selftest"

jq -e '
  (.panels[] | select(.title == "Machines not seen on target version")
    | .targets[0].expr | contains("_time:30d")) and
  (.panels[] | select(.title == "Inactive for more than 24 hours")
    | .targets[0].expr | contains("_time:1d")) and
  (.panels[] | select(.title == "Inactive for more than 48 hours")
    | .targets[0].expr | contains("_time:2d")) and
  (["Active installations by version", "Active installations by harness and OS"] as $titles
    | [.panels[] | select(.title as $title | $titles | index($title))]
    | all(.targets[0].expr | contains("_time:1d")))
' "$health_path" >/dev/null ||
  fail "$health must use the approved 30-day, 24-hour, and 48-hour populations"

jq -e '
  .panels[] | select(.title == "Stale installations") |
  .targets[0].expr as $expr |
  ($expr | contains("first 1 by (_time desc) partition by (machine.id)")) and
  ($expr | split("\n") | any(. == "| fields machine.id, _time, agent, service.version")) and
  (.fieldConfig.defaults.noValue == "Unknown") and
  any(.transformations[]?; .id == "extractFields" and .options.source == "labels") and
  any(.transformations[]?; .id == "convertFieldType"
    and any(.options.conversions[]?;
      .targetField == "_time" and .destinationType == "time")) and
  any(.transformations[]?; .id == "organize"
    and .options.excludeByName ==
      {"Line": true, "labels": true, "detected_level": true, "_time": true}
    and .options.renameByName ==
      {"Time": "Last seen", "machine.id": "Installation", "agent": "Harness",
       "service.version": "Observed version"}
    and .options.indexByName ==
      {"Time": 0, "agent": 1, "machine.id": 2, "service.version": 3})
' "$health_path" >/dev/null ||
  fail "$health stale table must show exactly Last seen, Harness, Installation, and Observed version"

jq -e '
  ["Machines not seen on target version", "Inactive for more than 24 hours",
    "Inactive for more than 48 hours", "Active installations by version",
    "Active installations by harness and OS"] as $titles
  | [.panels[] | select(.title as $title | $titles | index($title))]
  | all(.[]; .fieldConfig.defaults.unit == "locale" and
      .fieldConfig.defaults.decimals == 0 and
      .fieldConfig.defaults.color.mode == "fixed" and
      .fieldConfig.defaults.color.fixedColor == "blue")
' "$health_path" >/dev/null ||
  fail "$health count panels must use whole-number locale units and the neutral blue palette"

jq -e '
  ["Active installations by version", "Active installations by harness and OS"] as $titles
  | [.panels[] | select(.title as $title | $titles | index($title))] as $panels
  | ($panels | length == 2) and
    ($panels | all(.[];
      (.targets | length == 1) and
      (.targets[0].expr | contains("first 1 by (_time desc) partition by (machine.id)")) and
      (.targets[0].expr | endswith("| limit 15"))))
' "$health_path" >/dev/null ||
  fail "$health active distributions must use one latest event per installation and limit 15"

jq -e '
  (.panels[] | select(.title == "Active installations by version")
    | .targets[0].expr | contains("format if (!service.version:*) \"Unknown\" as service.version")) and
  (.panels[] | select(.title == "Active installations by harness and OS")
    | .targets[0].expr as $expr
    | ($expr | contains("format if (!agent:*) \"Unknown\" as agent")) and
      ($expr | contains("format if (!os.type:*) \"Unknown\" as os.type")))
' "$health_path" >/dev/null ||
  fail "$health active distributions must label missing dimensions as Unknown"

# --- Adoption overview (replaces the four per-domain dashboards) ------------
adoption=ai-agent-telemetry-adoption.json
check_common "$adoption" ai-agent-telemetry-adoption victorialogs
check_logs_selector "$adoption"
check_numeric_stats "$adoption" 'Onboarding over time'
check_titles "$adoption" \
  'Active installations per day' 'Active repositories per day' 'Sessions observed per day' \
  'Telemetry activity' 'Onboarding over time' 'Activity per installation' \
  'Top skills executed' 'Top MCPs' 'Machines by harness' 'Machines by OS' \
  'Skills per repository (matrix)' 'Skills per repository (stacked)' 'Skills per repository (table)' \
  'MCPs per repository (matrix)' 'MCPs per repository (stacked)' 'MCPs per repository (table)'

for removed_title in 'Installs' 'Used in repositories' 'Sessions caught' 'Activity per machine'; do
  jq -e --arg title "$removed_title" 'all(.panels[]; .title != $title)' \
    "$dashboard_dir/$adoption" >/dev/null ||
    fail "$adoption still contains removed panel: $removed_title"
done

jq -e '
  [
    "Active installations per day",
    "Active repositories per day",
    "Sessions observed per day"
  ] as $titles
  | [.panels[] | select(.title as $title | $titles | index($title))] as $panels
  | ($panels | length == 3)
    and ([$panels[].title] | sort ==
      ["Active installations per day", "Active repositories per day", "Sessions observed per day"])
    and ($panels | all(.[]; .type == "timeseries"))
    and ($panels | all(.[]; (.targets | length > 0)))
    and ([$panels[] | .targets[]] | all(.queryType == "statsRange"))
    and ([$panels[] | .targets[]] | all(.expr | contains("agent:!=\"selftest\"")))
    and ([$panels[] | .targets[]] | all(.expr | contains("_time:1d offset 0h")))
' "$dashboard_dir/$adoption" >/dev/null ||
  fail "$adoption daily panels must use selftest-free UTC calendar days"

jq -e '
  .panels[] | select(.title == "Active repositories per day") |
  .targets[0].expr as $expr |
  ($expr | contains("repo.remote:*")) and
  ($expr | contains("replace_regexp")) and
  ($expr | contains("git@github[.]com:")) and
  ($expr | contains("ssh://(git@)?github[.]com/")) and
  ($expr | contains("https?://github[.]com/")) and
  ($expr | contains("<lc:repo.remote>")) and
  ($expr | contains("[.]git$")) and
  ($expr | contains("/+$"))
' "$dashboard_dir/$adoption" >/dev/null ||
  fail "$adoption repository count must use the approved normalization"

jq -e '
  [
    "Telemetry activity",
    "Top MCPs",
    "Skills per repository (stacked)",
    "MCPs per repository (stacked)"
  ] as $titles
  | [.panels[] | select(.title as $title | $titles | index($title))] as $panels
  | ($panels | length == 4) and
    ($panels | all(.fieldConfig.defaults.color.mode == "continuous-BlPu"))
' "$dashboard_dir/$adoption" >/dev/null ||
  fail "$adoption count bar charts must use the neutral blue-purple palette"

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
# intentional per-installation ranking, where the installation is the unit of analysis.
jq -e '[.panels[].title, .panels[].fieldConfig.defaults.displayName?]
  | map(select(. != null))
  | all(test("session\\.id|event\\.id") | not)' "$dashboard_dir/$adoption" >/dev/null ||
  fail "$adoption displays a raw session or event identifier"
jq -e '[.panels[] | select(.title != "Activity per installation")
    | .title, .fieldConfig.defaults.displayName?]
  | map(select(. != null))
  | all(test("machine\\.id") | not)' "$dashboard_dir/$adoption" >/dev/null ||
  fail "$adoption exposes a machine identifier outside the per-installation panel"

for title in 'Active installations per day' 'Active repositories per day' 'Sessions observed per day' \
  'Machines by harness' 'Machines by OS'; do
  check_selftest_excluded "$adoption" "$title"
done

# --- Native agent metrics overview -----------------------------------------
overview=native-agent-metrics-overview.json
check_common "$overview" native-agent-metrics-overview victoriametrics
check_titles "$overview" \
  'Signal availability' 'Native metric sample age' \
  'Average top-level sessions per hour' 'Average tokens processed per hour' \
  'Tokens by model and type' 'Observed client versions'

overview_path=$dashboard_dir/$overview
for removed_title in 'Top-level sessions started' 'Token usage over time'; do
  jq -e --arg title "$removed_title" 'all(.panels[]; .title != $title)' "$overview_path" >/dev/null ||
    fail "$overview still contains removed panel: $removed_title"
done

jq -e '
  .panels[] | select(.title == "Signal availability") |
  .options.mode == "markdown" and
  (.options.content | contains("| Harness | Native metrics | Hook telemetry |")) and
  (.options.content | contains("\n| --- | --- | --- |")) and
  (.options.content | contains("\n| Codex | Supported | Supported |")) and
  (.options.content | contains("\n| Claude Code | Supported | Supported |")) and
  (.options.content | contains("\\n") | not)
' "$overview_path" >/dev/null ||
  fail "$overview Signal availability must use Markdown table rows with real line breaks"

jq -e '
  (.panels[] | select(.title == "Signal availability")
    | .gridPos == {"h": 8, "w": 24, "x": 0, "y": 0}) and
  (.panels[] | select(.title == "Native metric sample age")
    | .gridPos == {"h": 7, "w": 12, "x": 0, "y": 8}) and
  (.panels[] | select(.title == "Average top-level sessions per hour")
    | .gridPos == {"h": 7, "w": 12, "x": 12, "y": 8}) and
  (.panels[] | select(.title == "Average tokens processed per hour")
    | .gridPos == {"h": 8, "w": 24, "x": 0, "y": 15}) and
  (.panels[] | select(.title == "Tokens by model and type")
    | .gridPos == {"h": 8, "w": 12, "x": 0, "y": 23}) and
  (.panels[] | select(.title == "Observed client versions")
    | .gridPos == {"h": 8, "w": 12, "x": 12, "y": 23})
' "$overview_path" >/dev/null ||
  fail "$overview must reserve enough space for Signal availability without overlapping later rows"

jq -e '
  any(.panels[].targets[]?; .expr | contains("service_name=\"codex_cli_rs\"")) and
  any(.panels[].targets[]?; .expr | contains("service_name=~\"claude-code|claude-code-desktop\"")) and
  all(.panels[].targets[]?; (.expr | contains("agent_harness")) | not)
' "$overview_path" >/dev/null || fail "$overview must classify Codex and Claude without agent_harness"

jq -e '
  .panels[] | select(.title == "Native metric sample age") |
  (.description | contains("client-provided sample timestamp")) and
  (.description | contains("previous 30 days")) and
  (.description | contains("clock skew or replay")) and
  .fieldConfig.defaults.unit == "s" and
  (.targets | length == 2) and
  any(.targets[];
    .legendFormat == "Codex" and
    .expr == "time() - max(tlast_over_time({__name__=~\"codex_.*\",service_name=\"codex_cli_rs\"}[30d]))") and
  any(.targets[];
    .legendFormat == "Claude" and
    .expr == "time() - max(tlast_over_time({__name__=~\"claude_code_.*\",service_name=~\"claude-code|claude-code-desktop\"}[30d]))")
' "$overview_path" >/dev/null ||
  fail "$overview native sample age must use the approved 30-day namespace queries"

jq -e '
  ["Average top-level sessions per hour", "Average tokens processed per hour"] as $titles
  | [.panels[] | select(.title as $title | $titles | index($title))] as $panels
  | ($panels | length == 2)
    and ($panels | all(.[]; .type == "timeseries"))
    and ($panels | all(.[]; (.targets | length > 0)))
    and ($panels | all(.[]; all(.targets[];
      .interval == "1h" and
      (.expr | contains("[$__interval]")) and
      (.expr | contains("* 3600000 / $__interval_ms")) and
      ((.expr | contains("[1h]")) | not))))
    and ($panels | all(.[]; .fieldConfig.defaults.custom.showPoints == "always"))
    and ($panels | all(.[]; .fieldConfig.defaults.custom.pointSize == 5))
    and ($panels | all(.[]; .fieldConfig.defaults.custom.spanNulls == false))
' "$overview_path" >/dev/null ||
  fail "$overview hourly-average panels must cover the complete query interval"

jq -e '
  .panels[] | select(.title == "Average tokens processed per hour") |
  .fieldConfig.defaults.unit == "locale" and .fieldConfig.defaults.decimals == 0
' "$overview_path" >/dev/null ||
  fail "$overview hourly tokens must render exact whole numbers"

jq -e '
  .panels[] | select(.title == "Tokens by model and type") |
  .type == "table" and (.targets | length == 1) and
  .targets[0].instant == true and .targets[0].format == "time_series" and
  any(.transformations[]?; .id == "joinByLabels"
    and .options.join == ["agent_model"] and .options.value == "token_type") and
  any(.fieldConfig.overrides[]?;
    .matcher.id == "byType" and .matcher.options == "number" and
    any(.properties[]?; .id == "unit" and .value == "locale") and
    any(.properties[]?; .id == "decimals" and .value == 0) and
    any(.properties[]?; .id == "color" and .value.mode == "continuous-BlPu"))
' "$overview_path" >/dev/null ||
  fail "$overview token breakdown must be one exact-count target with a neutral matrix palette"

jq -e '
  .panels[] | select(.title == "Observed client versions") |
  .type == "table" and (.targets | length == 1) and
  .targets[0].instant == true and .targets[0].format == "table" and
  any(.transformations[]?; .id == "organize"
    and .options.excludeByName.Time == true
    and .options.excludeByName.Value == true
    and .options.excludeByName.__name__ == true
    and .options.renameByName.harness == "Harness"
    and .options.renameByName.app_version == "Version"
    and .options.indexByName.harness == 0
    and .options.indexByName.app_version == 1)
' "$overview_path" >/dev/null ||
  fail "$overview client versions must show Harness then Version and hide metric metadata"

# --- Codex native metrics deep-dive ----------------------------------------
codex='codex-native-metrics.json'
check_common "$codex" codex-native-metrics victoriametrics
check_titles "$codex" \
  'Average sessions and turns per hour' 'Average tool and MCP calls per hour' \
  'Tool failure ratio per query interval' 'Average tokens processed per hour' \
  'Top tools' 'MCP servers and outcomes' \
  'Tokens by model and type' 'Turn latency' 'Tool latency' 'API latency' 'Skill injections'

codex_path=$dashboard_dir/$codex
codex_panel_count=$(jq '[.panels[] | select(.type != "text" and .type != "row")] | length' "$codex_path")
[ "$codex_panel_count" -eq 11 ] || fail "$codex must contain exactly 11 query panels"

jq -e '
  [
    "Average sessions and turns per hour",
    "Average tool and MCP calls per hour",
    "Tool failure ratio per query interval",
    "Average tokens processed per hour"
  ] as $titles
  | [.panels[] | select(.title as $title | $titles | index($title))] as $panels
  | ($panels | length == 4)
    and ($panels | all(.type == "timeseries"))
    and ($panels | all(.[]; (.targets | length > 0)))
    and ($panels | all(.[]; all(.targets[];
      .interval == "1h" and
      (.expr | contains("[$__interval]")) and
      ((.expr | contains("[1h]")) | not))))
    and ($panels | all(.[]; .fieldConfig.defaults.custom.showPoints == "always"))
    and ($panels | all(.[]; .fieldConfig.defaults.custom.pointSize == 5))
    and ($panels | all(.[]; .fieldConfig.defaults.custom.spanNulls == false))
' "$codex_path" >/dev/null ||
  fail "$codex activity panels must cover the complete query interval"

jq -e '
  [
    "Average sessions and turns per hour",
    "Average tool and MCP calls per hour",
    "Average tokens processed per hour"
  ] as $titles
  | [.panels[] | select(.title as $title | $titles | index($title)) | .targets[]]
  | all(.expr | contains("* 3600000 / $__interval_ms"))
' "$codex_path" >/dev/null ||
  fail "$codex count panels must normalize each complete interval to an hourly average"

jq -e '
  .panels[] | select(.title == "Tool failure ratio per query interval") |
  (.targets | length == 1) and
  (.targets[0].expr | contains("[$__interval]")) and
  ((.targets[0].expr | contains("* 3600000 / $__interval_ms")) | not)
' "$codex_path" >/dev/null ||
  fail "$codex failure ratio must use the complete query interval without count normalization"

jq -e '
  .panels[] | select(.title == "Tokens by model and type") |
  .type == "table" and (.targets | length == 1) and
  any(.transformations[]?; .id == "joinByLabels"
    and .options.join == ["model"] and .options.value == "token_type") and
  any(.fieldConfig.overrides[]?;
    .matcher.id == "byType" and .matcher.options == "number" and
    any(.properties[]?; .id == "color" and .value.mode == "continuous-BlPu"))
' "$codex_path" >/dev/null ||
  fail "$codex tokens must use one model/token-type target with a neutral matrix palette"

jq -e '
  ["Top tools", "MCP servers and outcomes", "Skill injections"] as $titles
  | [.panels[] | select(.title as $title | $titles | index($title))] as $panels
  | ($panels | length == 3) and
    ($panels | all(.fieldConfig.defaults.color.mode == "continuous-BlPu")) and
    ($panels | all(.[]; (.targets | length == 1) and .targets[0].instant == true))
' "$codex_path" >/dev/null ||
  fail "$codex topk bar gauges must use instant queries and a neutral blue-purple palette"

jq -e '
  .panels[] | select(.title == "API latency") |
  (.targets | length == 6) and
  ([.targets[].legendFormat] | sort ==
    ["Inference p50", "Inference p95", "TBT p50", "TBT p95", "TTFT p50", "TTFT p95"])
' "$codex_path" >/dev/null ||
  fail "$codex API latency must expose p50 and p95 for TTFT, TBT, and inference"

jq -e '
  [.panels[] | select(.type != "text" and .type != "row") | .targets[]?] as $targets
  | ($targets | all(.expr | contains("service_name=\"codex_cli_rs\""))) and
    ($targets | all((.expr | contains("agent_harness")) | not))
' "$codex_path" >/dev/null || fail "$codex must select Codex by service_name without agent_harness"

jq -e '
  [.panels[] | select(.type != "text" and .type != "row") | .targets[]?.expr] as $expressions
  | ($expressions | all(
      gsub("session_source[[:space:]]*=[[:space:]]*\"cli\""; "")
      | test("session_source"; "i") | not
    )) and
    ($expressions | all(
      test("(^|[^[:alnum:]_])(by|without)[[:space:]]*\\([^)]*session_source"; "i") | not
    ))
' "$codex_path" >/dev/null ||
  fail "$codex must not group or break down metrics by session_source"

jq -e '
  [
    .panels[] | [
      .title?,
      .fieldConfig.defaults.displayName?,
      .fieldConfig.defaults.displayNameFromDS?,
      .targets[]?.legendFormat?,
      .fieldConfig.overrides[]?.matcher.options?,
      (.fieldConfig.overrides[]?.properties[]? | select(.id == "displayName") | .value?)
    ][] | select(type == "string")
  ]
  | all(test("session_source"; "i") | not)
' "$codex_path" >/dev/null ||
  fail "$codex must not display session_source in titles, legends, or display names"

for title in 'Turn latency' 'Tool latency' 'API latency'; do
  jq -e --arg title "$title" '
    .panels[] | select(.title == $title) |
    .fieldConfig.defaults.unit == "ms" and
    (.description | contains("Histogram quantiles require enough observations in the selected rate interval."))
  ' "$codex_path" >/dev/null || fail "$codex panel '$title' must use milliseconds and explain quantile requirements"
done

jq -e '
  .panels[] | select(.title == "Tool failure ratio per query interval") |
  .fieldConfig.defaults.unit == "percentunit"
' "$codex_path" >/dev/null || fail "$codex failure ratio must use percentunit"

jq -e '
  .links | length == 3 and
  any(.[]; .title == "Native agent metrics overview" and .url == "/grafana/d/native-agent-metrics-overview") and
  any(.[]; .title == "Adoption overview" and .url == "/grafana/d/ai-agent-telemetry-adoption") and
  any(.[]; .title == "Telemetry health" and .url == "/grafana/d/ai-agent-health")
' "$codex_path" >/dev/null || fail "$codex must link to the overview and telemetry dashboards"

check_codex_target() {
  title=$1
  ref_id=$2
  expr=$3
  jq -e --arg title "$title" --arg ref_id "$ref_id" --arg expr "$expr" '
    .panels[] | select(.title == $title) |
    any(.targets[]?; .refId == $ref_id and .expr == $expr)
  ' "$codex_path" >/dev/null || fail "$codex panel '$title' has an unexpected target $ref_id"
}

check_codex_legend() {
  title=$1
  ref_id=$2
  legend=$3
  jq -e --arg title "$title" --arg ref_id "$ref_id" --arg legend "$legend" '
    .panels[] | select(.title == $title) |
    any(.targets[]?; .refId == $ref_id and .legendFormat == $legend)
  ' "$codex_path" >/dev/null || fail "$codex panel '$title' must use legend '$legend' for $ref_id"
}

check_codex_target 'Average sessions and turns per hour' A 'sum(increase(codex_thread_started_total{service_name="codex_cli_rs",session_source="cli"}[$__interval])) * 3600000 / $__interval_ms'
check_codex_target 'Average sessions and turns per hour' B 'sum(increase(codex_conversation_turn_count_total{service_name="codex_cli_rs"}[$__interval])) * 3600000 / $__interval_ms'
check_codex_target 'Average tool and MCP calls per hour' A 'sum(increase(codex_tool_call_total{service_name="codex_cli_rs"}[$__interval])) * 3600000 / $__interval_ms'
check_codex_target 'Average tool and MCP calls per hour' B 'sum(increase(codex_mcp_call_total{service_name="codex_cli_rs"}[$__interval])) * 3600000 / $__interval_ms'
check_codex_target 'Tool failure ratio per query interval' A '(sum(increase(codex_tool_call_total{service_name="codex_cli_rs",success="false"}[$__interval])) or (0 * sum(increase(codex_tool_call_total{service_name="codex_cli_rs"}[$__interval])))) / sum(increase(codex_tool_call_total{service_name="codex_cli_rs"}[$__interval]))'
check_codex_target 'Average tokens processed per hour' A 'sum by (token_type) (increase(codex_turn_token_usage_sum{service_name="codex_cli_rs",token_type!="total"}[$__interval])) * 3600000 / $__interval_ms'
check_codex_target 'Top tools' A 'topk(10, sum by (tool) (increase(codex_tool_call_total{service_name="codex_cli_rs"}[$__range])))'
check_codex_target 'MCP servers and outcomes' A 'topk(10, sum by (server, status) (increase(codex_mcp_call_total{service_name="codex_cli_rs"}[$__range])))'
check_codex_target 'Tokens by model and type' A 'sum by (model, token_type) (increase(codex_turn_token_usage_sum{service_name="codex_cli_rs",token_type!="total"}[$__range]))'
check_codex_target 'Skill injections' A 'topk(15, sum by (skill, invoke_type, status) (increase(codex_skill_injected_total{service_name="codex_cli_rs"}[$__range])))'
check_codex_target 'Turn latency' A 'histogram_quantile(0.50, sum by (le) (rate(codex_turn_e2e_duration_ms_milliseconds_bucket{service_name="codex_cli_rs"}[$__rate_interval])))'
check_codex_target 'Turn latency' B 'histogram_quantile(0.95, sum by (le) (rate(codex_turn_e2e_duration_ms_milliseconds_bucket{service_name="codex_cli_rs"}[$__rate_interval])))'
check_codex_target 'Tool latency' A 'histogram_quantile(0.50, sum by (le) (rate(codex_tool_call_duration_ms_milliseconds_bucket{service_name="codex_cli_rs"}[$__rate_interval])))'
check_codex_target 'Tool latency' B 'histogram_quantile(0.95, sum by (le) (rate(codex_tool_call_duration_ms_milliseconds_bucket{service_name="codex_cli_rs"}[$__rate_interval])))'
check_codex_target 'API latency' A 'histogram_quantile(0.50, sum by (le) (rate(codex_responses_api_engine_service_ttft_duration_ms_milliseconds_bucket{service_name="codex_cli_rs"}[$__rate_interval])))'
check_codex_target 'API latency' B 'histogram_quantile(0.95, sum by (le) (rate(codex_responses_api_engine_service_ttft_duration_ms_milliseconds_bucket{service_name="codex_cli_rs"}[$__rate_interval])))'
check_codex_target 'API latency' C 'histogram_quantile(0.50, sum by (le) (rate(codex_responses_api_engine_service_tbt_duration_ms_milliseconds_bucket{service_name="codex_cli_rs"}[$__rate_interval])))'
check_codex_target 'API latency' D 'histogram_quantile(0.95, sum by (le) (rate(codex_responses_api_engine_service_tbt_duration_ms_milliseconds_bucket{service_name="codex_cli_rs"}[$__rate_interval])))'
check_codex_target 'API latency' E 'histogram_quantile(0.50, sum by (le) (rate(codex_responses_api_inference_time_duration_ms_milliseconds_bucket{service_name="codex_cli_rs"}[$__rate_interval])))'
check_codex_target 'API latency' F 'histogram_quantile(0.95, sum by (le) (rate(codex_responses_api_inference_time_duration_ms_milliseconds_bucket{service_name="codex_cli_rs"}[$__rate_interval])))'

check_codex_legend 'Average sessions and turns per hour' A Sessions
check_codex_legend 'Average sessions and turns per hour' B Turns
check_codex_legend 'Average tool and MCP calls per hour' A 'Tool calls'
check_codex_legend 'Average tool and MCP calls per hour' B 'MCP calls'
check_codex_legend 'Top tools' A '{{tool}}'
check_codex_legend 'MCP servers and outcomes' A '{{server}} · {{status}}'
check_codex_legend 'Average tokens processed per hour' A '{{token_type}}'
check_codex_legend 'Turn latency' A p50
check_codex_legend 'Turn latency' B p95
check_codex_legend 'Tool latency' A p50
check_codex_legend 'Tool latency' B p95
check_codex_legend 'API latency' A 'TTFT p50'
check_codex_legend 'API latency' B 'TTFT p95'
check_codex_legend 'API latency' C 'TBT p50'
check_codex_legend 'API latency' D 'TBT p95'
check_codex_legend 'API latency' E 'Inference p50'
check_codex_legend 'API latency' F 'Inference p95'
check_codex_legend 'Skill injections' A '{{skill}} · {{invoke_type}} · {{status}}'

printf 'PASS: Grafana dashboard contract\n'
