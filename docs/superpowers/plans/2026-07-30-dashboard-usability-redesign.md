# Dashboard usability redesign implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make all four provisioned telemetry dashboards readable and operationally useful while preserving the
approved hook and native-metrics data contracts.

**Architecture:** Keep hook-derived adoption and health queries in VictoriaLogs and native agent metrics in
VictoriaMetrics. Encode aggregation windows, populations, zero/no-data behavior, and table transformations directly in
the provisioned dashboard JSON, then protect those decisions with shell and `jq` contracts.

**Tech stack:** Grafana dashboard JSON, Grafana transformations, LogsQL, MetricsQL/PromQL, VictoriaLogs,
VictoriaMetrics, POSIX shell, `jq`, `curl`, Docker Compose, and Chrome DevTools MCP.

## Global constraints

- Follow `docs/superpowers/specs/2026-07-30-dashboard-usability-redesign.md`.
- Use the provisioned datasource UIDs `victorialogs` and `victoriametrics`.
- Remap `victoriametrics` to `victoriametrics-local` only in memory during local import.
- Do not create, edit, or delete a datasource in `http://localhost:13000`.
- Exclude `agent="selftest"` from every hook-derived adoption and health population.
- Use UTC calendar days for daily adoption panels.
- Use fixed trailing one-hour windows and a minimum one-hour interval for hourly native-metrics panels.
- Keep token-type values source-native; normalize only Claude's label key from `type` to `token_type`.
- Display exact token counts with locale digit grouping.
- Preserve the skill and MCP matrix, graph, and table representations in Adoption overview.
- Treat native-metric freshness as signal freshness, not installation-level OTLP coverage.
- Keep queries compatible with the deployed VictoriaLogs version.
- Use American English and keep Markdown body lines within 120 characters.

---

### Task 1: Redesign the native agent metrics overview

**Files:**

- Modify: `telemetry-backend/tests/dashboard-contract.sh`
- Modify: `telemetry-backend/grafana/dashboards/native-agent-metrics-overview.json`

**Interfaces:**

- Consumes: Codex metrics selected by `service_name="codex_cli_rs"`.
- Consumes: Claude metrics selected by `service_name=~"claude-code|claude-code-desktop"`.
- Produces: hourly session and token series, a cross-harness token matrix, and a compact version table.

- [ ] **Step 1: Replace the overview assertions with the new failing contract**

Update the overview section of `dashboard-contract.sh` to require the new titles and reject the old ones:

```sh
check_titles "$overview" \
  'Signal availability' 'Metrics freshness' 'Top-level sessions per hour' \
  'Tokens processed per hour' 'Tokens by model and type' 'Observed client versions'

for removed_title in 'Top-level sessions started' 'Token usage over time'; do
  jq -e --arg title "$removed_title" 'all(.panels[]; .title != $title)' "$overview_path" >/dev/null ||
    fail "$overview still contains removed panel: $removed_title"
done
```

Add these structural assertions:

```sh
jq -e '
  ["Top-level sessions per hour", "Tokens processed per hour"] as $titles
  | [.panels[] | select(.title as $title | $titles | index($title))] as $panels
  | ($panels | length == 2)
    and ($panels | all(.type == "timeseries"))
    and ($panels | all(.targets | length > 0))
    and ($panels | all(.targets[]; .interval == "1h" and (.expr | contains("[1h]"))))
' "$overview_path" >/dev/null ||
  fail "$overview hourly panels must use one-hour windows and intervals"

jq -e '
  .panels[] | select(.title == "Tokens processed per hour") |
  .fieldConfig.defaults.unit == "locale" and .fieldConfig.defaults.decimals == 0
' "$overview_path" >/dev/null ||
  fail "$overview hourly tokens must render exact whole numbers"

jq -e '
  .panels[] | select(.title == "Tokens by model and type") |
  .type == "table" and
  .targets[0].instant == true and .targets[0].format == "time_series" and
  any(.transformations[]?; .id == "joinByLabels"
    and .options.join == ["agent_model"] and .options.value == "token_type") and
  any(.fieldConfig.overrides[]?;
    .matcher.id == "byType" and .matcher.options == "number" and
    any(.properties[]?; .id == "unit" and .value == "locale") and
    any(.properties[]?; .id == "decimals" and .value == 0))
' "$overview_path" >/dev/null ||
  fail "$overview token breakdown must be an exact-count harness/model matrix"

jq -e '
  .panels[] | select(.title == "Observed client versions") |
  .type == "table" and (.targets | length == 1) and
  .targets[0].instant == true and .targets[0].format == "table" and
  any(.transformations[]?; .id == "organize"
    and .options.excludeByName.Time == true
    and .options.excludeByName.Value == true
    and .options.renameByName.harness == "Harness"
    and .options.renameByName.app_version == "Version")
' "$overview_path" >/dev/null ||
  fail "$overview client versions must show only harness and version"
```

- [ ] **Step 2: Run the dashboard contract and verify that it fails**

Run:

```sh
sh telemetry-backend/tests/dashboard-contract.sh
```

Expected: FAIL because the overview still contains the old titles, rate-based token query, bar gauge, and range table.

- [ ] **Step 3: Implement the hourly overview panels**

Replace the old session stat with a time series named `Top-level sessions per hour`. Use these targets with
`interval: "1h"`:

```promql
sum(increase(codex_thread_started_total{
  service_name="codex_cli_rs",
  session_source="cli"
}[1h]))
```

```promql
sum(increase(claude_code_session_count_total{
  service_name=~"claude-code|claude-code-desktop",
  start_type!="agents_view"
}[1h]))
```

Use legends `Codex` and `Claude`. Set the unit to `locale`, decimals to `0`, and describe each point as the sessions
observed during the trailing hour.

Replace the rate-based token panel with `Tokens processed per hour`. Use these targets with `interval: "1h"`:

```promql
sum by (token_type) (
  increase(codex_turn_token_usage_sum{
    service_name="codex_cli_rs",
    token_type!="total"
  }[1h])
)
```

```promql
sum by (type) (
  increase(claude_code_token_usage_tokens_total{
    service_name=~"claude-code|claude-code-desktop"
  }[1h])
)
```

Use legends `Codex · {{token_type}}` and `Claude · {{type}}`. Set unit `locale`, decimals `0`, and explain the trailing
one-hour window in the description.

- [ ] **Step 4: Implement the cross-harness token matrix**

Change `Tokens by model and type` to a table with an instant time-series target for this MetricsQL expression:

```promql
label_join(
  label_set(
    sum by (model, token_type) (
      increase(codex_turn_token_usage_sum{
        service_name="codex_cli_rs",
        token_type!="total"
      }[$__range])
    ),
    "harness", "Codex"
  ),
  "agent_model", " · ", "harness", "model"
)
or
label_join(
  label_set(
    label_move(
      sum by (model, type) (
        increase(claude_code_token_usage_tokens_total{
          service_name=~"claude-code|claude-code-desktop"
        }[$__range])
      ),
      "type", "token_type"
    ),
    "harness", "Claude"
  ),
  "agent_model", " · ", "harness", "model"
)
```

Set `instant: true`, `format: "time_series"`, and use:

```json
[
  {
    "id": "joinByLabels",
    "options": {
      "join": ["agent_model"],
      "value": "token_type"
    }
  },
  {
    "id": "organize",
    "options": {
      "renameByName": {
        "agent_model": "Harness · Model"
      }
    }
  }
]
```

Apply numeric-field overrides with unit `locale`, decimals `0`, centered cells, and a color-background cell mode.

- [ ] **Step 5: Implement the compact version table**

Use one instant table target:

```promql
label_set(
  group by (app_version) (
    max_over_time(codex_tool_call_total{service_name="codex_cli_rs"}[$__range])
  ),
  "harness", "Codex"
)
or
label_set(
  group by (app_version) (
    max_over_time(claude_code_session_count_total{
      service_name=~"claude-code|claude-code-desktop"
    }[$__range])
  ),
  "harness", "Claude"
)
```

Add an `organize` transformation that excludes `Time`, `Value`, and `__name__`; renames `harness` to `Harness` and
`app_version` to `Version`; and orders `Harness` before `Version`.

- [ ] **Step 6: Run the focused contract**

Run:

```sh
jq empty telemetry-backend/grafana/dashboards/native-agent-metrics-overview.json
sh telemetry-backend/tests/dashboard-contract.sh
```

Expected: `PASS: Grafana dashboard contract`.

- [ ] **Step 7: Commit the overview redesign**

```bash
git add telemetry-backend/tests/dashboard-contract.sh \
  telemetry-backend/grafana/dashboards/native-agent-metrics-overview.json
git commit -m "feat(telemetry): clarify native metrics overview"
```

---

### Task 2: Redesign the Codex native metrics dashboard

**Files:**

- Modify: `telemetry-backend/tests/dashboard-contract.sh`
- Modify: `telemetry-backend/tests/metrics-query-contract.sh`
- Modify: `telemetry-backend/grafana/dashboards/codex-native-metrics.json`

**Interfaces:**

- Consumes: Codex counters and histograms already validated by the native metric fixture.
- Produces: four hourly activity panels, a model/token matrix, and explicit p50/p95 latency series.
- Produces: deterministic failure-ratio checks for zero and no-data states.

- [ ] **Step 1: Add failing Codex dashboard assertions**

Replace the old title and panel-count assertions with:

```sh
check_titles "$codex" \
  'Sessions and turns per hour' 'Tool and MCP calls per hour' \
  'Tool failure ratio per hour' 'Tokens processed per hour' \
  'Top tools' 'MCP servers and outcomes' 'Tokens by model and type' \
  'Turn latency' 'Tool latency' 'API latency' 'Skill injections'

codex_panel_count=$(jq '[.panels[] | select(.type != "text" and .type != "row")] | length' "$codex_path")
[ "$codex_panel_count" -eq 11 ] || fail "$codex must contain exactly 11 query panels"
```

Require one-hour activity semantics:

```sh
jq -e '
  [
    "Sessions and turns per hour",
    "Tool and MCP calls per hour",
    "Tool failure ratio per hour",
    "Tokens processed per hour"
  ] as $titles
  | [.panels[] | select(.title as $title | $titles | index($title))] as $panels
  | ($panels | length == 4)
    and ($panels | all(.type == "timeseries"))
    and ($panels | all(.targets[]; .interval == "1h" and (.expr | contains("[1h]"))))
' "$codex_path" >/dev/null ||
  fail "$codex activity panels must use one-hour windows and intervals"
```

