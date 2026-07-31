# Dashboard freshness and onboarding docs implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Correct native sample-age and time-bucket semantics, separate client onboarding from backend operations, and
update PR #26 with a reviewer-oriented explanation.

**Architecture:** Keep raw vendor metrics and the existing Collector pipeline unchanged. Grafana queries derive sample
age from client timestamps over a fixed 30-day window and use Grafana's complete dynamic interval for activity panels.
The backend README describes deployment and support boundaries; a sibling onboarding document owns manual harness
configuration until the lifecycle installer adds that capability in a follow-up PR.

**Tech Stack:** Grafana dashboard JSON, PromQL/MetricsQL, VictoriaLogs LogsQL, POSIX shell contract tests, Markdown,
Docker Compose, Grafana browser validation, and GitHub CLI.

## Global constraints

- Backend retention remains 365 days; native metric sample-age lookback is fixed at 30 days.
- Sample age is based on the client-provided OTLP sample timestamp, not Collector receipt time.
- Codex and Claude Code are the only dashboard-classifiable native-metrics harnesses.
- Cline remains backend-compatible but has no dashboard selector.
- Activity panels cover the complete Grafana `$__interval`; count panels normalize the result to an hourly average.
- Activity query Min step remains `1h`.
- The adoption dashboard defaults to 30 complete UTC days and discloses partial edge buckets for custom ranges.
- The lifecycle installer does not configure native OTLP exporters in this PR.
- Provisioned datasource UIDs remain `victorialogs` and `victoriametrics`.
- Local Grafana validation must not create, delete, or modify datasources.

---

### Task 1: Preserve stale native sample visibility

**Files:**

- Modify: `telemetry-backend/tests/dashboard-contract.sh`
- Modify: `telemetry-backend/grafana/dashboards/telemetry-health.json`
- Modify: `telemetry-backend/grafana/dashboards/native-agent-metrics-overview.json`

**Interfaces:**

- Consumes: VictoriaMetrics `tlast_over_time(range-vector)` and the stored client sample timestamp.
- Produces: two panels titled `Native metric sample age`, each with Codex and Claude Code series in seconds.

- [ ] **Step 1: Write the failing dashboard contract**

Replace the health and overview freshness assertions with exact checks for:

```jq
.title == "Native metric sample age"
```

Codex:

```promql
time() - max(tlast_over_time({__name__=~"codex_.*",service_name="codex_cli_rs"}[30d]))
```

Claude Code:

```promql
time() - max(tlast_over_time({__name__=~"claude_code_.*",service_name=~"claude-code|claude-code-desktop"}[30d]))
```

Require the description to contain all of these phrases:

```text
client-provided sample timestamp
previous 30 days
clock skew or replay
```

Require unit `s`, neutral coloring, and exactly two dashboard-classifiable harness targets.

- [ ] **Step 2: Run the contract to verify it fails**

Run:

```bash
sh telemetry-backend/tests/dashboard-contract.sh
```

Expected: FAIL because the dashboards still use `timestamp()` on one representative metric and retain the old title.

- [ ] **Step 3: Implement the sample-age queries**

Update both dashboard JSON files:

- rename the panel to `Native metric sample age`;
- replace representative metric selectors with the two namespace queries above;
- describe the 30-day window and client-timestamp limitation;
- retain seconds as the unit and neutral color mode;
- update panel links or grid assertions only where the renamed title requires it.

- [ ] **Step 4: Run the focused contract**

Run:

```bash
sh telemetry-backend/tests/dashboard-contract.sh
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add telemetry-backend/tests/dashboard-contract.sh \
  telemetry-backend/grafana/dashboards/telemetry-health.json \
  telemetry-backend/grafana/dashboards/native-agent-metrics-overview.json
git commit -m "fix(telemetry): preserve stale native sample visibility"
```

### Task 2: Cover complete dynamic activity intervals

**Files:**

