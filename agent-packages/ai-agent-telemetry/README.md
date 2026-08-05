# ai-agent-telemetry

This retained package is a compatibility surface for repositories that already install telemetry hooks through APM.
New setups use the platform installer in the [root README](../../README.md#installation), which installs the CLI and
registers machine-wide hooks.

This compatibility package contains native APM hook assets for Claude Code, Codex, and Cursor. Cline is supported by
the machine-wide lifecycle installer, which manages Cline's single global file hook and deploys skills through the
APM `agent-skills` target. This package does not define a duplicate Cline hook.

## How it works

Each hook runs `ai-agent-telemetry ingest --agent=<harness>` and always exits 0 so it never blocks the agent.

| Harness | Hook event and matcher | Event coverage |
| --- | --- | --- |
| Claude Code | `PreToolUse` on `Skill` | Skill execution |
| Claude Code | `UserPromptExpansion` | Command invocation |
| Claude Code | `PostToolUse` on `mcp__.*` | Successful MCP execution |
| Claude Code | `PostToolUseFailure` on `mcp__.*` | Failed MCP execution |
| Codex | `Stop` | Transcript-derived skill execution |
| Codex | `PostToolUse` on `mcp__.*` | MCP execution with unknown outcome |
| Cursor | `afterAgentResponse` | Transcript-derived skill execution |
| Cursor | `afterMCPExecution` | MCP execution with unknown outcome |

The CLI routes the hook payload, writes typed events to a machine-wide outbox, and opportunistically flushes buffered
events to the collector over OTLP/HTTPS. There is no daemon.

## Existing consumers

The binary must be on `PATH` at `~/.local/bin/ai-agent-telemetry` and configured with a collector endpoint. The token
is optional. Install the managed CLI and telemetry once per machine:

```sh
curl -fsSL https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/install.sh | sh
```

On Windows PowerShell:

```powershell
powershell.exe -NoProfile -Command "& ([scriptblock]::Create((Invoke-RestMethod 'https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/install.ps1')))"
```

Use `--components telemetry` when you do not want the default APM and global Git-hook components. For unattended
setup, provide `AI_AGENT_TELEMETRY_ENDPOINT` and the optional `AI_AGENT_TELEMETRY_TOKEN`, then pass
`--non-interactive`.

Existing repositories may keep the package while they migrate. Its manifest and three native hook
sources remain supported as a compatibility surface. To reinstall an existing dependency, use the
same APM target already selected by that repository, for example:

```sh
apm install Netcracker/qubership-ai-agent-telemetry/agent-packages/ai-agent-telemetry --target claude
```

After the platform installer has refreshed the machine-wide hooks, verify the setup and remove the package dependency
through the repository's normal APM workflow. The CLI canonicalizes recognized APM telemetry entries without removing
unrelated hooks.

After refreshing the Codex hook, fully restart Codex. If prompted, inspect and approve exactly
`ai-agent-telemetry ingest --agent=codex`.

Machine-wide lifecycle management uses `ai-agent-telemetry update` and `ai-agent-telemetry uninstall`. Removing this
APM compatibility dependency does not uninstall the managed CLI or machine-wide hooks.