Require matrix and latency contracts:

```sh
jq -e '
  .panels[] | select(.title == "Tokens by model and type") |
  .type == "table" and
  any(.transformations[]?; .id == "joinByLabels"
    and .options.join == ["model"] and .options.value == "token_type")
' "$codex_path" >/dev/null ||
  fail "$codex tokens must use a model/token-type matrix"

jq -e '
  .panels[] | select(.title == "API latency") |
  (.targets | length == 6) and
  ([.targets[].legendFormat] | sort ==
    ["Inference p50", "Inference p95", "TBT p50", "TBT p95", "TTFT p50", "TTFT p95"])
' "$codex_path" >/dev/null ||
  fail "$codex API latency must expose p50 and p95 for TTFT, TBT, and inference"
```

- [ ] **Step 2: Add failing zero/no-data metric checks**

In `metrics-query-contract.sh`, add:

```sh
successful_only_ratio='
  (
    sum(increase(codex_tool_call_total{
      service_name="codex_cli_rs",
      success="false"
    }[1h]))
    or
    (0 * sum(increase(codex_tool_call_total{
      service_name="codex_cli_rs"
    }[1h])))
  )
  /
  sum(increase(codex_tool_call_total{
    service_name="codex_cli_rs"
  }[1h]))
'
no_calls_ratio='
  (
    sum(increase(codex_tool_call_total{
      service_name="codex-no-calls",
      success="false"
    }[1h]))
    or
    (0 * sum(increase(codex_tool_call_total{
      service_name="codex-no-calls"
    }[1h])))
  )
  /
  sum(increase(codex_tool_call_total{
    service_name="codex-no-calls"
  }[1h]))
'

zero_ratio=$(query_metric "$successful_only_ratio" |
  jq -r '.data.result[0].value[1] // empty')
[ "$zero_ratio" = 0 ] || fail "successful-only tool calls must produce a zero failure ratio"

query_metric "$no_calls_ratio" |
  jq -e '.data.result | length == 0' >/dev/null ||
  fail 'missing tool-call metrics must produce no failure-ratio series'
```

Do not use an unconditional `or vector(0)` or `clamp_min(..., 1)`.

- [ ] **Step 3: Run the contracts and verify that they fail**

Run:

```sh
sh telemetry-backend/tests/dashboard-contract.sh
TEST_HTTP_PORT=28080 TEST_HTTPS_PORT=28443 \
  sh telemetry-backend/tests/with-fixture-stack.sh \
  sh telemetry-backend/tests/metrics-query-contract.sh
```

Expected: the dashboard contract fails on old panels. The metric check fails if the fixture does not yet include a
successful-only series with enough timestamps for a one-hour `increase()`.

- [ ] **Step 4: Implement the four hourly panels**

Use `interval: "1h"` for every target.

`Sessions and turns per hour`:

```promql
sum(increase(codex_thread_started_total{
  service_name="codex_cli_rs",
  session_source="cli"
}[1h]))
```

```promql
sum(increase(codex_conversation_turn_count_total{
  service_name="codex_cli_rs"
}[1h]))
```

`Tool and MCP calls per hour`:

```promql
sum(increase(codex_tool_call_total{service_name="codex_cli_rs"}[1h]))
```

```promql
sum(increase(codex_mcp_call_total{service_name="codex_cli_rs"}[1h]))
```

`Tool failure ratio per hour`:

```promql
(
  sum(increase(codex_tool_call_total{
    service_name="codex_cli_rs",
    success="false"
  }[1h]))
  or
  (
    0 * sum(increase(codex_tool_call_total{
      service_name="codex_cli_rs"
    }[1h]))
  )
)
/
sum(increase(codex_tool_call_total{
  service_name="codex_cli_rs"
}[1h]))
```

`Tokens processed per hour`:

```promql
sum by (token_type) (
  increase(codex_turn_token_usage_sum{
    service_name="codex_cli_rs",
    token_type!="total"
  }[1h])
)
```

Use unit `percentunit` for the ratio and unit `locale`, decimals `0`, for counts and tokens. Descriptions must state
that each point covers the trailing hour.

- [ ] **Step 5: Implement the Codex token matrix**

Use an instant time-series target:

```promql
sum by (model, token_type) (
  increase(codex_turn_token_usage_sum{
    service_name="codex_cli_rs",
    token_type!="total"
  }[$__range])
)
```

Use `joinByLabels` with `join: ["model"]` and `value: "token_type"`, then rename `model` to `Model`. Apply numeric
overrides with unit `locale`, decimals `0`, centered cells, and color-background cell mode. Set `instant: true` and
`format: "time_series"`.

- [ ] **Step 6: Implement all latency quantiles**

Keep the existing p50/p95 turn and tool histogram queries. Give each target an explicit legend and leave the unit as
milliseconds.

Create six API targets from this template:

```promql
histogram_quantile(
  QUANTILE,
  sum by (le) (
    rate(METRIC{service_name="codex_cli_rs"}[$__rate_interval])
  )
)
```

Use the following exact mapping:

| Legend | Quantile | Metric |
| --- | --- | --- |
| `TTFT p50` | `0.50` | `codex_responses_api_engine_service_ttft_duration_ms_milliseconds_bucket` |
| `TTFT p95` | `0.95` | `codex_responses_api_engine_service_ttft_duration_ms_milliseconds_bucket` |
| `TBT p50` | `0.50` | `codex_responses_api_engine_service_tbt_duration_ms_milliseconds_bucket` |
| `TBT p95` | `0.95` | `codex_responses_api_engine_service_tbt_duration_ms_milliseconds_bucket` |
| `Inference p50` | `0.50` | `codex_responses_api_inference_time_duration_ms_milliseconds_bucket` |
| `Inference p95` | `0.95` | `codex_responses_api_inference_time_duration_ms_milliseconds_bucket` |

- [ ] **Step 7: Make the fixture-backed failure-ratio contract pass**

Ensure the Codex fixture has two cumulative samples for successful tool calls within the one-hour lookback and no
`success="false"` series. Keep a positive delta between the samples. Add two cumulative token samples with
`token_type="input"` and `model="fixture-codex"` so the production matrix selector has a positive one-hour delta. Use
the existing render-time fixture timestamp variables rather than fixed dates.

Run:

```sh
TEST_HTTP_PORT=28080 TEST_HTTPS_PORT=28443 \
  sh telemetry-backend/tests/with-fixture-stack.sh \
  sh telemetry-backend/tests/metrics-query-contract.sh
sh telemetry-backend/tests/dashboard-contract.sh
```

Expected: both contracts print PASS.

- [ ] **Step 8: Commit the Codex redesign**

```bash
git add telemetry-backend/tests/dashboard-contract.sh \
  telemetry-backend/tests/metrics-query-contract.sh \
  telemetry-backend/tests/fixtures/otel-metrics.json \
  telemetry-backend/grafana/dashboards/codex-native-metrics.json
git commit -m "feat(telemetry): add hourly Codex activity views"
```

---

### Task 3: Replace Adoption header stats with UTC daily activity

**Files:**

- Modify: `telemetry-backend/tests/dashboard-contract.sh`
- Modify: `telemetry-backend/grafana/dashboards/ai-agent-telemetry-adoption.json`

**Interfaces:**

- Consumes: hook events with the existing repository, harness, OS, and version dashboard filters.
- Produces: UTC daily unique installations, normalized repositories, and observed sessions.
- Preserves: all six skill/MCP matrix, stacked, and table panels.

- [ ] **Step 1: Add the failing Adoption contract**

Require these titles:

```sh
check_titles "$adoption" \
  'Active installations per day' 'Active repositories per day' 'Sessions observed per day' \
  'Telemetry activity' 'Onboarding over time' 'Activity per installation' \
  'Top skills executed' 'Top MCPs' 'Machines by harness' 'Machines by OS' \
  'Skills per repository (matrix)' 'Skills per repository (stacked)' \
  'Skills per repository (table)' 'MCPs per repository (matrix)' \
  'MCPs per repository (stacked)' 'MCPs per repository (table)'
```

Reject the removed titles `Installs`, `Used in repositories`, `Sessions caught`, and `Activity per machine`.

Require all three daily panels to be `timeseries`, `statsRange`, UTC-bucketed, and selftest-free:

```sh
jq -e '
  [
    "Active installations per day",
    "Active repositories per day",
    "Sessions observed per day"
  ] as $titles
  | [.panels[] | select(.title as $title | $titles | index($title))] as $panels
  | ($panels | length == 3)
    and ($panels | all(.type == "timeseries"))
    and ($panels | all(.targets[]; .queryType == "statsRange"))
    and ($panels | all(.targets[]; .expr | contains("agent:!=\"selftest\"")))
    and ($panels | all(.targets[]; .expr | contains("_time:1d offset 0h")))
' "$dashboard_dir/$adoption" >/dev/null ||
  fail "$adoption daily panels must use selftest-free UTC calendar days"
```

Require the repository query to contain the exact normalization stages:

```sh
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
```

Keep explicit assertions for all matrix, stacked, and table titles so the intentional duplication cannot disappear.

- [ ] **Step 2: Run the dashboard contract and verify that it fails**

Run:

```sh
sh telemetry-backend/tests/dashboard-contract.sh
```

Expected: FAIL because the old stat titles and panel types are still present.

- [ ] **Step 3: Implement Active installations per day**

Use a `timeseries` panel and this `statsRange` query:

```logsql
{service.name="ai-agent-telemetry"}
agent:!="selftest"
machine.id:*
repo.remote:~"${repository:regex}"
agent:~"${harness:regex}"
os.type:~"${os:regex}"
service.version:~"${version:regex}"
| stats by (_time:1d offset 0h) count_uniq(machine.id) installations
| sort by (_time)
```

Set unit `locale`, decimals `0`, and describe the UTC calendar-day and unique `machine.id` semantics.

- [ ] **Step 4: Implement Active repositories per day**

Use a `timeseries` panel and this `statsRange` query:

```logsql
{service.name="ai-agent-telemetry"}
agent:!="selftest"
repo.remote:*
repo.remote:~"${repository:regex}"
agent:~"${harness:regex}"
os.type:~"${os:regex}"
service.version:~"${version:regex}"
| replace_regexp(`^[[:space:]]+|[[:space:]]+$`, "") at repo.remote
| format "<lc:repo.remote>" as repo.remote
| replace_regexp(`^(git@github[.]com:|ssh://(git@)?github[.]com/|git://github[.]com/|https?://github[.]com/|github[.]com/)`, "") at repo.remote
| replace_regexp(`/+$`, "") at repo.remote
| replace_regexp(`[.]git$`, "") at repo.remote
| replace_regexp(`/+$`, "") at repo.remote
| repo.remote:*
| stats by (_time:1d offset 0h) count_uniq(repo.remote) repositories
| sort by (_time)
```

Set unit `locale`, decimals `0`, and describe the UTC day, normalization, and exclusion of missing remotes.

- [ ] **Step 5: Implement Sessions observed per day**

Use a `timeseries` panel and this `statsRange` query:

```logsql
{service.name="ai-agent-telemetry"}
agent:!="selftest"
session.id:*
repo.remote:~"${repository:regex}"
agent:~"${harness:regex}"
os.type:~"${os:regex}"
service.version:~"${version:regex}"
| stats by (_time:1d offset 0h) count_uniq(agent, session.id) sessions
| sort by (_time)
```

Set unit `locale`, decimals `0`, and describe the unique `(agent, session.id)` semantics.

- [ ] **Step 6: Rename the diagnostic panel and preserve the three representations**

Rename `Activity per machine` to `Activity per installation`. Do not change the six skill/MCP matrix, stacked, and
table panels or their queries.

Update identifier-leakage assertions so `machine.id` remains allowed only in `Activity per installation`.

- [ ] **Step 7: Run the focused contract**

Run:

```sh
jq empty telemetry-backend/grafana/dashboards/ai-agent-telemetry-adoption.json
sh telemetry-backend/tests/dashboard-contract.sh
```

Expected: PASS.

- [ ] **Step 8: Commit the Adoption redesign**

```bash
git add telemetry-backend/tests/dashboard-contract.sh \
  telemetry-backend/grafana/dashboards/ai-agent-telemetry-adoption.json
git commit -m "feat(telemetry): show daily adoption activity"
```

---

### Task 4: Rebuild Telemetry health around actionable cohorts

**Files:**

- Modify: `telemetry-backend/tests/dashboard-contract.sh`
- Modify: `telemetry-backend/grafana/dashboards/telemetry-health.json`

**Interfaces:**

- Consumes: 30-day hook cohorts for target-version and stale-installation checks.
- Consumes: trailing 24-hour hook events for active distributions.
- Consumes: Codex and Claude native metrics only for signal freshness.
- Produces: actionable counts, a correlated latest-event stale table, and active-population distributions.

- [ ] **Step 1: Replace the health contract with failing actionable-health assertions**

Refactor `check_common` to accept one or more allowed datasource UIDs:

```sh
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
```

Change `check_logs_selector` so it validates only VictoriaLogs targets:

```sh
jq -e '
  [.panels[] | select(.type != "text" and .type != "row") | .targets[]?
    | select(.datasource.uid == "victorialogs")]
  | length > 0 and all(.expr | contains("service.name=\"ai-agent-telemetry\""))
' "$path" >/dev/null || fail "$file contains a logs query without the service selector"
```

Call `check_common "$health" ai-agent-health victorialogs victoriametrics`. Keep the existing single-UID calls for
Adoption, native overview, and Codex.

Set `health_path=$dashboard_dir/$health` before the health-specific `jq` assertions.

Require these titles:

```sh
check_titles "$health" \
  'OTLP coverage boundary' 'Machines not seen on target version' \
  'Inactive for more than 24 hours' 'Inactive for more than 48 hours' \
  'Native metrics freshness' 'Stale installations' \
  'Active installations by version' 'Active installations by harness and OS'
```

Reject every legacy title:

```sh
for removed_title in \
  'Event ID coverage' 'Machine ID coverage' 'Duplicate delivery rate' \
  'MCP duration coverage' 'Data quality by version' 'Version adoption' \
  'Harness and OS coverage' 'MCP outcomes'; do
  jq -e --arg title "$removed_title" 'all(.panels[]; .title != $title)' "$health_path" >/dev/null ||
    fail "$health still contains legacy panel: $removed_title"
done
```

Require a visible textbox variable:

```sh
jq -e '
  any(.templating.list[];
    .name == "target_version" and .type == "textbox" and
    .current.text == "v1.2.0" and .current.value == "v1.2.0")
' "$health_path" >/dev/null ||
  fail "$health must expose target_version with default v1.2.0"
```

Require the cohort boundaries and selftest exclusion:

```sh
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
```

Require latest-event correlation:

```sh
jq -e '
  .panels[] | select(.title == "Stale installations") |
  .targets[0].expr as $expr |
  ($expr | contains("first 1 by (_time desc) partition by (machine.id)")) and
  ($expr | contains("fields machine.id, _time, agent, service.version")) and
  (.fieldConfig.defaults.noValue == "Unknown") and
  any(.transformations[]?; .id == "extractFields" and .options.source == "labels") and
  any(.transformations[]?; .id == "convertFieldType"
    and any(.options.conversions[]?;
      .targetField == "_time" and .destinationType == "time")) and
  any(.transformations[]?; .id == "organize"
    and .options.renameByName["machine.id"] == "Installation"
    and .options.renameByName._time == "Last seen"
    and .options.renameByName.agent == "Harness"
    and .options.renameByName["service.version"] == "Observed version")
' "$health_path" >/dev/null ||
  fail "$health stale table must use one latest event per installation"
```

- [ ] **Step 2: Run the dashboard contract and verify that it fails**

Run:

```sh
sh telemetry-backend/tests/dashboard-contract.sh
```

Expected: FAIL because the health dashboard still contains legacy quality panels.

- [ ] **Step 3: Add the coverage-boundary text and target-version variable**

Create a text panel named `OTLP coverage boundary` with this meaning:

```text
Native OTLP metrics do not share an installation identifier with hook telemetry.
This dashboard can show native-signal freshness, but it cannot count installations
that have or have not configured OTLP export.
```

Add a visible `textbox` variable named `target_version` with label `Target version` and default `v1.2.0`.

- [ ] **Step 4: Add the three actionable cohort counts**

Use neutral stat panels with unit `locale`, decimals `0`, and descriptions that disclose the fixed lookback.

`Machines not seen on target version`:

```logsql
options(ignore_global_time_filter=true)
_time:30d
{service.name="ai-agent-telemetry"}
agent:!="selftest"
machine.id:*
NOT machine.id:in(
  _time:30d
  {service.name="ai-agent-telemetry"}
  agent:!="selftest"
  service.version:="$target_version"
  machine.id:*
  | uniq by (machine.id)
)
| stats count_uniq(machine.id) machines
```

`Inactive for more than 24 hours` uses the same outer 30-day population and excludes IDs returned by this subquery:

```logsql
_time:1d
{service.name="ai-agent-telemetry"}
agent:!="selftest"
machine.id:*
| uniq by (machine.id)
```

`Inactive for more than 48 hours` uses `_time:2d` in that subquery. Both finish with
`| stats count_uniq(machine.id) machines`.

- [ ] **Step 5: Add native metrics freshness**

Use a Prometheus-backed stat panel with:

```promql
time() - max(timestamp(codex_tool_call_total{service_name="codex_cli_rs"}))
```

```promql
time() - max(timestamp(claude_code_session_count_total{
  service_name=~"claude-code|claude-code-desktop"
}))
```

Use legends `Codex` and `Claude`, unit `s`, and a description that defines the value as seconds since the latest
native metric sample. Do not mention installation coverage.

- [ ] **Step 6: Add the correlated stale-installation table**

Use a logs/table query:

```logsql
options(ignore_global_time_filter=true)
_time:30d
{service.name="ai-agent-telemetry"}
agent:!="selftest"
machine.id:*
NOT machine.id:in(
  _time:1d
  {service.name="ai-agent-telemetry"}
  agent:!="selftest"
  machine.id:*
  | uniq by (machine.id)
)
| first 1 by (_time desc) partition by (machine.id)
| fields machine.id, _time, agent, service.version
| sort by (_time desc)
| limit 100
```

Use these transformations and set `fieldConfig.defaults.noValue` to `Unknown`:

```json
[
  {
    "id": "extractFields",
    "options": {
      "source": "labels"
    }
  },
  {
    "id": "convertFieldType",
    "options": {
      "conversions": [
        {
          "targetField": "_time",
          "destinationType": "time"
        }
      ]
    }
  },
  {
    "id": "organize",
    "options": {
      "excludeByName": {
        "Line": true,
        "labels": true
      },
      "renameByName": {
        "machine.id": "Installation",
        "_time": "Last seen",
        "agent": "Harness",
        "service.version": "Observed version"
      }
    }
  }
]
```

- [ ] **Step 7: Add active installation distributions**

Both distribution queries start with:

```logsql
options(ignore_global_time_filter=true)
_time:1d
{service.name="ai-agent-telemetry"}
agent:!="selftest"
machine.id:*
| first 1 by (_time desc) partition by (machine.id)
```

For `Active installations by version`, append:

```logsql
| format if (!service.version:*) "Unknown" as service.version
| stats by (service.version) count() installations
| sort by (installations desc)
| limit 15
```

For `Active installations by harness and OS`, append:

```logsql
| format if (!agent:*) "Unknown" as agent
| format if (!os.type:*) "Unknown" as os.type
| stats by (agent, os.type) count() installations
| sort by (installations desc)
| limit 15
```

Use bounded bar gauges or compact tables with unit `locale`, decimals `0`, and descriptions that state trailing
24-hour activity.

- [ ] **Step 8: Run the focused contract**

Run:

```sh
jq empty telemetry-backend/grafana/dashboards/telemetry-health.json
sh telemetry-backend/tests/dashboard-contract.sh
```

Expected: PASS.

- [ ] **Step 9: Commit the health redesign**

```bash
git add telemetry-backend/tests/dashboard-contract.sh \
  telemetry-backend/grafana/dashboards/telemetry-health.json
git commit -m "feat(telemetry): make health cohorts actionable"
```

---

### Task 5: Validate dashboard queries with deterministic fixtures

**Files:**

- Modify: `telemetry-backend/tests/smoke.sh`
- Modify: `telemetry-backend/tests/metrics-query-contract.sh`
- Modify: `telemetry-backend/tests/fixtures/otel-metrics.json` if Task 2 did not need a second cumulative sample

**Interfaces:**

- Consumes: the fixture Grafana, VictoriaLogs, and VictoriaMetrics stack.
- Produces: query-level evidence for daily UTC series, stale latest-event selection, token matrices, and ratio states.

- [ ] **Step 1: Add the new queries to the fixture Grafana request**

Extend the `queries` array in `smoke.sh` with:

```json
{
  "datasource": {"type": "victoriametrics-logs-datasource", "uid": "victorialogs"},
  "expr": "{service.name=\"ai-agent-telemetry\"} agent:!=\"selftest\" machine.id:* | stats by (_time:1d offset 0h) count_uniq(machine.id) installations | sort by (_time)",
  "queryType": "statsRange",
  "refId": "DA",
  "maxDataPoints": 1000,
  "intervalMs": 86400000
}
```

```json
{
  "datasource": {"type": "prometheus", "uid": "victoriametrics"},
  "expr": "sum by (model, token_type) (increase(codex_turn_token_usage_sum{service_name=\"codex_cli_rs\",token_type!=\"total\"}[1h]))",
  "format": "time_series",
  "instant": true,
  "refId": "TM",
  "maxDataPoints": 1000,
  "intervalMs": 3600000
}
```

Add a stale latest-event query with ref ID `ST` using the exact Task 4 expression and query type required by the
VictoriaLogs plugin.

- [ ] **Step 2: Add failing frame assertions**

Assert:

```sh
jq -e '.results.DA.status == 200 and
  (.results.DA.frames | [.[].schema.fields[]] | any(.name == "Time" and .type == "time")) and
  (.results.DA.frames | [.[].schema.fields[]] | any(.name == "Value" and .type == "number"))' \
  "$grafana_response" >/dev/null ||
  fail 'daily active installations did not return a UTC numeric range frame'

jq -e '.results.TM.status == 200 and
  (.results.TM.frames | [.[].schema.fields[].labels?]
    | any(.model == "fixture-codex" and .token_type != null))' \
  "$grafana_response" >/dev/null ||
  fail 'the token matrix query did not return model and token_type labels'

jq -e '.results.ST.status == 200 and (.results.ST.frames | length > 0)' \
  "$grafana_response" >/dev/null ||
  fail 'the stale query did not return a Grafana frame'
```

- [ ] **Step 3: Run the smoke test and verify the new assertions fail**

Run:

```sh
TEST_HTTP_PORT=28080 TEST_HTTPS_PORT=28443 sh telemetry-backend/tests/smoke.sh
```

Expected: FAIL until the query types and fixture time ranges produce the required frames.

- [ ] **Step 4: Verify the stale row through the VictoriaLogs query endpoint**

Run the same stale query against the authenticated `/select/logsql/query` endpoint and save its JSON Lines response in
the existing temporary-file set:

```sh
stale_response=$(mktemp)
stale_query='options(ignore_global_time_filter=true) _time:30d {service.name="ai-agent-telemetry"} agent:!="selftest" machine.id:* NOT machine.id:in(_time:1d {service.name="ai-agent-telemetry"} agent:!="selftest" machine.id:* | uniq by (machine.id)) | first 1 by (_time desc) partition by (machine.id) | fields machine.id, _time, agent, service.version | sort by (_time desc) | limit 100'
curl --fail --silent --show-error --cacert "$TEST_CA_CERT" \
  --user "$TEST_DASHBOARD_USER:$TEST_DASHBOARD_PASSWORD" \
  --get --data-urlencode "query=$stale_query" \
  "$TEST_BASE_URL/select/logsql/query" >"$stale_response"
```

Add `$stale_response` to the existing trap, then assert:

```sh
jq -s -e '
  length == 1 and
  all(.[];
    has("machine.id") and has("_time") and has("agent") and has("service.version"))
' "$stale_response" >/dev/null ||
  fail 'the stale query did not return one correlated latest event'
```

- [ ] **Step 5: Make the fixture queries deterministic**

Adjust only fixture timestamps, sample pairs, or query request bounds needed to ensure:

- daily events fall into a known UTC bucket;
- the Codex token counter has a positive one-hour delta;
- the stale-table query has one fixture installation outside 24 hours but inside 30 days;
- the zero-ratio fixture has successful calls and no failure series;
- the no-data selector has no tool-call series.

Do not change production retention or dashboard time ranges to accommodate a test.

- [ ] **Step 6: Run the full backend test set**

Run:

```sh
TEST_HTTP_PORT=28080 TEST_HTTPS_PORT=28443 sh telemetry-backend/tests/smoke.sh
sh telemetry-backend/tests/config-contract.sh
sh telemetry-backend/tests/dashboard-contract.sh
go test ./...
```

Expected: every command prints PASS or exits with status `0`.

- [ ] **Step 7: Commit deterministic dashboard query coverage**

```bash
git add telemetry-backend/tests/smoke.sh \
  telemetry-backend/tests/metrics-query-contract.sh \
  telemetry-backend/tests/fixtures/otel-metrics.json
git commit -m "test(telemetry): cover dashboard query semantics"
```

---

### Task 6: Import and inspect all dashboards in the local Grafana

**Files:**

- Verify: `telemetry-backend/grafana/dashboards/telemetry-health.json`
- Verify: `telemetry-backend/grafana/dashboards/ai-agent-telemetry-adoption.json`
- Verify: `telemetry-backend/grafana/dashboards/native-agent-metrics-overview.json`
- Verify: `telemetry-backend/grafana/dashboards/codex-native-metrics.json`

**Interfaces:**

- Consumes: the four tested provisioned JSON files and the existing `victorialogs` and `victoriametrics-local`
  datasources.
- Produces: four overwritten local dashboards with unchanged datasource configuration.

- [ ] **Step 1: Snapshot datasource definitions before import**

Use the authenticated local Grafana API to save:

```text
/grafana/api/datasources/uid/victorialogs
/grafana/api/datasources/uid/victoriametrics-local
```

Store the responses in `/tmp/dashboard-redesign-datasources-before.json`. Do not print credentials or datasource
secrets.

- [ ] **Step 2: Import each dashboard with an in-memory metrics UID remap**

For each of the four dashboard JSON files:

1. Read the JSON from the worktree.
2. Apply this `jq` transformation in the pipeline only:

```jq
walk(
  if type == "object"
    and .type? == "prometheus"
    and .uid? == "victoriametrics"
  then .uid = "victoriametrics-local"
  else .
  end
)
| .id = null
| .version = 0
| {dashboard: ., overwrite: true}
```

3. POST the transformed payload to `/grafana/api/dashboards/db`.
4. Require HTTP `200` and the expected dashboard UID.

Do not save the transformed JSON into the repository.

- [ ] **Step 3: Prove datasources did not change**

Fetch the same two datasource definitions into `/tmp/dashboard-redesign-datasources-after.json`. Remove volatile fields
such as `version` only if Grafana changes them without a datasource update, then compare before and after with `jq -S`.

Expected: no semantic difference.

- [ ] **Step 4: Inspect every dashboard with Chrome DevTools MCP**

Open each dashboard for the last seven days:

```text
http://localhost:13000/grafana/d/native-agent-metrics-overview/native-agent-metrics-overview
http://localhost:13000/grafana/d/codex-native-metrics/codex-native-metrics
http://localhost:13000/grafana/d/ai-agent-telemetry-adoption/adoption-overview
http://localhost:13000/grafana/d/ai-agent-health/telemetry-health
```

For each dashboard:

- take an accessibility snapshot;
- confirm every expected title is present;
- confirm no panel shows a query, transformation, datasource, or JavaScript error;
- inspect console errors and failed XHR/fetch requests;
- verify exact token values use digit grouping and no `tps` unit;
- verify matrices have one row per model and one column per token type;
- verify client versions contain no Time column;
- verify daily and hourly panels expose their time semantics in titles or descriptions;
- verify stale rows contain one installation, timestamp, harness, and version per row;
- verify the health text does not claim installation-level OTLP coverage.

- [ ] **Step 5: Compare rendered values with direct datasource queries**

Use Grafana's datasource proxy or `/api/ds/query` to compare:

- the latest session-per-hour point;
- the latest token-per-hour point;
- one token matrix cell;
- active installations for one UTC day;
- inactive installation counts for 24 and 48 hours;
- one stale table row.

Expected: rendered values and direct query results match.

- [ ] **Step 6: Fix any browser-only defect with a new RED/GREEN cycle**

If a rendered panel exposes a Grafana transformation or formatting defect:

1. add a focused failing assertion to `dashboard-contract.sh`;
2. run it and confirm the expected failure;
3. change only the affected dashboard JSON;
4. rerun the contract;
5. reimport and reinspect the affected dashboard.

Do not patch local Grafana independently from the repository JSON.

---

### Task 7: Run the final verification and review gate

**Files:**

- Verify: all files changed by Tasks 1–6

**Interfaces:**

- Consumes: the complete dashboard redesign diff.
- Produces: evidence that repository contracts, fixture integration, Go tests, and live rendering all pass.

- [ ] **Step 1: Run static validation**

```sh
jq empty telemetry-backend/grafana/dashboards/*.json
git diff --check
sh telemetry-backend/tests/config-contract.sh
sh telemetry-backend/tests/dashboard-contract.sh
```

Expected: all commands exit `0`.

- [ ] **Step 2: Run fixture integration**

```sh
TEST_HTTP_PORT=28080 TEST_HTTPS_PORT=28443 sh telemetry-backend/tests/smoke.sh
```

Expected: all smoke checks print PASS.

- [ ] **Step 3: Run repository tests**

```sh
go test ./...
```

Expected: all packages pass.

- [ ] **Step 4: Review scope and generated artifacts**

```sh
git status --short
git diff --stat HEAD~5
git diff -- telemetry-backend/grafana/dashboards telemetry-backend/tests
```

Confirm:

- no datasource provisioning file changed;
- no local-import payload or credential entered the repository;
- Adoption retains all six matrix/stacked/table panels;
- every dashboard title and description uses the approved semantics;
- all dashboard links still resolve.

- [ ] **Step 5: Request final code review**

Invoke `superpowers:requesting-code-review` against the complete branch diff. Resolve findings with
`superpowers:receiving-code-review` and rerun the affected checks.

- [ ] **Step 6: Commit any final browser or review corrections**

If the final review produced repository changes:

```bash
git add telemetry-backend/grafana/dashboards telemetry-backend/tests
git commit -m "fix(telemetry): align dashboard rendering"
```

Do not create an empty commit when no correction is needed.
