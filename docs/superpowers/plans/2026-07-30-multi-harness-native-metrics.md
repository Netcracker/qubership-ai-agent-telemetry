# Multi-harness native metrics implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add tested native-metrics onboarding, a cross-harness overview, and a Codex deep-dive dashboard.

**Architecture:** Preserve vendor metrics in VictoriaMetrics and classify documented harness contracts in dashboard
queries with exact `service_name` selectors. Keep the Collector metrics pipeline limited to `deltatocumulative` and
`batch`; do not add a synthetic harness label or canonical metric copies.

**Tech stack:** POSIX shell, OTLP/HTTP JSON fixtures, OpenTelemetry Collector Contrib `0.119.0`, VictoriaMetrics
`v1.148.0`, PromQL/MetricsQL, Grafana provisioned dashboard JSON, Docker Compose, `jq`, and `curl`.

## Global constraints

- Preserve the existing hook-based VictoriaLogs dashboards and event contract.
- Use the provisioned Prometheus datasource UID `victoriametrics` for native-metrics panels.
- Treat `service_name` as query classification, not authenticated producer identity.
- Never query or trust a client-provided `agent.harness`, `agent_harness`, or spelling variant.
- Keep raw vendor metric names and instruments unchanged at ingestion.
- Configure Codex and Claude Code for metrics only; do not enable native logs, traces, prompts, or tool content.
- Do not add a Collector privacy processor, recording rules, a Cline hook target, or a Claude deep-dive dashboard.
- Keep Cline outside dashboard selectors until a live export or version-pinned identity contract exists.
- Use American English and keep Markdown body lines within 120 characters.
- Follow the approved design in
  `docs/superpowers/specs/2026-07-30-multi-harness-native-metrics-design.md`.

---

### Task 1: Expand the native metrics fixture and metric query contract

**Files:**

- Modify: `telemetry-backend/tests/fixtures/otel-metrics.json`
- Modify: `telemetry-backend/tests/with-fixture-stack.sh`
- Create: `telemetry-backend/tests/metrics-query-contract.sh`
- Modify: `telemetry-backend/tests/smoke.sh`
- Modify: `telemetry-backend/tests/config-contract.sh`

**Interfaces:**

- Consumes: `TEST_BASE_URL`, `TEST_CA_CERT`, `TEST_DASHBOARD_USER`, `TEST_DASHBOARD_PASSWORD`,
  `TEST_TIME_FROM`, and `TEST_TIME_TO` from `with-fixture-stack.sh`.
- Produces: cumulative Codex, Claude Code, and test-scoped Cline series in VictoriaMetrics.
- Produces: `metrics-query-contract.sh`, which validates normalized metric names, values, and labels.

- [ ] **Step 1: Write the failing metric contract**

Create `metrics-query-contract.sh` with helpers that query the authenticated Prometheus API:

```sh
#!/bin/sh
set -eu

: "${TEST_BASE_URL:?set TEST_BASE_URL}"
: "${TEST_CA_CERT:?set TEST_CA_CERT}"
: "${TEST_DASHBOARD_USER:?set TEST_DASHBOARD_USER}"
: "${TEST_DASHBOARD_PASSWORD:?set TEST_DASHBOARD_PASSWORD}"

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

query_metric() {
  query=$1
  curl --fail --silent --show-error --cacert "$TEST_CA_CERT" \
    --user "$TEST_DASHBOARD_USER:$TEST_DASHBOARD_PASSWORD" \
    --get --data-urlencode "query=$query" \
    "$TEST_BASE_URL/prometheus/api/v1/query"
}

assert_metric_value() {
  name=$1
  expected=$2
  query=$3
  actual=$(query_metric "$query" | jq -r '.data.result[0].value[1] // empty')
  [ "$actual" = "$expected" ] || fail "$name=$actual, want $expected"
}

assert_metric_value codex_sessions 2 \
  'codex_thread_started_total{service_name="codex_cli_rs",session_source="cli"}'
assert_metric_value codex_tools 3 \
  'codex_tool_call_total{service_name="codex_cli_rs",tool="exec_command",success="true"}'
assert_metric_value codex_tokens 1200 \
  'codex_turn_token_usage_sum{service_name="codex_cli_rs",token_type="total",model="fixture-codex"}'
assert_metric_value claude_sessions 2 \
  'claude_code_session_count_total{service_name="claude-code",start_type="fresh"}'
assert_metric_value claude_tokens 900 \
  'claude_code_token_usage_tokens_total{service_name="claude-code",type="input",model="fixture-claude"}'
assert_metric_value cline_fixture 1 \
  'cline_fixture_task_count_total{service_name="cline-fixture"}'

query_metric '{service_name="cline-fixture",agent_harness=~".+"}' |
  jq -e '.data.result | length == 0' >/dev/null ||
  fail 'the Cline fixture must not gain a synthetic harness label'

printf 'PASS: native metrics query contract\n'
```

