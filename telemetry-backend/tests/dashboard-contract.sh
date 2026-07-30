#!/bin/sh
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

# --- Codex native metrics deep-dive ----------------------------------------
codex=codex-native-metrics.json
check_common "$codex" codex-native-metrics victoriametrics
check_titles "$codex" \
  'Top-level sessions' 'Conversation turns' 'Tool calls' 'MCP calls' 'Total tokens' \
  'Tool failure ratio' 'Tool and MCP activity' 'Top tools' 'MCP servers and outcomes' \
  'Tokens by model and type' 'Turn latency' 'Tool latency' 'API latency' 'Skill injections'

codex_path=$dashboard_dir/$codex
codex_panel_count=$(jq '[.panels[] | select(.type != "text" and .type != "row")] | length' "$codex_path")
[ "$codex_panel_count" -eq 14 ] || fail "$codex must contain exactly 14 query panels"
codex_target_count=$(jq '[.panels[] | select(.type != "text" and .type != "row") | .targets[]?] | length' "$codex_path")
[ "$codex_target_count" -eq 19 ] || fail "$codex must contain exactly 19 approved targets"

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
  .panels[] | select(.title == "Tool failure ratio") |
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

check_codex_no_legend() {
  title=$1
  ref_id=$2
  jq -e --arg title "$title" --arg ref_id "$ref_id" '
    .panels[] | select(.title == $title) |
    any(.targets[]?; .refId == $ref_id and (.legendFormat? == null))
  ' "$codex_path" >/dev/null || fail "$codex panel '$title' must not set a legend for $ref_id"
}

for title in 'Top-level sessions' 'Conversation turns' 'Tool calls' 'MCP calls' 'Total tokens'; do
  check_neutral_stat "$codex" "$title"
done
for title in 'Top-level sessions' 'Conversation turns' 'Tool calls' 'MCP calls' 'Total tokens' 'Tool failure ratio'; do
  check_codex_no_legend "$title" A
done

check_codex_target 'Top-level sessions' A 'sum(increase(codex_thread_started_total{service_name="codex_cli_rs",session_source="cli"}[$__range]))'
check_codex_target 'Conversation turns' A 'sum(increase(codex_conversation_turn_count_total{service_name="codex_cli_rs",session_source="cli"}[$__range]))'
check_codex_target 'Tool calls' A 'sum(increase(codex_tool_call_total{service_name="codex_cli_rs"}[$__range]))'
check_codex_target 'MCP calls' A 'sum(increase(codex_mcp_call_total{service_name="codex_cli_rs"}[$__range]))'
check_codex_target 'Total tokens' A 'sum(increase(codex_turn_token_usage_sum{service_name="codex_cli_rs",token_type="total"}[$__range]))'
check_codex_target 'Tool failure ratio' A '(sum(increase(codex_tool_call_total{service_name="codex_cli_rs",success="false"}[$__range])) or vector(0)) / clamp_min(sum(increase(codex_tool_call_total{service_name="codex_cli_rs"}[$__range])), 1)'
check_codex_target 'Tool and MCP activity' A 'sum(rate(codex_tool_call_total{service_name="codex_cli_rs"}[$__rate_interval]))'
check_codex_target 'Tool and MCP activity' B 'sum(rate(codex_mcp_call_total{service_name="codex_cli_rs"}[$__rate_interval]))'
check_codex_target 'Top tools' A 'topk(10, sum by (tool) (increase(codex_tool_call_total{service_name="codex_cli_rs"}[$__range])))'
check_codex_target 'MCP servers and outcomes' A 'topk(10, sum by (server, status) (increase(codex_mcp_call_total{service_name="codex_cli_rs"}[$__range])))'
check_codex_target 'Tokens by model and type' A 'sum by (model, token_type) (increase(codex_turn_token_usage_sum{service_name="codex_cli_rs",token_type!="total"}[$__range]))'
check_codex_target 'Skill injections' A 'topk(15, sum by (skill, invoke_type, status) (increase(codex_skill_injected_total{service_name="codex_cli_rs"}[$__range])))'
check_codex_target 'Turn latency' A 'histogram_quantile(0.50, sum by (le) (rate(codex_turn_e2e_duration_ms_milliseconds_bucket{service_name="codex_cli_rs"}[$__rate_interval])))'
check_codex_target 'Turn latency' B 'histogram_quantile(0.95, sum by (le) (rate(codex_turn_e2e_duration_ms_milliseconds_bucket{service_name="codex_cli_rs"}[$__rate_interval])))'
check_codex_target 'Tool latency' A 'histogram_quantile(0.50, sum by (le) (rate(codex_tool_call_duration_ms_milliseconds_bucket{service_name="codex_cli_rs"}[$__rate_interval])))'
check_codex_target 'Tool latency' B 'histogram_quantile(0.95, sum by (le) (rate(codex_tool_call_duration_ms_milliseconds_bucket{service_name="codex_cli_rs"}[$__rate_interval])))'
check_codex_target 'API latency' A 'histogram_quantile(0.95, sum by (le) (rate(codex_responses_api_engine_service_ttft_duration_ms_milliseconds_bucket{service_name="codex_cli_rs"}[$__rate_interval])))'
check_codex_target 'API latency' B 'histogram_quantile(0.95, sum by (le) (rate(codex_responses_api_engine_service_tbt_duration_ms_milliseconds_bucket{service_name="codex_cli_rs"}[$__rate_interval])))'
check_codex_target 'API latency' C 'histogram_quantile(0.95, sum by (le) (rate(codex_responses_api_inference_time_duration_ms_milliseconds_bucket{service_name="codex_cli_rs"}[$__rate_interval])))'

check_codex_legend 'Tool and MCP activity' A 'Tool calls'
check_codex_legend 'Tool and MCP activity' B 'MCP calls'
check_codex_legend 'Top tools' A '{{tool}}'
check_codex_legend 'MCP servers and outcomes' A '{{server}} · {{status}}'
check_codex_legend 'Tokens by model and type' A '{{model}} · {{token_type}}'
check_codex_legend 'Turn latency' A p50
check_codex_legend 'Turn latency' B p95
check_codex_legend 'Tool latency' A p50
check_codex_legend 'Tool latency' B p95
check_codex_legend 'API latency' A 'TTFT p95'
check_codex_legend 'API latency' B 'TBT p95'
check_codex_legend 'API latency' C 'Inference p95'
check_codex_legend 'Skill injections' A '{{skill}} · {{invoke_type}} · {{status}}'

printf 'PASS: Grafana dashboard contract\n'
