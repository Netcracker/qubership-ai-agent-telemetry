# Cline MCP tool telemetry implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Emit exact, privacy-bounded `mcp_tool_executed` events from Cline's existing post-tool hook.

**Architecture:** Extend the allowlisted Cline adapter to route skill wrappers, classic `use_mcp_tool` calls, and
reversible direct SDK MCP names. Reuse the existing event constructors, repository attribution, global hook, outbox,
and OTLP mapping. Reject ambiguous SDK identities and never decode MCP arguments, results, or errors.

**Tech stack:** Go, Cline file hooks, JSON, OpenTelemetry logs, POSIX smoke-contract scripts.

## Global constraints

- Work only in `/private/tmp/qubership-ai-agent-telemetry-cline-command-mcp` on
  `feat/cline-command-mcp-telemetry`.
- Preserve existing Cline `skill_executed` behavior and hook lifecycle behavior.
- Do not add a new Cline hook.
- Do not decode or serialize MCP arguments, results, errors, prompts, model data, or user identity.
- Prefer missing telemetry over an incorrectly reconstructed SDK server or tool name.
- Keep committed files in English and Markdown body lines at or below 120 characters.

---

### Task 1: Detect Cline MCP completions

**Files:**

- Modify: `detect_test.go`
- Modify: `privacy_test.go`
- Modify: `detect.go`

**Interfaces:**

- Consumes: `newMCPEvent`, `MCPPayload`, `mcpSucceeded`, `mcpFailed`, `validIdentifier`, and `clineRepository`.
- Produces: `clineMCPIdentity(toolName string, parameters clineParameters) (server, tool string, ok bool)` and
  `clineDuration(executionTime, duration json.RawMessage) *int64`.

- [ ] **Step 1: Write failing detector tests**

  Add `TestDetectClineMCP` with literal expected payloads for:

  - `PostToolUse`, `use_mcp_tool`, explicit `server_name=github`, `tool_name=get_issue`, `success=true`, and
    `executionTimeMs=42`;
  - `tool_result`, the `tool` alias, `success=false`, and `durationMs=17`;
  - reversible direct SDK name `github__get_issue` for both outcomes;
  - zero duration, missing duration, negative duration, fractional duration, and an overflowing integer;
  - missing or non-boolean `success`;
  - missing or invalid classic server and tool names;
  - direct names with no separator, more than one separator, total length 64, and an `_0123abcd` suffix; and
  - unrelated tools, which must not resolve a repository or emit an event.

  Each accepted case asserts `Agent`, `SessionID`, `RepoDir`, `RepoRemote`, `ServerName`, `ToolName`, `Outcome`, and
  `DurationMS` with hand-written literals.

- [ ] **Step 2: Add failing privacy cases**

  Add classic and direct Cline MCP fixtures to `TestPrivacyRawHooksExcludePrivateFieldsFromOutboxAndOTLP`. Put the
  existing forbidden sentinels in `arguments`, arbitrary parameter fields, `result`, `error`, `userId`, `model`, and
  workspace metadata. Keep only safe server and tool names in the expected event.

- [ ] **Step 3: Run the focused tests and verify RED**

  Run:

  ```bash
  go test ./... -run 'TestDetectClineMCP|TestPrivacyRawHooksExcludePrivateFieldsFromOutboxAndOTLP'
  ```

  Expected: `TestDetectClineMCP` fails because the Cline adapter emits no MCP event. The new privacy cases fail because
  policy receives zero events.

- [ ] **Step 4: Implement the allowlisted payload fields**

  Add a named `clineParameters` struct containing only the three existing skill aliases plus `server_name` and
  `tool_name`. Change `success` to `*bool`. Add `executionTimeMs` and `durationMs` as `json.RawMessage` so malformed or
  overflowing duration values do not reject the rest of the hook payload.

- [ ] **Step 5: Implement exact MCP identity normalization**

  Implement `clineMCPIdentity` with these branches:

  ```go
  switch toolName {
  case "use_mcp_tool":
      return parameters.ServerName, parameters.ToolName,
          validIdentifier(parameters.ServerName, mcpIdentifier) &&
              validIdentifier(parameters.ToolName, mcpIdentifier)
  default:
      return normalizeClineDirectMCPName(toolName)
  }
  ```

  `normalizeClineDirectMCPName` accepts exactly one `__`, a complete name shorter than 64 bytes, two valid MCP
  identifiers, and no component matching `_[0-9a-f]{8}$`.

- [ ] **Step 6: Implement outcome and duration mapping**

  Require a present boolean `success` for MCP events. Map `true` to `mcpSucceeded` and `false` to `mcpFailed`.
  `clineDuration` selects `executionTimeMs` first, otherwise `durationMs`; it returns a pointer only when the selected
  JSON value is a non-negative `int64`.

- [ ] **Step 7: Route MCP and skill calls without changing skill semantics**

  Keep skills restricted to `success=true`. For a valid MCP identity, reuse `clineRepository` and call:

  ```go
  newMCPEvent("cline", p.TaskID, repoRemote, repoDir, MCPPayload{
      ServerName: server,
      ToolName:   tool,
      Outcome:    outcome,
      DurationMS: duration,
  }, now)
  ```

  Validate the tool-specific fields before repository resolution so rejected calls cause no Git lookup.