- [ ] **Step 2: Run the contract and verify that it fails**

Run:

```sh
TEST_HTTP_PORT=28080 TEST_HTTPS_PORT=28443 \
  sh telemetry-backend/tests/with-fixture-stack.sh \
  sh telemetry-backend/tests/metrics-query-contract.sh
```

Expected: FAIL because the fixture stack has not ingested the expanded metrics and the Claude/Cline series are absent.

- [ ] **Step 3: Expand the OTLP fixture**

Keep `codex.tool.call` and add these resource/metric contracts:

| Resource `service.name` | OTLP metric | Type and unit | Required labels | Value |
| --- | --- | --- | --- | --- |
| `codex_cli_rs` | `codex.thread.started` | monotonic cumulative sum | `session_source=cli`, `model=fixture-codex` | `2` |
| `codex_cli_rs` | `codex.turn.token_usage` | cumulative histogram | `token_type=total`, `model=fixture-codex` | sum `1200` |
| `claude-code` | `claude_code.session.count` | monotonic cumulative sum | `start_type=fresh` | `2` |
| `claude-code` | `claude_code.token.usage` | monotonic cumulative sum, unit `tokens` | `type=input`, `model=fixture-claude` | `900` |
| `cline-fixture` | `cline.fixture.task.count` | monotonic cumulative sum | `fixture=true` | `1` |

Use `__METRIC_START_TS__` and `__METRIC_TS__` for every point. The Cline name and service are test-scoped and must not
be documented as a Cline client contract.

For `codex.turn.token_usage`, use explicit histogram fields so VictoriaMetrics creates `_bucket`, `_count`, and `_sum`:

```json
{
  "name": "codex.turn.token_usage",
  "histogram": {
    "aggregationTemporality": 2,
    "dataPoints": [
      {
        "attributes": [
          {"key": "token_type", "value": {"stringValue": "total"}},
          {"key": "model", "value": {"stringValue": "fixture-codex"}}
        ],
        "startTimeUnixNano": "__METRIC_START_TS__",
        "timeUnixNano": "__METRIC_TS__",
        "count": "1",
        "sum": 1200,
        "bucketCounts": ["0", "1"],
        "explicitBounds": [1000]
      }
    ]
  }
}
```

- [ ] **Step 4: Ingest metrics before running metric assertions**

Move the authenticated fixture POST from `smoke.sh` into `with-fixture-stack.sh`, after the logs fixture becomes
queryable. Poll for `codex_tool_call_total=3`, then export the existing test variables.

Remove the duplicate fixture POST and its poll from `smoke.sh`. Call the new contract beside the existing contracts:

```sh
sh "$script_dir/query-contract.sh"
sh "$script_dir/metrics-query-contract.sh"
sh "$script_dir/dashboard-contract.sh"
```

- [ ] **Step 5: Lock the Collector pipeline contract**

Add `collector_config=$backend_dir/otel-collector-config.yaml` to `config-contract.sh`. Assert that the metrics pipeline
contains `deltatocumulative` followed by `batch` and reject any identity processor:

```sh
metrics_pipeline=$(sed -n '/^    metrics:$/,/^    [a-z]/p' "$collector_config")
printf '%s\n' "$metrics_pipeline" | grep -Fq 'processors: [deltatocumulative, batch]' ||
  fail 'metrics pipeline must convert delta metrics and batch them'
if grep -Fq 'transform/metrics_identity' "$collector_config"; then
  fail 'metrics pipeline must not classify harness identity at ingestion'
fi
```