- Modify: `telemetry-backend/tests/dashboard-contract.sh`
- Modify: `telemetry-backend/grafana/dashboards/native-agent-metrics-overview.json`
- Modify: `telemetry-backend/grafana/dashboards/codex-native-metrics.json`

**Interfaces:**

- Consumes: Grafana `$__interval`, `$__interval_ms`, and target Min step `1h`.
- Produces: hourly-average count series with no uncovered interval when Grafana increases the query step.

- [ ] **Step 1: Replace fixed-window assertions with failing dynamic-window assertions**

Require the overview titles:

```text
Average top-level sessions per hour
Average tokens processed per hour
```

Require the Codex titles:

```text
Average sessions and turns per hour
Average tool and MCP calls per hour
Tool failure ratio per query interval
Average tokens processed per hour
```

For every count target, require `.interval == "1h"`, `[$__interval]`, and the normalization:

```promql
* 3600000 / $__interval_ms
```

For the failure-ratio target, require all three `increase()` calls to use `[$__interval]`, and reject `[1h]`.

- [ ] **Step 2: Run the contract to verify it fails**

Run:

```bash
sh telemetry-backend/tests/dashboard-contract.sh
```

Expected: FAIL because the current titles and queries use fixed `[1h]` windows.

- [ ] **Step 3: Update overview activity queries**

Use:

```promql
sum(increase(codex_thread_started_total{service_name="codex_cli_rs",session_source="cli"}[$__interval])) * 3600000 / $__interval_ms
```

```promql
sum(increase(claude_code_session_count_total{service_name=~"claude-code|claude-code-desktop",start_type!="agents_view"}[$__interval])) * 3600000 / $__interval_ms
```

```promql
sum by (token_type) (increase(codex_turn_token_usage_sum{service_name="codex_cli_rs",token_type!="total"}[$__interval])) * 3600000 / $__interval_ms
```

```promql
sum by (type) (increase(claude_code_token_usage_tokens_total{service_name=~"claude-code|claude-code-desktop"}[$__interval])) * 3600000 / $__interval_ms
```

Descriptions must state that one-hour steps show that hour's count and longer steps show an hourly average across the
complete interval.

- [ ] **Step 4: Update Codex activity queries**

Apply the same dynamic window and hourly normalization to sessions, turns, tool calls, MCP calls, and tokens. Use this
failure-ratio query without hourly normalization:

```promql
(sum(increase(codex_tool_call_total{service_name="codex_cli_rs",success="false"}[$__interval])) or (0 * sum(increase(codex_tool_call_total{service_name="codex_cli_rs"}[$__interval])))) / sum(increase(codex_tool_call_total{service_name="codex_cli_rs"}[$__interval]))
```

Preserve the existing zero-versus-no-data behavior.

- [ ] **Step 5: Run the focused contract**

Run:

```bash
sh telemetry-backend/tests/dashboard-contract.sh
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add telemetry-backend/tests/dashboard-contract.sh \
  telemetry-backend/grafana/dashboards/native-agent-metrics-overview.json \
  telemetry-backend/grafana/dashboards/codex-native-metrics.json
git commit -m "fix(telemetry): cover complete activity intervals"
```

### Task 3: Make the default adoption range complete UTC days

**Files:**

- Modify: `telemetry-backend/tests/dashboard-contract.sh`
- Modify: `telemetry-backend/grafana/dashboards/ai-agent-telemetry-adoption.json`

**Interfaces:**

- Consumes: Grafana dashboard timezone and relative time rounding.
- Produces: a default range of 30 complete UTC days with honest custom-range edge semantics.

- [ ] **Step 1: Write the failing UTC-range contract**

Require:

```jq
.timezone == "utc"
and .time.from == "now-30d/d"
and .time.to == "now/d"
```

For `Active installations per day`, `Active repositories per day`, and `Sessions observed per day`, require each
description to contain:

```text
UTC
custom range
edge buckets may be partial
```

Keep the existing `_time:1d offset 0h` and `agent:!="selftest"` checks.

