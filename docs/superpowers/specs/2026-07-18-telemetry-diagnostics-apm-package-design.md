# Telemetry diagnostics APM package design

## Context

The developer installer installs `qubership-global-essentials`, the telemetry
CLI, and CLI-managed global hooks. The agent baseline does not include the
existing `ai-agent-telemetry-configure` package. An installed agent may
therefore not know how to test or troubleshoot telemetry when the user asks.

The configure package already has the right primitive shape: a short trigger
instruction and an on-demand skill. It predates generic telemetry events and
still describes the product primarily as skill-usage telemetry. Its hook table
also omits the command and MCP registrations added for the versioned event
schema.

## Decision

Update the existing `ai-agent-telemetry-configure` package and include it as a
required transitive dependency of `qubership-global-essentials`. Do not create
another diagnostics package.

The CLI remains the only owner of configuration, native hooks, the outbox, and
event delivery. The APM package provides agent guidance only. It must not
contain APM lifecycle hooks or restore the retired `_apm_source` telemetry
registrations.

The work spans two repositories and one release:

1. Merge the installer migration that removes the legacy telemetry APM hook
   package.
2. Update and release `ai-agent-telemetry-configure` in
   `qubership-ai-agent-telemetry` as part of `v1.2.0`.
3. Add the released package to `qubership-global-essentials` in
   `qubership-ai-packages`.

## Package trigger

Keep the always-on instruction short because APM compiles it into the harness's
global context. Use this trigger:

> When setting up, checking, testing, troubleshooting, or repairing
> machine-wide AI agent telemetry, invoke the
> `ai-agent-telemetry-configure` skill.

The trigger covers both diagnosis and repair. The skill body contains the
detailed workflow and is loaded only when the request matches.

## Diagnostic contract

The skill uses two verification levels.

### Installation and transport verification

This level is mandatory:

1. Run `ai-agent-telemetry status --verbose` by bare name.
2. Confirm that the CLI reports `configured`, the selected harness hooks report
   `installed`, and diagnostics contain no delivery error.
3. Inspect `buffered` and `last_flush_attempt`. A nonzero buffer is not
   automatically an error, but a growing buffer with a delivery error requires
   troubleshooting.
4. Run `ai-agent-telemetry selftest` and require collector acceptance and
   removal of the probe from the outbox.
5. Repair missing native hooks through `ai-agent-telemetry hooks install`, then
   require a full harness restart.

The agent must not read, print, or ask the user to paste the telemetry token.
The CLI owns secret input and persistent configuration. The agent must not
hand-edit native hook JSON while the CLI can perform the repair safely.

### Real harness-event verification

Use the configure skill invocation itself as the skill signal. Event coverage
differs by harness:

| Harness | Skill execution | Command invocation | MCP tool execution |
| --- | --- | --- | --- |
| Claude Code | Supported | Supported | Supported |
| Codex | Supported | Not supported | Supported |
| Cursor | Supported | Not supported | Supported |

Claude Code emits its skill event before the skill runs. Codex and Cursor
detect skill use after the response, so their verification requires a second
user turn:

1. Record `buffered`, `last_flush_attempt`, and delivery diagnostics.
2. Complete the response that invokes the configure skill so the harness hook
   can run.
3. On the user's next telemetry-check request, run `status --verbose` again.
4. Confirm that `last_flush_attempt` advanced, no new delivery error appeared,
   and the buffer did not grow because of a failed send.

Test MCP telemetry only with a read-only tool that is already configured and
appropriate for the user's request. Do not mutate external state solely to
create a telemetry event. Test `command_invoked` only in Claude Code and only
with an available harmless slash command.

If the user already has read access to the telemetry store, offer a server-side
query as additional evidence. Store access is optional. Do not request store
credentials or tokens in the conversation. A passing `selftest`, installed
hooks, and a successful subsequent flush are the baseline success criteria.

## Harness guidance

Update the package's native-hook reference to match the released capability
matrix:

- Claude Code: `PreToolUse` for skills, `UserPromptExpansion` for commands, and
  `PostToolUse` or `PostToolUseFailure` for MCP tools.
- Codex: `Stop` for transcript-derived skills and `PostToolUse` for MCP tools,
  plus the CLI-managed execution policy.
- Cursor: `afterAgentResponse` for transcript-derived skills and
  `afterMCPExecution` for MCP tools.

The skill must state that ordinary built-in tools are not collected and that
only Claude Code emits command events. It must not promise uniform event
support across harnesses.

## Repository changes

### `qubership-ai-agent-telemetry`

Update these package files:

- the trigger instruction;
- `SKILL.md`, including terminology, the two-level workflow, capability matrix,
  and current hook registrations;
- the package README;
- references only where generic-event behavior makes existing text stale;
- `apm.yml`, changing the package version from `2.3.0` to `2.4.0`.

This change does not modify the CLI, event schema, native hook implementation,
or release workflow.

### `qubership-ai-packages`

After `v1.2.0` is published, add this dependency to
`qubership-global-essentials`:

```yaml
dependencies:
  apm:
    - Netcracker/qubership-ai-agent-telemetry/agent-packages/ai-agent-telemetry-configure#v1.2.0
```

The `#v1.2.0` suffix follows the APM pinned-ref syntax documented in
[Manage dependencies][apm-dependencies]. Bump the umbrella package from
`1.0.1` to `1.1.0`, then update its description, README, and package list.

The umbrella remains content-free. It acquires the telemetry instruction and
skill only through transitive dependency resolution.

## Validation

Validate the telemetry package before the release:

- run the repository's Markdown and YAML checks;
- install the package in an isolated temporary APM project with targets
  `claude,codex,cursor`;
- run `apm compile --target claude,codex,cursor` in that project;
- confirm that each target receives the trigger and skill;
- confirm that no APM telemetry hook is deployed.

Validate the umbrella PR after the release:

- run the `qubership-ai-packages` package validation;
- install and compile `qubership-global-essentials` for Claude Code, Codex, and
  Cursor in an isolated user scope;
- confirm that `ai-agent-telemetry-configure` arrives transitively;
- confirm that the installation adds guidance only and does not add
  `_apm_source` telemetry hooks.

## Release sequence

Do not publish `v1.2.0` until the legacy-hook migration and configure-package
update are merged and their CI checks are green. The release workflow creates
the tag and assets; do not create the tag manually.

After `v1.2.0` is available, merge the umbrella PR pinned to that tag. Existing
users receive the new skill when they next update
`qubership-global-essentials`. New overall-installer runs receive it during the
normal APM installation.

## Out of scope

- A new telemetry diagnostics package, prompt, subagent, or APM lifecycle hook.
- Automatic access to VictoriaLogs or another telemetry backend.
- Collection of additional event types or fields.
- Changes to token storage or configuration ownership.
- Automatic execution of state-changing MCP tools for testing.

[apm-dependencies]: https://microsoft.github.io/apm/guides/dependencies/