- [ ] **Step 6: Run the focused contracts**

Run:

```sh
TEST_HTTP_PORT=28080 TEST_HTTPS_PORT=28443 \
  sh telemetry-backend/tests/with-fixture-stack.sh \
  sh telemetry-backend/tests/metrics-query-contract.sh
sh telemetry-backend/tests/config-contract.sh
```

Expected: `PASS: native metrics query contract` and `PASS: backend configuration contract`.

- [ ] **Step 7: Commit the fixture contract**

```bash
git add telemetry-backend/tests/fixtures/otel-metrics.json \
  telemetry-backend/tests/with-fixture-stack.sh \
  telemetry-backend/tests/metrics-query-contract.sh \
  telemetry-backend/tests/config-contract.sh \
  telemetry-backend/tests/smoke.sh
git commit -m "test(telemetry): cover multi-harness native metrics"
```

---

### Task 2: Document native-metrics onboarding and support boundaries

**Files:**

- Modify: `telemetry-backend/README.md`
- Modify: `telemetry-backend/tests/config-contract.sh`

**Interfaces:**

- Consumes: remote `/v1/metrics` and `/v1/logs` routes already exposed by Caddy.
- Produces: copy-ready metrics-only Codex and Claude Code setup, conditional Cline setup, and the support matrix.

- [ ] **Step 1: Add failing documentation assertions**

Extend the README text loop in `config-contract.sh` with these stable anchors:

```sh
'Native agent metrics overview'
'Codex native metrics'
'Native metrics and hook telemetry'
'metrics_exporter = { otlp-http = {'
'OTEL_EXPORTER_OTLP_METRICS_ENDPOINT'
'OTEL_METRICS_INCLUDE_SESSION_ID=false'
'Cline is not an accepted `--harnesses` target'
'Backend fixture'
```

- [ ] **Step 2: Run the contract and verify that it fails**

Run:

```sh
sh telemetry-backend/tests/config-contract.sh
```

Expected: FAIL on the first new README anchor.

- [ ] **Step 3: Replace the Codex pilot section with the support matrix and signal explanation**

Add this capability model:

| Harness | Hook events | Native metrics | Validation | APM |
| --- | --- | --- | --- | --- |
| Codex | Supported | Supported | Live pilot and backend fixture | Supported |
| Claude Code | Supported | Supported | Backend fixture | Supported |
| Cursor | Supported | Not documented | Existing hook coverage | Supported |
| Cline | Not supported | Supported | Backend fixture | Not supported |

Define `Backend fixture` and `Live pilot` exactly as the design does. Add a section named
`Native metrics and hook telemetry` that states:

- native exporters provide vendor counters and histograms;
- `ai-agent-telemetry` hooks provide normalized skill, MCP, command, repository, and machine events;
- one signal does not replace the other; and
- the released agent binary is sufficient because it does not configure native exporters.

- [ ] **Step 4: Add the Codex metrics-only procedure**

Document the user-level `~/.codex/config.toml` block:

```toml
[otel]
environment = "prod"
exporter = "none"
trace_exporter = "none"
log_user_prompt = false
metrics_exporter = { otlp-http = { endpoint = "https://<SITE_ADDRESS>/v1/metrics", protocol = "binary", headers = { Authorization = "Bearer <INGEST_TOKEN>" } } }
```

State that users must merge an existing `[otel]` table, fully restart Codex, run a turn, and check
`codex_thread_started_total`. Retain the plaintext-token and missing stable producer-identity caveats.

- [ ] **Step 5: Add the Claude Code metrics-only procedure**

Document these variables:

```bash
export CLAUDE_CODE_ENABLE_TELEMETRY=1
export OTEL_METRICS_EXPORTER=otlp
export OTEL_LOGS_EXPORTER=none
export OTEL_TRACES_EXPORTER=none
export OTEL_EXPORTER_OTLP_METRICS_PROTOCOL=http/protobuf
export OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=https://<SITE_ADDRESS>/v1/metrics
export OTEL_EXPORTER_OTLP_HEADERS='Authorization=Bearer <INGEST_TOKEN>'
export OTEL_METRICS_INCLUDE_SESSION_ID=false
export OTEL_METRICS_INCLUDE_VERSION=true
claude
```

