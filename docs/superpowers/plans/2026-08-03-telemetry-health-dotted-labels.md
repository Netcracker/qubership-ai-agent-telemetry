# Telemetry Health Dotted-Label Display Names Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Render version, harness, and operating-system label values as Telemetry health bar names without changing
VictoriaLogs queries, datasource configuration, or dataframe names.

**Architecture:** Keep each panel's existing single VictoriaLogs target and `legendFormat`. Add a field-level Grafana
display-name expression under `fieldConfig.defaults`, with bracket notation for dotted labels, and lock all three JSON
properties into the dashboard contract. Validate the provisioned dashboard and its existing datasource in local Grafana
before reviewing the live PR feedback and publishing the rebased branch.

**Tech Stack:** Grafana dashboard JSON, VictoriaLogs LogsQL, POSIX shell, jq, Docker Compose, GitHub CLI

## Global Constraints

- Keep the existing VictoriaLogs datasource and both LogsQL queries unchanged.
- Keep exactly one target in each affected panel.
- Preserve `{{service.version}}` and `{{agent}} · {{os.type}}` as the target legend formats.
- Set display names only at `.fieldConfig.defaults.displayName`; do not use field overrides.
- Use `${__field.labels["service.version"]}` for the version bar.
- Use `${__field.labels.agent} · ${__field.labels["os.type"]}` for the harness and operating-system bar.
- Publish only after checking all PR issue comments, reviews, and inline review threads.

---

### Task 1: Lock the dashboard JSON contract and implement the display names

**Files:**

- Modify: `telemetry-backend/tests/dashboard-contract.sh:249`
- Modify: `telemetry-backend/grafana/dashboards/telemetry-health.json`

**Interfaces:**

- Consumes: the two existing bar-gauge panels and their single VictoriaLogs targets.
- Produces: exact `.fieldConfig.defaults.displayName` values while retaining exact `.targets[0].legendFormat` values.

- [ ] **Step 1: Write the failing contract**

Add this assertion after the existing active-distributions query contract:

```sh
jq -e '
  (.panels[] | select(.title == "Active installations by version")
    | (.targets | length == 1) and
      .targets[0].legendFormat == "{{service.version}}" and
      .fieldConfig.defaults.displayName == "${__field.labels[\"service.version\"]}") and
  (.panels[] | select(.title == "Active installations by harness and OS")
    | (.targets | length == 1) and
      .targets[0].legendFormat == "{{agent}} · {{os.type}}" and
      .fieldConfig.defaults.displayName ==
        "${__field.labels.agent} · ${__field.labels[\"os.type\"]}")
' "$health_path" >/dev/null ||
  fail "$health active distributions must preserve dataframe legends and set field display names"
```

- [ ] **Step 2: Run the contract and verify the red state**

Run:

```bash
sh telemetry-backend/tests/dashboard-contract.sh
```

Expected: exit status `1` and
`FAIL: telemetry-health.json active distributions must preserve dataframe legends and set field display names`.

- [ ] **Step 3: Add the minimal dashboard properties**

Add only these properties to the respective `fieldConfig.defaults` objects:

```json
"displayName": "${__field.labels[\"service.version\"]}"
```

```json
"displayName": "${__field.labels.agent} · ${__field.labels[\"os.type\"]}"
```

Do not modify either target, query, datasource, or `legendFormat`.

- [ ] **Step 4: Run focused verification and verify the green state**

Run:

```bash
jq empty telemetry-backend/grafana/dashboards/telemetry-health.json
sh -n telemetry-backend/tests/dashboard-contract.sh
sh telemetry-backend/tests/dashboard-contract.sh
git diff --check
```

Expected: every command exits with status `0`.

- [ ] **Step 5: Commit the tested implementation**

```bash
git add telemetry-backend/tests/dashboard-contract.sh \
  telemetry-backend/grafana/dashboards/telemetry-health.json
git commit -m "fix(grafana): render telemetry health labels"
```

### Task 2: Verify the provisioned dashboard against the existing local datasource

**Files:**

- Runtime input: `telemetry-backend/grafana/dashboards/telemetry-health.json`
- Runtime destination: `/home/ildar/.local/share/rtk-codex/ai-agent-telemetry-live/dashboards/telemetry-health.json`

**Interfaces:**

