# Telemetry diagnostics APM package implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Update `ai-agent-telemetry-configure` so agents can test and
troubleshoot generic telemetry before the package is included in
`qubership-global-essentials`.

**Architecture:** Keep the existing instruction-and-skill package. The trigger
loads the skill on telemetry setup, checking, testing, troubleshooting, or
repair requests. The CLI remains the only owner of configuration, native
hooks, the outbox, and delivery.

**Tech Stack:** APM 0.25 or newer, Markdown, YAML, markdownlint-cli2

## Global constraints

- Do not add APM lifecycle hooks, prompts, or subagents.
- Do not restore `_apm_source` telemetry registrations.
- Do not read, print, or ask the user to paste a telemetry token.
- Describe only documented harness capabilities.
- Treat `selftest` and installed hooks as the transport baseline. Apply the
  harness-specific real-event evidence rules in Task 2.
- Require a second user turn to verify real skill events in Codex and Cursor.
- Use a read-only MCP call only when it is already configured and appropriate.
- Keep Markdown lines within the repository's 80-character limit.
- Bump the APM package version from `2.3.0` to `2.4.0`.
- Do not dispatch `v1.2.0` or modify `qubership-ai-packages` in this plan.

---

### Task 1: Update package identity and activation contract

**Files:**

- Modify:
  `agent-packages/ai-agent-telemetry-configure/.apm/instructions/ai-agent-telemetry-configure.instructions.md`
- Modify: `agent-packages/ai-agent-telemetry-configure/apm.yml`
- Modify: `agent-packages/ai-agent-telemetry-configure/README.md`
- Modify:
  `agent-packages/ai-agent-telemetry-configure/.apm/skills/ai-agent-telemetry-configure/SKILL.md`

**Interfaces:**

- Consumes: the package layout and trigger mechanism defined by APM.
- Produces: package version `2.4.0` with a trigger for setup, checking,
  testing, troubleshooting, and repair.

- [ ] **Step 1: Confirm that the new trigger is absent**

Run:

```bash
rg -F \
  'When setting up, checking, testing, troubleshooting, or repairing' \
  agent-packages/ai-agent-telemetry-configure/.apm/instructions
```

Expected: exit `1`; the existing instruction does not contain the complete
trigger.

- [ ] **Step 2: Replace the instruction body**

Keep the existing frontmatter and heading. Replace the body below the heading
with:

```markdown
When setting up, checking, testing, troubleshooting, or repairing machine-wide
AI agent telemetry, invoke the `ai-agent-telemetry-configure` skill.
```

- [ ] **Step 3: Update the package manifest**

Replace the manifest with:

```yaml
name: ai-agent-telemetry-configure
version: 2.4.0
description: Agent-guided setup, testing, repair, and delivery verification.
author: Netcracker
```

- [ ] **Step 4: Update the skill frontmatter and opening**

Use this frontmatter and opening:

```markdown
---
name: ai-agent-telemetry-configure
description: Set up, test, troubleshoot, repair, and verify AI agent telemetry.
---

# Configure AI agent telemetry

This machine reports skill executions, command invocations, and MCP tool
executions through `ai-agent-telemetry`. Each harness exposes a documented
subset of those events. The binary needs per-machine configuration that the
public package cannot carry: a collector endpoint, sometimes a CA certificate,
and sometimes a token. Get that configuration in place, prove delivery, verify
a real harness event, and then stop.
```

Keep the existing secret-handling paragraph immediately after this opening.

- [ ] **Step 5: Update the package README introduction**

Replace the opening package description with:

```markdown
This package delivers the optional setup, testing, troubleshooting, repair,
and verification skill for the `ai-agent-telemetry` CLI. The standalone
installers handle binary installation, configuration, and native global hook
registration. This package teaches an agent how to verify and diagnose that
installation.

Install this package when you want the agent to check telemetry on request. It
does not install telemetry hooks itself.
```

Replace the restart-and-test sentence in the installation section with:

```markdown
Restart your agent and ask it to "test AI agent telemetry." The bundled skill
checks configuration, native hooks, collector delivery, and a real harness
event without reading the telemetry token.
```

- [ ] **Step 6: Verify the package identity changes**

Run:

```bash
rg -F \
  'When setting up, checking, testing, troubleshooting, or repairing' \
  agent-packages/ai-agent-telemetry-configure/.apm/instructions
rg -F 'version: 2.4.0' \
  agent-packages/ai-agent-telemetry-configure/apm.yml
rg -F 'does not install telemetry hooks itself' \
  agent-packages/ai-agent-telemetry-configure/README.md
```