State that the default metric export interval is 60 seconds and that `claude --debug` exposes export failures. Disclose
the always-included anonymous `user.id`, possible OAuth `user.email`, organization/account attributes, and the lack of
a Collector privacy processor in this deployment.

- [ ] **Step 6: Add the bounded Cursor and Cline procedures**

For Cursor, state that the public documentation has no documented native OTLP metrics exporter as of July 30, 2026,
and point to hook telemetry.

For Cline, document dashboard-based metrics enablement and the environment override limitation. Require administrators
to disable logs in Remote Configuration and verify the effective exporters with:

```bash
TEL_DEBUG_DIAGNOSTICS=true code .
```

State verbatim: `Cline is not an accepted --harnesses target or a first-class APM target.` Do not invent a native Cline
metric name or stable `service.name`.

State that the support matrix is the scope boundary rather than an exhaustive harness inventory. Mention GitHub
Copilot CLI as a documented native-OTLP client that remains outside this PR.

- [ ] **Step 7: Update the dashboard catalog**

Add:

- **Native agent metrics overview** at `/grafana/d/native-agent-metrics-overview`;
- **Codex native metrics** at `/grafana/d/codex-native-metrics`.

Explain that the overview uses query-time classification and that the Codex dashboard reflects the live `0.146.0`
pilot contract.

- [ ] **Step 8: Run the documentation contract**

Run:

```sh
sh telemetry-backend/tests/config-contract.sh
```

Expected: `PASS: backend configuration contract`.

- [ ] **Step 9: Commit the onboarding documentation**

```bash
git add telemetry-backend/README.md telemetry-backend/tests/config-contract.sh
git commit -m "docs(telemetry): add native metrics onboarding"
```

---

### Task 3: Add the native agent metrics overview

**Files:**

- Modify: `telemetry-backend/tests/dashboard-contract.sh`
- Create: `telemetry-backend/grafana/dashboards/native-agent-metrics-overview.json`

**Interfaces:**

- Consumes: Codex series selected with `service_name="codex_cli_rs"`.
- Consumes: Claude Code series selected with `service_name=~"claude-code|claude-code-desktop"`.
- Produces: dashboard UID `native-agent-metrics-overview` using datasource UID `victoriametrics`.

- [ ] **Step 1: Generalize the dashboard contract by datasource**

Replace the log-specific `check_common` assumptions with:

```sh
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
```

Call it with `victorialogs` for the existing dashboards. Keep their existing service-selector checks in a new
`check_logs_selector` helper.

- [ ] **Step 2: Add failing overview assertions**

Require the overview UID and these panel titles:

```text
Signal availability
Metrics freshness
Top-level sessions started
Token usage over time
Tokens by model and type
Observed client versions
```

Assert that:

- every non-text target uses datasource UID `victoriametrics`;
- Codex targets contain `service_name="codex_cli_rs"`;
- Claude targets contain `service_name=~"claude-code|claude-code-desktop"`;
- no target contains `agent_harness`;
- the sessions panel contains `session_source="cli"` and `start_type!="agents_view"`; and
- token panels use Grafana units `tokens` or `tps`.

- [ ] **Step 3: Run the dashboard contract and verify that it fails**

Run:

```sh
sh telemetry-backend/tests/dashboard-contract.sh
```

Expected: FAIL because `native-agent-metrics-overview.json` is missing.

- [ ] **Step 4: Create the overview dashboard**

Use `schemaVersion: 41`, `editable: false`, default range `now-7d`, refresh `5m`, and graph tooltip mode `1`. Include
links to Adoption overview, Telemetry health, and Codex native metrics.

Use these target expressions:

```promql
time() - max(timestamp(codex_tool_call_total{service_name="codex_cli_rs"}))
time() - max(timestamp({__name__=~"claude_code_.*",service_name=~"claude-code|claude-code-desktop"}))
sum(increase(codex_thread_started_total{service_name="codex_cli_rs",session_source="cli"}[$__range]))
sum(increase(claude_code_session_count_total{service_name=~"claude-code|claude-code-desktop",start_type!="agents_view"}[$__range]))
sum by (token_type) (rate(codex_turn_token_usage_sum{service_name="codex_cli_rs",token_type!="total"}[$__rate_interval]))
sum by (type) (rate(claude_code_token_usage_tokens_total{service_name=~"claude-code|claude-code-desktop"}[$__rate_interval]))
sum by (model, token_type) (increase(codex_turn_token_usage_sum{service_name="codex_cli_rs",token_type!="total"}[$__range]))
sum by (model, type) (increase(claude_code_token_usage_tokens_total{service_name=~"claude-code|claude-code-desktop"}[$__range]))
group by (app_version) (max_over_time(codex_tool_call_total{service_name="codex_cli_rs"}[$__range]))
group by (app_version) (max_over_time(claude_code_session_count_total{service_name=~"claude-code|claude-code-desktop"}[$__range]))
```

Use explicit legends such as `Codex`, `Claude`, `Codex · {{token_type}}`, and `Claude · {{type}}`. Render signal
availability as a static Markdown table so unsupported signals never become numeric zeroes.

- [ ] **Step 5: Run and pass the focused dashboard contract**

Run:

```sh
jq empty telemetry-backend/grafana/dashboards/native-agent-metrics-overview.json
sh telemetry-backend/tests/dashboard-contract.sh
```

Expected: valid JSON and `PASS: Grafana dashboard contract`.

- [ ] **Step 6: Commit the overview**

```bash
git add telemetry-backend/tests/dashboard-contract.sh \
  telemetry-backend/grafana/dashboards/native-agent-metrics-overview.json
git commit -m "feat(telemetry): add native metrics overview"
```

---

### Task 4: Add the Codex native metrics deep-dive

**Files:**

- Modify: `telemetry-backend/tests/dashboard-contract.sh`
- Create: `telemetry-backend/grafana/dashboards/codex-native-metrics.json`

**Interfaces:**

- Consumes: live Codex `0.146.0` series and the Codex fixture.
- Produces: dashboard UID `codex-native-metrics` using datasource UID `victoriametrics`.

- [ ] **Step 1: Add failing Codex dashboard assertions**

Require these panel titles:

```text
Top-level sessions
Conversation turns
Tool calls
MCP calls
Total tokens
Tool failure ratio
Tool and MCP activity
Top tools
MCP servers and outcomes
Tokens by model and type
Turn latency
Tool latency
API latency
Skill injections
```

Assert that every target contains `service_name="codex_cli_rs"`, no target contains `agent_harness`, duration panels
use unit `ms`, and the failure ratio uses unit `percentunit`.

- [ ] **Step 2: Run the dashboard contract and verify that it fails**

Run:

```sh
sh telemetry-backend/tests/dashboard-contract.sh
```

Expected: FAIL because `codex-native-metrics.json` is missing.

- [ ] **Step 3: Create the Codex summary and activity panels**

Use these expressions:

```promql
sum(increase(codex_thread_started_total{service_name="codex_cli_rs",session_source="cli"}[$__range]))
sum(increase(codex_conversation_turn_count_total{service_name="codex_cli_rs",session_source="cli"}[$__range]))
sum(increase(codex_tool_call_total{service_name="codex_cli_rs"}[$__range]))
sum(increase(codex_mcp_call_total{service_name="codex_cli_rs"}[$__range]))
sum(increase(codex_turn_token_usage_sum{service_name="codex_cli_rs",token_type="total"}[$__range]))
sum(increase(codex_tool_call_total{service_name="codex_cli_rs",success="false"}[$__range]))
/
clamp_min(sum(increase(codex_tool_call_total{service_name="codex_cli_rs"}[$__range])), 1)
sum(rate(codex_tool_call_total{service_name="codex_cli_rs"}[$__rate_interval]))
sum(rate(codex_mcp_call_total{service_name="codex_cli_rs"}[$__rate_interval]))
```