- [ ] **Step 8: Run focused tests and verify GREEN**

  Run:

  ```bash
  go test ./... -run 'TestDetectCline|TestPrivacyRawHooksExcludePrivateFieldsFromOutboxAndOTLP'
  ```

  Expected: PASS with no warnings.

- [ ] **Step 9: Commit the adapter**

  ```bash
  git add detect.go detect_test.go privacy_test.go
  git commit -m "feat(cline): record MCP tool executions"
  ```

### Task 2: Update maintained capability documentation

**Files:**

- Modify: `README.md`
- Modify: `docs/agent-integration.md`
- Modify: `docs/adr/0007-cline-harness-support.md`

**Interfaces:**

- Consumes: the adapter behavior completed in Task 1.
- Produces: the maintained capability contract for Cline users and reviewers.

- [ ] **Step 1: Update the README**

  Extend the Cline paragraph to state that the existing `PostToolUse` hook records skill executions and MCP tool
  completions. List server name, tool name, exact outcome, and optional duration. State that commands remain
  unsupported because Cline removes the command identity before the prompt hook.

- [ ] **Step 2: Update the integration matrix and Cline section**

  Add a matrix row for Cline MCP events. Document classic `use_mcp_tool`, reversible direct SDK names, the two duration
  aliases, exact success/failure mapping, conservative rejection of transformed names, and the privacy exclusions.
  Replace the sentence claiming that only Claude Code emits `command_invoked` with wording that distinguishes command
  support from the MCP rows.

- [ ] **Step 3: Amend ADR 0007**

  Update the decision and consequences to include Cline `mcp_tool_executed`. Preserve the original skill decision and
  record the command limitation. State that the existing hook is sufficient and no lifecycle change is required.

- [ ] **Step 4: Validate Markdown and commit**

  Run:

  ```bash
  awk 'length($0) > 120 { print FNR ":" length($0) ":" $0 }' \
    README.md docs/agent-integration.md docs/adr/0007-cline-harness-support.md
  git diff --check
  ```

  Expected: no output.

  Commit:

  ```bash
  git add README.md docs/agent-integration.md docs/adr/0007-cline-harness-support.md
  git commit -m "docs(cline): document MCP tool telemetry"
  ```

### Task 3: Extend the backend smoke fixture

**Files:**

- Modify: `telemetry-backend/tests/fixtures/otel-events.json`
- Modify: `telemetry-backend/tests/query-contract.sh`
- Modify: `telemetry-backend/tests/with-fixture-stack.sh`

**Interfaces:**

- Consumes: the existing OTLP log attribute contract.
- Produces: a sanitized Cline MCP record exercised by the backend LogsQL contract.

- [ ] **Step 1: Add a sanitized Cline MCP fixture record**

  Add a second record to the existing Cline resource with a unique event ID and timestamp placeholder. Use
  `server_name=github`, `tool_name=get_issue`, `outcome=succeeded`, and `duration_ms=42` through their OTLP attribute
  names.

- [ ] **Step 2: Render the new timestamp placeholder**

  Add `__TS_11__` to `with-fixture-stack.sh` at a second recent timestamp in the same fixture hour. Keep `__TS_9__`
  stale and preserve the existing `__TS_10__` Cline skill timestamp.

- [ ] **Step 3: Update query expectations**

  Change the literal expectations to:

  ```text
  distinct_events=8
  raw_id_events=9
  mcp_events=4
  mcp_succeeded=2
  mcp_failure_rate=0.3333333333333333
  mcp_duration_records=3
  ```

  Keep `mcp_failed=1` and `mcp_unknown=1` unchanged.

- [ ] **Step 4: Validate the fixture and shell contract**

  Run:

  ```bash
  jq empty telemetry-backend/tests/fixtures/otel-events.json
  sh -n telemetry-backend/tests/query-contract.sh
  sh -n telemetry-backend/tests/with-fixture-stack.sh
  git diff --check
  ```

  Expected: all commands exit `0` with no output.

- [ ] **Step 5: Commit the backend fixture**

  ```bash
  git add telemetry-backend/tests/fixtures/otel-events.json telemetry-backend/tests/query-contract.sh \
    telemetry-backend/tests/with-fixture-stack.sh
  git commit -m "test(cline): cover MCP backend ingestion"
  ```

### Task 4: Verify the complete branch

**Files:**

- Verify only: all changed files.

**Interfaces:**

- Consumes: Tasks 1 through 3.
- Produces: clean test and repository evidence for branch completion.

- [ ] **Step 1: Format and inspect**

  Run:

  ```bash
  gofmt -w detect.go detect_test.go privacy_test.go
  git diff --check
  git status --short
  ```

  Expected: only the planned committed history remains and the worktree is clean.

- [ ] **Step 2: Run the full Go suite**

  Run:

  ```bash
  go test ./...
  ```

  Expected: PASS.

- [ ] **Step 3: Run race and static analysis**

  Run:

  ```bash
  go test -race ./...
  go vet ./...
  ```

  Expected: PASS with no diagnostics.

- [ ] **Step 4: Review the final branch diff**

  Run:

  ```bash
  git diff --stat origin/main...HEAD
  git diff --check origin/main...HEAD
  git status --short --branch
  ```

  Expected: the design, adapter, tests, docs, and backend fixture are the only branch changes, with a clean worktree.