Expected: all three commands exit `0` and print one matching line.

- [ ] **Step 7: Lint the changed Markdown**

Run:

```bash
markdownlint_url=https://raw.githubusercontent.com/Netcracker/.github/main
curl -fsSL "$markdownlint_url/config/linters/.markdownlint.json" \
  -o /tmp/netcracker-markdownlint.json
markdownlint-cli2 --config /tmp/netcracker-markdownlint.json \
  'agent-packages/ai-agent-telemetry-configure/**/*.md'
git diff --check
```

Expected: markdownlint reports `0 error(s)` and `git diff --check` prints
nothing.

- [ ] **Step 8: Commit the activation contract**

```bash
git add agent-packages/ai-agent-telemetry-configure
git commit -m "docs(apm): expand telemetry diagnostics trigger"
```

### Task 2: Add the generic-event verification workflow

**Files:**

- Modify:
  `agent-packages/ai-agent-telemetry-configure/.apm/skills/ai-agent-telemetry-configure/SKILL.md`
- Modify: `agent-packages/ai-agent-telemetry-configure/README.md`

**Interfaces:**

- Consumes: `status --verbose`, `selftest`, `hooks install`, and the native
  event coverage documented in `docs/agent-integration.md`.
- Produces: a two-level diagnostic workflow that distinguishes transport
  verification from real harness-event verification.

- [ ] **Step 1: Confirm that generic-event guidance is absent**

Run:

```bash
rg -F 'command_invoked' \
  agent-packages/ai-agent-telemetry-configure/.apm/skills
rg -F 'afterMCPExecution' \
  agent-packages/ai-agent-telemetry-configure/.apm/skills
```

Expected: both commands exit `1` before the update.

- [ ] **Step 2: Add the capability matrix**

After `## What "working" means`, keep the existing command descriptions and
add:

```markdown
Event coverage is harness-specific:

| Harness | Skill execution | Command invocation | MCP tool execution |
| --- | --- | --- | --- |
| Claude Code | Supported | Supported | Supported |
| Codex | Supported | Not supported | Supported |
| Cursor | Supported | Not supported | Supported |

Ordinary built-in tools are not collected. Only Claude Code emits
`command_invoked`. Do not report a missing event type as a failure when the
harness does not expose it.
```

- [ ] **Step 3: Update the main workflow**

Replace the existing workflow list with:

```markdown
1. Ensure the binary is installed by using the installer in
   `references/deployment.md`, then run `status --verbose` by bare name.
2. Fix each configuration or delivery gap that status reports.
3. Run `selftest`. Re-run `status --verbose` and `selftest` after each fix until
   the collector accepts the probe and removes it from the outbox.
4. Repair and verify native global hooks. If Codex is a target, verify its
   CLI-managed execution-policy rule. Require a full harness restart.
5. Verify one real harness event by following `Verify delivery`.
6. Report the outcome without exposing configuration secrets.
7. Run `update-check` and offer an available update without applying it unless
   the user consents.
```

- [ ] **Step 4: Replace the hook-registration table**

Use this table under `## Confirm the global hooks`:

```markdown
| Harness | Active hook file |
| --- | --- |
| Claude Code | `~/.claude/settings.json` |
| Codex | `~/.codex/hooks.json` |
| Cursor | `~/.cursor/hooks.json` |

- Claude Code requires `PreToolUse`/`Skill`, `UserPromptExpansion`, and
  `PostToolUse` plus `PostToolUseFailure`/`mcp__.*`.
- Codex requires `Stop` and `PostToolUse`/`mcp__.*`.
- Cursor requires `afterAgentResponse`, `afterMCPExecution`, and a numeric
  top-level `version`.
```

Immediately after the table, state:

```markdown
These registrations collect only the event subset listed in the capability
matrix. The Codex target also requires
`~/.codex/rules/ai-agent-telemetry.rules`.
```

- [ ] **Step 5: Replace `Verify delivery` with the two-level workflow**

Use this section:

```markdown
## Verify delivery

### Level 1: installation and transport

Run `status --verbose`, then `selftest`. Require all of these results:

- the CLI reports `state: configured`;
- the current harness hook reports `installed`;
- diagnostics contain no delivery error;
- the collector accepts the probe and the probe leaves the outbox.

A nonzero `buffered` value is not automatically a failure. Treat a growing
buffer together with a delivery error as a failure. Fix that error before
continuing.

### Level 2: real harness event

The current `ai-agent-telemetry-configure` invocation is the skill test event.
Record `buffered`, `last_flush_attempt`, and delivery diagnostics after the
level 1 selftest.

Claude Code emits the skill event before this skill runs, and the level 1
`selftest` flushes any queued Claude skill event. Without telemetry-store read
access, the available Claude evidence is therefore the installed native hook
plus passing transport verification. Offer an optional server-side query for
proof of the individual skill event.

Codex and Cursor run their skill-detection hook after the response, so ask the
user to send one more telemetry-check message after this response. On that
next turn, run `status --verbose` again. Accept either outcome:

- `last_flush_attempt` advanced, no new delivery error appeared, and the
  buffer did not grow because of a failed send; or
- `buffered` increased above the post-level-1 baseline with no delivery error,
  which proves that batching queued an event. Run `selftest` to force the full
  outbox to flush, then require `buffered` to return to the recorded baseline
  (normally zero) with no delivery error.

If `last_flush_attempt` did not advance and `buffered` did not grow, no harness
event is observable. Troubleshoot the native hook and full harness restart.

If the user already has read access to the telemetry store, offer a
server-side query as additional evidence. Do not request store credentials in
the conversation. Store access is optional.

Test MCP telemetry only with a read-only tool that is already configured and
appropriate for the user's request. Never mutate external state solely to
create a telemetry event. Test `command_invoked` only in Claude Code and only
with an available harmless slash command.

Do not report success until level 1 passes and the native hook is installed.
For a requested Codex or Cursor real-event test, do not report that part as
complete until one follow-up outcome passes. For Claude Code, do not claim
individual-event proof without a successful store query.
```

- [ ] **Step 6: Update the README behavior summary**

Replace the first paragraph under `## How it works` with:

```markdown
Native CLI-managed hooks collect the event subset supported by each harness:
skill executions on Claude Code, Codex, and Cursor; command invocations on
Claude Code; and MCP tool executions on all three. The diagnostic skill checks
the installation and uses its own invocation as a real skill event.
```

Keep the existing outbox and no-daemon explanation after this paragraph.

- [ ] **Step 7: Verify the new workflow text**

Run:

```bash
package=agent-packages/ai-agent-telemetry-configure
skill=$package/.apm/skills/ai-agent-telemetry-configure/SKILL.md
rg -F 'command_invoked' "$skill"
rg -F 'afterMCPExecution' "$skill"
rg -F 'Level 1: installation and transport' "$skill"
rg -F 'Level 2: real harness event' "$skill"
rg -F 'Never mutate external state solely' "$skill"
```

Expected: every command exits `0` and prints a matching line.

- [ ] **Step 8: Lint and commit the workflow**

Run:

```bash
markdownlint_url=https://raw.githubusercontent.com/Netcracker/.github/main
curl -fsSL "$markdownlint_url/config/linters/.markdownlint.json" \
  -o /tmp/netcracker-markdownlint.json
markdownlint-cli2 --config /tmp/netcracker-markdownlint.json \
  'agent-packages/ai-agent-telemetry-configure/**/*.md'
git diff --check
```

Expected: markdownlint reports `0 error(s)` and `git diff --check` prints
nothing.

Commit:

```bash
git add agent-packages/ai-agent-telemetry-configure
git commit -m "docs(apm): add telemetry event verification workflow"
```

### Task 3: Verify APM deployment for all supported harnesses

**Files:**

- Test only; no repository files change.

**Interfaces:**

- Consumes: the local package at
  `agent-packages/ai-agent-telemetry-configure`.
- Produces: evidence that APM deploys only instructions, skills, and
  references for Claude Code, Codex, and Cursor.

- [ ] **Step 1: Create an isolated APM project**

Run from the repository worktree in one shell:

```bash
package=$PWD/agent-packages/ai-agent-telemetry-configure
smoke_root=$(mktemp -d)
mkdir -p "$smoke_root/home" "$smoke_root/project"
cd "$smoke_root/project"
```

Expected: `smoke_root` names a new directory under the system temporary
directory.

- [ ] **Step 2: Install and compile the package**

Run in the same shell:

```bash
HOME="$smoke_root/home" apm install --dev "$package" \
  --target claude,codex,cursor
HOME="$smoke_root/home" apm compile --target claude,codex,cursor
```

Expected: APM reports two rules integrated into Claude and Cursor outputs, one
skill integrated into agent and Claude outputs, and one generated `AGENTS.md`.

- [ ] **Step 3: Assert the deployed assets**

Run:

```bash
test -f AGENTS.md
test -f .agents/skills/ai-agent-telemetry-configure/SKILL.md
test -f .claude/rules/ai-agent-telemetry-configure.md
test -f .claude/skills/ai-agent-telemetry-configure/SKILL.md
test -f .cursor/rules/ai-agent-telemetry-configure.mdc
rg -F \
  'When setting up, checking, testing, troubleshooting, or repairing' \
  AGENTS.md .claude/rules .cursor/rules
```

Expected: every `test` and `rg` command exits `0`.

- [ ] **Step 4: Assert that no APM telemetry hook was deployed**

Run:

```bash
test ! -e .claude/settings.json
test ! -e .codex/hooks.json
test ! -e .cursor/hooks.json
```

Expected: all commands exit `0`. The package deploys guidance, not hooks.

- [ ] **Step 5: Audit the isolated installation**

Run:

```bash
HOME="$smoke_root/home" apm audit --ci
```

Expected: APM reports all policy and drift checks passed. The missing-org
warning is acceptable because the temporary project has no Git remote.

### Task 4: Final review and PR preparation

**Files:**

- Review all files changed in Tasks 1 and 2.
- Do not add generated APM smoke-project assets to the repository.

**Interfaces:**

- Consumes: the two package-content commits and Task 3 verification evidence.
- Produces: a review-ready PR against `main`; release dispatch remains a user
  decision.

- [ ] **Step 1: Run final repository checks**

Run:

```bash
superpowers=docs/superpowers
design=$superpowers/specs/2026-07-18-telemetry-diagnostics-apm-package-design.md
plan=$superpowers/plans/2026-07-18-telemetry-diagnostics-apm-package.md
markdownlint_url=https://raw.githubusercontent.com/Netcracker/.github/main
curl -fsSL "$markdownlint_url/config/linters/.markdownlint.json" \
  -o /tmp/netcracker-markdownlint.json
markdownlint-cli2 --config /tmp/netcracker-markdownlint.json \
  'agent-packages/ai-agent-telemetry-configure/**/*.md' "$design" "$plan"
git diff --check origin/main...HEAD
git status --short --branch
```

Expected: markdownlint reports `0 error(s)`, the diff check prints nothing,
and the worktree has no uncommitted changes.

- [ ] **Step 2: Review the complete diff**

Run:

```bash
git diff --stat origin/main...HEAD
superpowers=docs/superpowers
design=$superpowers/specs/2026-07-18-telemetry-diagnostics-apm-package-design.md
plan=$superpowers/plans/2026-07-18-telemetry-diagnostics-apm-package.md
git diff origin/main...HEAD -- \
  agent-packages/ai-agent-telemetry-configure "$design" "$plan"
```

Expected: the diff contains only the approved package guidance, package
version bump, design, and plan.

- [ ] **Step 3: Request an independent code review**

Use `superpowers:requesting-code-review` and ask the reviewer to check:

- consistency with `docs/agent-integration.md`;
- privacy and secret-handling requirements;
- Claude Code, Codex, and Cursor capability accuracy;
- the two-turn Codex and Cursor workflow;
- absence of APM telemetry hooks.

Fix valid findings, rerun Tasks 2 and 3 verification, and commit each fix with
a focused Conventional Commit message.

- [ ] **Step 4: Push and create the PR**

Use this title:

```text
feat(apm): teach agents to test telemetry
```

The PR description must use `Why`, `What`, and `How to verify`. Include the
Markdown lint and isolated APM install, compile, and audit commands. State that
the `qubership-global-essentials` dependency is follow-up work after `v1.2.0`.

- [ ] **Step 5: Stop before release dispatch**

Wait for PR review, merge, and green `main` CI. Report release readiness, but
do not run the Release workflow until the user explicitly authorizes
publishing `v1.2.0`.

After `v1.2.0` is published, create a separate plan in the
`qubership-ai-packages` repository for adding this pinned dependency:

```yaml
- Netcracker/qubership-ai-agent-telemetry/agent-packages/ai-agent-telemetry-configure#v1.2.0
```