Use neutral stat colors for counts. Render failure ratio as `percentunit`, where `0.05` displays as 5%.

- [ ] **Step 4: Create the breakdown panels**

Use:

```promql
topk(10, sum by (tool) (increase(codex_tool_call_total{service_name="codex_cli_rs"}[$__range])))
topk(10, sum by (server, status) (increase(codex_mcp_call_total{service_name="codex_cli_rs"}[$__range])))
sum by (model, token_type) (increase(codex_turn_token_usage_sum{service_name="codex_cli_rs",token_type!="total"}[$__range]))
topk(15, sum by (skill, invoke_type, status) (increase(codex_skill_injected_total{service_name="codex_cli_rs"}[$__range])))
```

Do not display `session_source` values that contain thread identifiers.

- [ ] **Step 5: Create the latency panels**

Use p50 and p95 targets:

```promql
histogram_quantile(0.50, sum by (le) (rate(codex_turn_e2e_duration_ms_milliseconds_bucket{service_name="codex_cli_rs"}[$__rate_interval])))
histogram_quantile(0.95, sum by (le) (rate(codex_turn_e2e_duration_ms_milliseconds_bucket{service_name="codex_cli_rs"}[$__rate_interval])))
histogram_quantile(0.50, sum by (le) (rate(codex_tool_call_duration_ms_milliseconds_bucket{service_name="codex_cli_rs"}[$__rate_interval])))
histogram_quantile(0.95, sum by (le) (rate(codex_tool_call_duration_ms_milliseconds_bucket{service_name="codex_cli_rs"}[$__rate_interval])))
histogram_quantile(0.95, sum by (le) (rate(codex_responses_api_engine_service_ttft_duration_ms_milliseconds_bucket{service_name="codex_cli_rs"}[$__rate_interval])))
histogram_quantile(0.95, sum by (le) (rate(codex_responses_api_engine_service_tbt_duration_ms_milliseconds_bucket{service_name="codex_cli_rs"}[$__rate_interval])))
histogram_quantile(0.95, sum by (le) (rate(codex_responses_api_inference_time_duration_ms_milliseconds_bucket{service_name="codex_cli_rs"}[$__rate_interval])))
```

Use legends `p50`, `p95`, `TTFT p95`, `TBT p95`, and `Inference p95`. State in panel descriptions that histogram
quantiles require enough observations in the selected rate interval.

- [ ] **Step 6: Run and pass the focused dashboard contract**

Run:

```sh
jq empty telemetry-backend/grafana/dashboards/codex-native-metrics.json
sh telemetry-backend/tests/dashboard-contract.sh
```

Expected: valid JSON and `PASS: Grafana dashboard contract`.

- [ ] **Step 7: Commit the Codex dashboard**

```bash
git add telemetry-backend/tests/dashboard-contract.sh \
  telemetry-backend/grafana/dashboards/codex-native-metrics.json
git commit -m "feat(telemetry): add Codex metrics dashboard"
```

---

### Task 5: Verify provisioning, local installation, datasource execution, and the live dashboards

**Files:**

- Modify: `telemetry-backend/tests/smoke.sh`
- Modify: `telemetry-backend/tests/metrics-query-contract.sh`
- Modify if browser review finds a defect:
  `telemetry-backend/grafana/dashboards/native-agent-metrics-overview.json`
- Modify if browser review finds a defect:
  `telemetry-backend/grafana/dashboards/codex-native-metrics.json`

**Interfaces:**

- Consumes: both dashboard UIDs and the fixture metrics from Tasks 1, 3, and 4.
- Produces: end-to-end proof that Grafana provisions and executes Prometheus targets.
- Produces: browser-validated dashboards in the existing Grafana at `http://localhost:13000`.
- Preserves: every existing local Grafana datasource and its configuration.

- [ ] **Step 1: Add failing provisioning checks**

Expand the dashboard UID loop in `smoke.sh`:

```sh
for uid in \
  ai-agent-health \
  ai-agent-telemetry-adoption \
  native-agent-metrics-overview \
  codex-native-metrics; do
  curl --fail --silent --show-error --cacert "$TEST_CA_CERT" \
    --cookie "$viewer_cookie" \
    "$TEST_BASE_URL/grafana/api/dashboards/uid/$uid" >/dev/null ||
    fail "dashboard $uid was not provisioned"
done
```

- [ ] **Step 2: Add representative Grafana Prometheus queries**

Append queries using datasource `{type: "prometheus", uid: "victoriametrics"}` to the existing `/grafana/api/ds/query`
request:

```json
{
  "datasource": {"type": "prometheus", "uid": "victoriametrics"},
  "expr": "sum(codex_tool_call_total{service_name=\"codex_cli_rs\"})",
  "format": "time_series",
  "instant": true,
  "refId": "CM",
  "intervalMs": 60000,
  "maxDataPoints": 1000
}
```

```json
{
  "datasource": {"type": "prometheus", "uid": "victoriametrics"},
  "expr": "sum(claude_code_token_usage_tokens_total{service_name=\"claude-code\",type=\"input\"})",
  "format": "time_series",
  "instant": true,
  "refId": "HM",
  "intervalMs": 60000,
  "maxDataPoints": 1000
}
```

Assert both results return status `200`, numeric frames, Codex value `3`, and Claude value `900`.

- [ ] **Step 3: Run the full backend smoke test**

Run:

```sh
TEST_HTTP_PORT=28080 TEST_HTTPS_PORT=28443 sh telemetry-backend/tests/smoke.sh
```

Expected:

```text
PASS: LogsQL query contract
PASS: native metrics query contract
PASS: Grafana dashboard contract
PASS: telemetry backend smoke test
```

- [ ] **Step 4: Install the dashboards in the existing local Grafana**

Use the authenticated browser session and the Grafana dashboard API to create or overwrite only these dashboard UIDs:

```text
native-agent-metrics-overview
codex-native-metrics
```

Import the repository JSON models with `overwrite: true`. Do not create, update, delete, or reprovision any datasource.
Before and after the import, capture the datasource UID, type, URL, and access mode from the Grafana API and verify that
they are unchanged.

- [ ] **Step 5: Open the overview against the existing real-data datasource**

Use the existing local Grafana and Chrome DevTools connection. Open:

```text
http://localhost:13000/grafana/d/native-agent-metrics-overview
```

Set the time range to seven days. Confirm:

- Codex freshness is nonempty;
- Codex top-level sessions and token panels render;
- Claude panels show no data, not numeric zero, until a real Claude client connects;
- the signal-availability table explains unsupported combinations; and
- the browser console has no dashboard query errors.

- [ ] **Step 6: Open the Codex deep-dive against accumulated data**

Open:

```text
http://localhost:13000/grafana/d/codex-native-metrics
```

Confirm:

- summary stats are nonempty;
- tools include `exec_command` and MCP data includes the observed servers;
- token legends contain model and token type;
- p50/p95 panels use milliseconds;
- skill labels are readable; and
- no panel exposes a session or thread identifier.

Adjust only queries, units, legends, descriptions, or layout defects observed in this browser pass. Re-run
`dashboard-contract.sh` after every dashboard JSON change.

- [ ] **Step 7: Run final repository checks**

Run:

```sh
git diff --check
go test ./...
TEST_HTTP_PORT=28080 TEST_HTTPS_PORT=28443 sh telemetry-backend/tests/smoke.sh
```

Expected: all commands exit `0`.

- [ ] **Step 8: Commit end-to-end verification changes**

```bash
git add telemetry-backend/tests/smoke.sh \
  telemetry-backend/tests/metrics-query-contract.sh \
  telemetry-backend/grafana/dashboards/native-agent-metrics-overview.json \
  telemetry-backend/grafana/dashboards/codex-native-metrics.json
git commit -m "test(telemetry): verify native metrics dashboards"
```

- [ ] **Step 9: Review branch state before PR update**

Run:

```sh
git status --short --branch
git log --oneline --decorate -10
git diff im/feat/remote-codex-metrics...HEAD --stat
```

Expected: a clean worktree and only the approved native-metrics, dashboard, test, and documentation changes.