- Consumes: the tested dashboard JSON and the running `ai-agent-telemetry-live-grafana-1` container.
- Produces: evidence that Grafana provisions the field display names and renders the datasource's dotted labels.

- [ ] **Step 1: Confirm the existing stack and datasource before deployment**

Run:

```bash
docker ps --filter name=ai-agent-telemetry-live-grafana-1
docker exec ai-agent-telemetry-live-grafana-1 sh -c \
  'curl -fsS -u "${GF_SECURITY_ADMIN_USER}:${GF_SECURITY_ADMIN_PASSWORD}" \
  http://127.0.0.1:3000/api/datasources/uid/victorialogs'
```

Expected: Grafana is running and datasource UID `victorialogs` still points to the pre-existing remote VictoriaLogs URL.

- [ ] **Step 2: Provision the changed dashboard without touching datasource configuration**

```bash
cp telemetry-backend/grafana/dashboards/telemetry-health.json \
  /home/ildar/.local/share/rtk-codex/ai-agent-telemetry-live/dashboards/telemetry-health.json
```

Poll `GET /api/dashboards/uid/ai-agent-health` through authenticated `docker exec` until both exact display-name
properties appear in the provisioned dashboard. Do not recreate or restart the stack.

- [ ] **Step 3: Inspect both queries through Grafana**

Open `http://127.0.0.1:13000/d/ai-agent-health` with the browser MCP. In Query Inspector, run each panel query and
confirm that the numeric field contains these label keys under `schema.fields[].labels`:

- version query: `service.version`;
- harness and operating-system query: `agent` and `os.type`.

Also confirm that the dataframe names still include representative values such as `v1.2.0` and `codex · linux`.

- [ ] **Step 4: Inspect the rendered bar gauges**

Confirm in the browser that:

- a version bar is named `v1.2.0`;
- a harness and operating-system bar is named `codex · linux`;
- any missing grouped value is rendered with `Unknown` in its name;
- no bar name starts with `installations`.

Capture the browser state or screenshot as verification evidence.

### Task 3: Review PR feedback, rebase, run release checks, and publish

**Files:**

- Review: repository contribution instructions and the complete PR #26 discussion
- Modify: only files required by feedback that is relevant and technically correct

**Interfaces:**

- Consumes: the locally verified commits and current upstream `main`.
- Produces: a rebased, fully checked `im/feat/remote-codex-metrics` branch for PR #26.

- [ ] **Step 1: Read contribution rules and all current PR feedback**

Run:

```bash
gh pr view 26 --repo Netcracker/qubership-ai-agent-telemetry \
  --json comments,reviews,reviewDecision,statusCheckRollup
gh api --paginate repos/Netcracker/qubership-ai-agent-telemetry/pulls/26/comments
gh api graphql -F owner=Netcracker -F name=qubership-ai-agent-telemetry -F number=26 \
  -f query='query($owner:String!,$name:String!,$number:Int!){repository(owner:$owner,name:$name){pullRequest(number:$number){reviewThreads(first:100){nodes{isResolved comments(first:100){nodes{author{login} body path line url}}}}}}}'
```

Compare each unresolved or new item with the implementation and apply only feedback that is still applicable. If code
changes, repeat Tasks 1 and 2 before continuing.

- [ ] **Step 2: Rebase on the current upstream base**

```bash
git fetch origin main
git rebase origin/main
```

Expected: the branch is based on current `origin/main`; resolve no conflict by discarding unrelated user changes.

- [ ] **Step 3: Run the relevant repository checks**

```bash
jq empty telemetry-backend/grafana/dashboards/telemetry-health.json
sh -n telemetry-backend/tests/dashboard-contract.sh
sh telemetry-backend/tests/dashboard-contract.sh
TEST_HTTP_PORT=28080 TEST_HTTPS_PORT=28443 sh telemetry-backend/tests/smoke.sh
sh telemetry-backend/tests/config-contract.sh
git diff --check origin/main...HEAD
git status --short --branch
```

Expected: all checks pass and the worktree contains no uncommitted changes.

- [ ] **Step 4: Recheck the PR after the final commit and publish only to the fork remote**

Repeat the three PR-feedback queries from Step 1. If there is no new applicable feedback, run:

```bash
git push --force-with-lease im feat/remote-codex-metrics
gh pr checks 26 --repo Netcracker/qubership-ai-agent-telemetry --watch
```

Expected: the existing PR #26 updates, and its checks complete successfully.