- [ ] **Step 2: Run the contract to verify it fails**

Run:

```bash
sh telemetry-backend/tests/dashboard-contract.sh
```

Expected: FAIL because the dashboard uses browser timezone and `now-30d` through `now`.

- [ ] **Step 3: Update the adoption dashboard**

Set:

```json
"timezone": "utc",
"time": {
  "from": "now-30d/d",
  "to": "now/d"
}
```

Update all three daily descriptions to identify UTC calendar-day counts and disclose that custom ranges not aligned
to UTC midnight produce partial edge buckets.

- [ ] **Step 4: Run the focused contract**

Run:

```bash
sh telemetry-backend/tests/dashboard-contract.sh
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add telemetry-backend/tests/dashboard-contract.sh \
  telemetry-backend/grafana/dashboards/ai-agent-telemetry-adoption.json
git commit -m "fix(telemetry): default adoption to complete UTC days"
```

### Task 4: Separate backend operation from harness onboarding

**Files:**

- Create: `telemetry-backend/native-otlp-onboarding.md`
- Modify: `telemetry-backend/README.md`

**Interfaces:**

- Consumes: the current Codex, Claude Code, Cursor, and Cline onboarding content.
- Produces: a backend-focused README and a manually executed client onboarding guide.

- [ ] **Step 1: Create the onboarding document**

Move the harness configuration instructions into `telemetry-backend/native-otlp-onboarding.md` with these sections:

```markdown
# Native OTLP onboarding

## Scope
## Before you start
## Codex metrics only
## Claude Code metrics only
## Cursor
## Cline
## Verify ingestion
## Remove the configuration
```

State near the top:

```text
The lifecycle installer does not configure these exporters. Apply the matching client configuration manually.
```

Preserve the existing metrics-only examples, plaintext-token warning for Codex, Cline effective-configuration check,
and dashboard or VictoriaMetrics verification steps. Add reversible removal instructions:

- remove only the installer-independent keys added to the Codex `[otel]` table;
- remove the Claude Code environment entries from the shell or managed settings source;
- disable Cline metrics export in Remote Configuration and remove local overrides.

- [ ] **Step 2: Reduce the backend README to operational scope**

Keep the support matrix, validation-level definitions, native-versus-hook explanation, and dashboard links. Replace the
per-harness configuration sections with:

```markdown
The backend accepts native OTLP metrics, but this release does not configure harness exporters. Follow
[Native OTLP onboarding](native-otlp-onboarding.md) to opt in manually.
```

Update Claude Code validation to `Live pilot and backend fixture` and identify Codex `0.146.0` and Claude Code
`2.1.220` as the July 30, 2026 live pilots. Do not claim a live Cline export.

- [ ] **Step 3: Validate Markdown and links**

Run:

```bash
markdownlint-cli2 telemetry-backend/README.md telemetry-backend/native-otlp-onboarding.md
```

Expected: PASS. If repository-wide baseline lint differs, both changed files must have no newly introduced findings.

Run:

```bash
git diff --check
```

Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add telemetry-backend/README.md telemetry-backend/native-otlp-onboarding.md
git commit -m "docs(telemetry): separate native exporter onboarding"
```

### Task 5: Verify dashboards with tests and real data

**Files:**

- Modify only if a verified defect is found:
  `telemetry-backend/grafana/dashboards/telemetry-health.json`
- Modify only if a verified defect is found:
  `telemetry-backend/grafana/dashboards/native-agent-metrics-overview.json`
- Modify only if a verified defect is found:
  `telemetry-backend/grafana/dashboards/codex-native-metrics.json`
- Modify only if a verified defect is found:
  `telemetry-backend/grafana/dashboards/ai-agent-telemetry-adoption.json`

**Interfaces:**

- Consumes: provisioned JSON, existing real VictoriaMetrics and VictoriaLogs datasources in local Grafana.
- Produces: browser-verified dashboards without datasource mutations.

- [ ] **Step 1: Run repository verification**

Run:

```bash
sh telemetry-backend/tests/dashboard-contract.sh
sh telemetry-backend/tests/config-contract.sh
TEST_HTTP_PORT=28080 TEST_HTTPS_PORT=28443 sh telemetry-backend/tests/smoke.sh
go test ./...
shellcheck telemetry-backend/tests/config-contract.sh telemetry-backend/tests/dashboard-contract.sh
git diff --check
```

Expected: every command exits `0`.

- [ ] **Step 2: Import the four dashboard JSON files into local Grafana**

Import:

```text
telemetry-health.json
native-agent-metrics-overview.json
codex-native-metrics.json
ai-agent-telemetry-adoption.json
```

Use the existing `victoriametrics-local` mapping only during import. Do not create, delete, or modify Grafana
datasources.

- [ ] **Step 3: Validate with browser developer tools**

Open `http://localhost:13000` and verify:

- Native metric sample age shows Claude Code's stale age instead of No data.
- The panel description discloses client timestamp, 30 days, clock skew, and replay.
- Overview and Codex activity panels render without PromQL errors.
- Query inspector shows dynamic interval substitution and no fixed `[1h]` activity windows.
- Adoption opens in UTC with 30 complete days by default.
- Daily descriptions disclose partial edge buckets for custom ranges.
- Existing datasource settings remain unchanged.

- [ ] **Step 4: Commit only verified browser fixes**

If browser validation finds a defect, add a failing contract assertion first, apply the smallest dashboard change, run
the full verification again, and commit:

```bash
git add telemetry-backend/tests/dashboard-contract.sh telemetry-backend/grafana/dashboards
git commit -m "fix(telemetry): correct browser-verified dashboard behavior"
```

If no defect is found, do not create an empty commit.

### Task 6: Update and publish PR #26

**Files:**

- Create temporarily, do not commit: `/tmp/pr-26-body.md`

**Interfaces:**

- Consumes: the verified branch diff and completed validation commands.
- Produces: an updated reviewer-oriented PR description and pushed `im/feat/remote-codex-metrics`.

- [ ] **Step 1: Re-check contribution and branch state**

Run:

```bash
git status --short --branch
git log --oneline origin/main..HEAD
gh pr view 26 --json title,body,baseRefName,headRefName,url
gh pr checks 26
```

Expected: the worktree is clean before publication, the PR head is `feat/remote-codex-metrics`, and the base is
`main`.

- [ ] **Step 2: Write a reviewer-oriented PR body**

The body must use these sections:

```markdown
## Why
## What
## What this PR does not do
## How to verify
## Live validation
## Follow-up
```

State explicitly:

- the backend now accepts and stores native OTLP metrics;
- dashboards support Codex and Claude Code vendor metrics without Collector-side renaming;
- fixtures prove backend compatibility, not client configuration;
- Codex `0.146.0` and Claude Code `2.1.220` produced the live pilot data;
- this PR does not modify harness configurations or teach the lifecycle installer to configure native exporters;
- users must follow `telemetry-backend/native-otlp-onboarding.md` manually;
- installer automation, directory-based `repo-allow`, clearer APM privilege output, and optional `user.name` are
  follow-up work.

- [ ] **Step 3: Push only to the permitted remote**

Run:

```bash
git push im feat/remote-codex-metrics
```

Expected: `im/feat/remote-codex-metrics` advances to the verified local HEAD.

- [ ] **Step 4: Update the existing PR**

Run:

```bash
gh pr edit 26 --body-file /tmp/pr-26-body.md
gh pr view 26 --json url,title,body
gh pr checks 26
```

Expected: PR #26 explains the backend-versus-client boundary and CI starts or remains green.

- [ ] **Step 5: Report the follow-up boundary**

Report the final commit, pushed branch, PR URL, verification results, and any pending CI. Do not start the follow-up
installer/config implementation inside PR #26.
