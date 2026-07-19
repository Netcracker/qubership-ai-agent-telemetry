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
  jq -e '[.panels[].title, .panels[].fieldConfig.defaults.displayName?]
    | map(select(. != null))
    | all(test("machine\\.id|session\\.id|event\\.id") | not)' "$path" >/dev/null ||
    fail "$file displays a raw identifier"
  for title in "$@"; do
    jq -e --arg title "$title" 'any(.panels[]; .title == $title)' "$path" >/dev/null ||
      fail "$file is missing panel: $title"
  done
}

check_dashboard executive-overview.json ai-agent-executive \
  'Active installs' 'Active repositories' 'Active sessions' 'Installs using skills' 'Adoption trend' \
  'Top skills' 'Top MCP tools' 'Repository adoption' 'Data semantics'
check_dashboard skill-adoption.json ai-agent-skills \
  'Active installs using skills' 'Active repositories using skills' 'Observed skills' 'Skill events' \
  'Skill adoption trend' 'Top skills by reach and frequency' 'Adoption concentration' \
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

printf 'PASS: Grafana dashboard contract\n'
