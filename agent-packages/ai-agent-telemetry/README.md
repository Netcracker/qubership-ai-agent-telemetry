# ai-agent-telemetry

This package delivers the lifecycle hooks that record skill-usage telemetry for the `ai-agent-telemetry` CLI.
Install it in a repository to have the CLI called automatically after each skill invocation.

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

## Prerequisites

The binary must be on `PATH` at `~/.local/bin/ai-agent-telemetry` and configured with a collector endpoint
and token. Install the companion dev package `ai-agent-telemetry-configure` and run the
`ai-agent-telemetry-configure` skill to complete per-machine setup.

## Install

Install the APM CLI first ([uv](https://docs.astral.sh/uv/): `uv tool install apm-cli`), then add the
package. `--target` is required — without it APM cannot pick a harness and the install fails. It is
one of `claude`, `codex`, `cursor`, or `all`; the example targets Claude Code:

```sh
apm install Netcracker/qubership-ai-agent-telemetry/agent-packages/ai-agent-telemetry --target claude
```

Or add the dependency to your `apm.yml`, pinned to a tag from the
[Releases](https://github.com/Netcracker/qubership-ai-agent-telemetry/releases) page:

```yaml
dependencies:
  apm:
    - Netcracker/qubership-ai-agent-telemetry/agent-packages/ai-agent-telemetry
```

Then install for your agent:

```sh
apm install --target claude
```

On Claude Code that is enough. Codex and other agents that read `AGENTS.md` additionally need
`apm compile --target codex` to register the trigger.

Restart your agent. Installing is the consent boundary — nothing is sent until the binary is configured
with an endpoint and token.
