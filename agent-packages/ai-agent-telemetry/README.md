# ai-agent-telemetry

This package is only for repositories that already install telemetry hooks through APM. For a new
machine, use the platform installer in the [root README](../../README.md#installation). It installs
the CLI and registers Claude Code, Codex, and Cursor for every repository on that machine.

Supported agents: Claude Code, Codex, and Cursor.

## How it works

Each hook runs `ai-agent-telemetry ingest --agent=<harness>` and always exits 0 so it never blocks the agent.

| Harness | Hook event | Detection method |
| --- | --- | --- |
| Claude Code | `PreToolUse` on the `Skill` tool | Native hook event |
| Codex | `Stop` | Session transcript |
| Cursor | `afterAgentResponse` | Session transcript |

The CLI detects the skill from the hook payload, writes the event to a machine-global outbox, and
opportunistically flushes buffered events to the collector over OTLP/HTTPS. There is no daemon.

## Existing consumers

The binary must be on `PATH` at `~/.local/bin/ai-agent-telemetry` and configured with a
collector endpoint and token. Install it once per machine:

```sh
curl -fsSL https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/install.sh | sh
```

On Windows PowerShell:

```powershell
iex "& { $(irm https://github.com/Netcracker/qubership-ai-agent-telemetry/releases/latest/download/install.ps1) }"
```

Existing repositories may keep the package while they migrate. Its manifest and three native hook
sources remain supported as a compatibility surface. To reinstall an existing dependency, use the
same APM target already selected by that repository, for example:

```sh
apm install Netcracker/qubership-ai-agent-telemetry/agent-packages/ai-agent-telemetry --target claude
```

After the platform installer has refreshed the machine-wide hooks, verify the setup, remove the
package dependency through the repository's normal APM workflow, and fully restart the harness.
The CLI canonicalizes recognized APM telemetry entries without removing unrelated hooks.
